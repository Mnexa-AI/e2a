package piguard

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// EngineConfig tunes aggregation. These are operator (deployment-level) knobs; the
// zero value is usable via sensible defaults applied in NewEngine.
type EngineConfig struct {
	// Weights is the per-detector weight in the aggregate (by Detector.Name).
	// Missing → 1.0.
	Weights map[string]float64
	// Timeout bounds each detector's Inspect. A detector that exceeds it is recorded
	// as StatusTimeout and EXCLUDED from the aggregate (never counted as benign).
	Timeout time.Duration
	// MinOK is the minimum number of detectors that must return StatusOK for the
	// aggregate to be trusted. Below it, the Engine reports Degraded → the caller
	// maps to review (fail-to-review). Default 1.
	MinOK int
}

const defaultDetectorTimeout = 5 * time.Second

// Engine runs the registered detectors in parallel and normalizes their verdicts
// into one Aggregate. It is safe for concurrent use.
type Engine struct {
	detectors []Detector
	cfg       EngineConfig
}

// NewEngine builds an Engine. At least one detector is expected; an Engine with no
// detectors always reports Degraded (fail-to-review), which is the safe posture.
func NewEngine(cfg EngineConfig, detectors ...Detector) *Engine {
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultDetectorTimeout
	}
	if cfg.MinOK <= 0 {
		cfg.MinOK = 1
	}
	return &Engine{detectors: detectors, cfg: cfg}
}

// Timeout returns the effective per-detector timeout, e.g. for wiring-layer tests
// that assert a caller configured a non-default timeout (see buildScreenEngine in
// internal/relay/server.go, which widens this when the Gemini detector is present).
func (e *Engine) Timeout() time.Duration { return e.cfg.Timeout }

// Aggregate is the combined verdict plus the control signals the wiring layer needs
// to choose an action without knowing per-detector internals.
type Aggregate struct {
	Result
	// Degraded is true when fewer than MinOK detectors returned StatusOK. The caller
	// must map a degraded aggregate to review — NOT allow, NOT block.
	Degraded bool
	// MinAction is a deterministic force-override floor: when a high-confidence
	// deterministic marker is present (e.g. Unicode Tags-block smuggling, or
	// truncated/unscannable content), the applied action is at least this severe
	// regardless of score. ActionAllow when no override fires.
	MinAction Action
	// Truncated is true when DecodedSignals.Truncated (the content exceeded the
	// extraction-level scan cap) OR any StatusOK detector reported its own
	// Result.Truncated (e.g. Gemini's provider-side content-length cap cut off part
	// of the message before it was even sent) — either way, only partially
	// inspected. Folded into MinAction (→ review) but exposed for audit/metrics.
	Truncated bool
	// PerDetector retains every detector's raw Result (including timeouts/errors)
	// for audit and for writing protection_events rows.
	PerDetector []Result
}

// DetectorLabel returns a stable, sorted, comma-joined list of the names of
// detectors that actually contributed a StatusOK verdict to this aggregate.
// Callers writing an audit row (e.g. protection_events.detector) should use this
// instead of hardcoding a single detector name — with more than one detector
// wired into the Engine, a hardcoded name silently mislabels which detector(s)
// actually drove the recorded score/action. Empty when no detector returned
// StatusOK (a Degraded aggregate); PerDetector still has every detector's Result,
// including timeouts/errors, for a caller that wants the full breakdown (e.g.
// marshaled into an audit row's raw/JSONB column).
func (a Aggregate) DetectorLabel() string {
	var names []string
	for _, r := range a.PerDetector {
		if r.Status == StatusOK {
			names = append(names, r.Provider.Name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// Action folds the aggregate into a single applied action using the per-agent
// threshold ladder, guaranteeing the fail-safe defaults so call sites can't
// accidentally silent-allow: a degraded aggregate (too few usable verdicts) routes
// to review; the deterministic force-override floor (MinAction — truncated→review,
// Unicode-tags→flag) always applies; and a NaN score can never read as allow.
// Prefer this over calling ActionForScore(agg.Score, …) directly.
func (a Aggregate) Action(reviewThreshold, blockThreshold float64) Action {
	if a.Degraded {
		return MoreSevere(ActionReview, a.MinAction)
	}
	return MoreSevere(ActionForScore(a.Score, reviewThreshold, blockThreshold), a.MinAction)
}

// Evaluate fans out to all detectors with a per-detector timeout, then aggregates.
// It never panics: a detector that panics is recorded as StatusError and excluded.
func (e *Engine) Evaluate(ctx context.Context, req Request) Aggregate {
	results := make([]Result, len(e.detectors))
	var wg sync.WaitGroup
	for i, d := range e.detectors {
		wg.Add(1)
		go func(i int, d Detector) {
			defer wg.Done()
			results[i] = e.runOne(ctx, d, req)
		}(i, d)
	}
	wg.Wait()
	return e.aggregate(req, results)
}

// runOne invokes one detector under a timeout, converting panics and errors into a
// non-OK Result so a bad backend can never crash the relay or be mistaken for benign.
func (e *Engine) runOne(ctx context.Context, d Detector, req Request) (res Result) {
	cctx, cancel := context.WithTimeout(ctx, e.cfg.Timeout)
	defer cancel()

	type outcome struct {
		r   *Result
		err error
	}
	ch := make(chan outcome, 1)
	start := time.Now()
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				ch <- outcome{nil, errPanic}
			}
		}()
		r, err := d.Inspect(cctx, req)
		ch <- outcome{r, err}
	}()

	name := d.Name()
	select {
	case <-cctx.Done():
		return Result{Status: StatusTimeout, Provider: ProviderMeta{Name: name, LatencyMS: int(e.cfg.Timeout / time.Millisecond)}}
	case o := <-ch:
		latency := int(time.Since(start) / time.Millisecond)
		if o.err != nil || o.r == nil {
			return Result{Status: StatusError, Provider: ProviderMeta{Name: name, LatencyMS: latency}}
		}
		o.r.Provider.Name = name // ensure attribution even if a detector forgot
		o.r.Provider.LatencyMS = latency
		// Sanitize the score. A NaN/Inf/negative score is an unusable verdict: a
		// buggy or hostile adapter (or a deserialized NaN/Inf from a network
		// provider) must not be able to poison the weighted aggregate into allow or
		// erase a co-detector's score. Exclude it like a timeout; clamp >1 to 1.
		if math.IsNaN(o.r.Score) || math.IsInf(o.r.Score, 0) || o.r.Score < 0 {
			return Result{Status: StatusError, Provider: ProviderMeta{Name: name, LatencyMS: latency}}
		}
		if o.r.Score > 1 {
			o.r.Score = 1
		}
		return *o.r
	}
}

// aggregate combines the per-detector results: weighted average of StatusOK scores,
// per-category max, the Degraded check, and the deterministic force-override floor.
func (e *Engine) aggregate(req Request, results []Result) Aggregate {
	var weightSum, scoreSum float64
	okCount := 0
	flagged := false
	detectorTruncated := false
	catScores := map[string]float64{}
	catNative := map[string]string{}
	var spans []Span

	for _, r := range results {
		if r.Status != StatusOK {
			continue
		}
		okCount++
		w := e.weight(r.Provider.Name)
		weightSum += w
		scoreSum += w * r.Score
		if r.Flagged {
			flagged = true
		}
		if r.Truncated {
			detectorTruncated = true
		}
		for _, c := range r.Categories {
			if c.Score > catScores[c.Name] {
				catScores[c.Name] = c.Score
			}
			if c.NativeCode != "" {
				catNative[c.Name] = c.NativeCode
			}
		}
		spans = append(spans, r.Spans...)
	}

	agg := Aggregate{
		PerDetector: results,
		MinAction:   ActionAllow,
		Truncated:   req.Signals.Truncated || detectorTruncated,
	}
	if okCount < e.cfg.MinOK {
		// Fail-to-review: too few usable verdicts to trust. Mark the aggregate as not
		// OK so callers cannot read it as a benign allow.
		agg.Degraded = true
		agg.Result.Status = StatusError
		agg.MinAction = e.forceFloor(req, detectorTruncated)
		return agg
	}

	if weightSum > 0 {
		agg.Result.Score = scoreSum / weightSum
	}
	agg.Result.Flagged = flagged
	agg.Result.Status = StatusOK
	agg.Result.Spans = spans
	cats := make([]Category, 0, len(catScores))
	for name, sc := range catScores {
		cats = append(cats, Category{Name: name, NativeCode: catNative[name], Score: sc})
	}
	sortCategories(cats)
	agg.Result.Categories = cats
	agg.MinAction = e.forceFloor(req, detectorTruncated)
	return agg
}

// forceFloor returns the deterministic minimum action implied by high-confidence
// signals, independent of the (possibly low) aggregate score. detectorTruncated is
// true when any StatusOK detector reported Result.Truncated (e.g. Gemini's
// provider-side content cap cut off part of the message) — treated the same as
// extraction-level truncation: a low score from a detector that didn't see
// everything is not a safety guarantee.
func (e *Engine) forceFloor(req Request, detectorTruncated bool) Action {
	floor := ActionAllow
	if req.Signals.UnicodeTags {
		floor = MoreSevere(floor, ActionFlag)
	}
	if req.Signals.Truncated || detectorTruncated {
		// Unscannable content (scan cap hit) is not a safety guarantee — design §5
		// routes it to review rather than treating "no finding" as benign.
		floor = MoreSevere(floor, ActionReview)
	}
	if req.Signals.Unscannable {
		// A non-text part we couldn't read (binary attachment) is likewise not a
		// safety guarantee → review.
		floor = MoreSevere(floor, ActionReview)
	}
	return floor
}

func (e *Engine) weight(name string) float64 {
	if w, ok := e.cfg.Weights[name]; ok && w > 0 {
		return w
	}
	return 1.0
}

// errPanic is the sentinel a recovered detector panic resolves to.
var errPanic = &detectorError{"detector panicked"}

type detectorError struct{ msg string }

func (e *detectorError) Error() string { return e.msg }
