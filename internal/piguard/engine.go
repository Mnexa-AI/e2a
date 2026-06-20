package piguard

import (
	"context"
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

// Aggregate is the combined verdict plus the control signals the wiring layer needs
// to choose an action without knowing per-detector internals.
type Aggregate struct {
	Result
	// Degraded is true when fewer than MinOK detectors returned StatusOK. The caller
	// must map a degraded aggregate to review — NOT allow, NOT block.
	Degraded bool
	// MinAction is a deterministic force-override floor: when a high-confidence
	// deterministic marker is present (e.g. Unicode Tags-block smuggling), the
	// applied action is at least this severe regardless of score. ActionAllow when
	// no override fires.
	MinAction Action
	// PerDetector retains every detector's raw Result (including timeouts/errors)
	// for audit and for writing screening_events rows.
	PerDetector []Result
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
		return *o.r
	}
}

// aggregate combines the per-detector results: weighted average of StatusOK scores,
// per-category max, the Degraded check, and the deterministic force-override floor.
func (e *Engine) aggregate(req Request, results []Result) Aggregate {
	var weightSum, scoreSum float64
	okCount := 0
	flagged := false
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

	agg := Aggregate{PerDetector: results, MinAction: ActionAllow}
	if okCount < e.cfg.MinOK {
		// Fail-to-review: too few usable verdicts to trust. Mark the aggregate as not
		// OK so callers cannot read it as a benign allow.
		agg.Degraded = true
		agg.Result.Status = StatusError
		agg.MinAction = e.forceFloor(req)
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
	agg.MinAction = e.forceFloor(req)
	return agg
}

// forceFloor returns the deterministic minimum action implied by high-confidence
// signals, independent of the (possibly low) aggregate score.
func (e *Engine) forceFloor(req Request) Action {
	if req.Signals.UnicodeTags {
		return ActionFlag
	}
	return ActionAllow
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
