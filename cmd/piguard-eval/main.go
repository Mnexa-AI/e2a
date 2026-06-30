// piguard-eval is an offline evaluation harness for the piguard heuristics detector.
//
// Reads a manifest JSONL (one EmailRecord per line) from --manifest or stdin, screens
// each .eml with the built-in HeuristicsDetector, and writes one prediction JSON line
// per message to stdout.
//
// Usage:
//
//	piguard-eval --base-dir /path/to/e2a-paper --manifest dataset/prompt-injection/manifest.jsonl
//	cat combined.jsonl | piguard-eval --base-dir /path/to/e2a-paper
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/Mnexa-AI/e2a/internal/piguard"
)

type manifestEntry struct {
	ID      string `json:"id"`
	EMLPath string `json:"eml_path"`
}

type signalsSummary struct {
	UnicodeTags      bool    `json:"unicode_tags,omitempty"`
	ZeroWidth        bool    `json:"zero_width,omitempty"`
	HiddenCSSText    bool    `json:"hidden_css_text,omitempty"`
	HomoglyphRatio   float64 `json:"homoglyph_ratio,omitempty"`
	PlainHTMLDiverge bool    `json:"plain_html_diverge,omitempty"`
	FragmentedURL    bool    `json:"fragmented_url,omitempty"`
	Truncated        bool    `json:"truncated,omitempty"`
	Unscannable      bool    `json:"unscannable,omitempty"`
}

type predictionRecord struct {
	ID         string             `json:"id"`
	Detector   string             `json:"detector"`
	Flagged    bool               `json:"flagged"`
	Score      float64            `json:"score"`
	Action     string             `json:"action"`
	Categories []piguard.Category `json:"categories,omitempty"`
	LatencyMS  int                `json:"latency_ms"`
	Degraded   bool               `json:"degraded,omitempty"`
	Truncated  bool               `json:"truncated,omitempty"`
	Signals    signalsSummary     `json:"signals"`
	Error      string             `json:"error,omitempty"`
}

// segmentDumpRecord is the canonical detector-input dump consumed by the Python
// eval harness via PIGUARD_SEGMENTS (one line per message, keyed by id).
type segmentOut struct {
	Type    string `json:"type"`
	Content string `json:"content"`
	Ref     string `json:"ref"`
}

type segmentDumpRecord struct {
	ID       string         `json:"id"`
	Segments []segmentOut   `json:"segments"`
	Signals  signalsSummary `json:"signals"`
	Error    string         `json:"error,omitempty"`
}

func main() {
	manifestPath := flag.String("manifest", "", "manifest JSONL path (default: stdin)")
	baseDir := flag.String("base-dir", ".", "root for resolving eml_path fields")
	reviewThreshold := flag.Float64("review-threshold", 0.35, "score ≥ this → review")
	blockThreshold := flag.Float64("block-threshold", 0.75, "score ≥ this → block")
	dumpSegments := flag.Bool("dump-segments", false,
		"emit {id,segments,signals} from Extract (canonical detector input) instead of verdicts")
	flag.Parse()

	var scanner *bufio.Scanner
	if *manifestPath != "" {
		f, err := os.Open(*manifestPath)
		if err != nil {
			log.Fatalf("open manifest: %v", err)
		}
		defer f.Close()
		scanner = bufio.NewScanner(f)
	} else {
		scanner = bufio.NewScanner(os.Stdin)
	}
	// 4 MiB line buffer — some manifest lines with inline body content are large.
	scanner.Buffer(make([]byte, 4<<20), 4<<20)

	engine := piguard.NewEngine(piguard.EngineConfig{}, piguard.NewHeuristicsDetector())
	ctx := context.Background()
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	for scanner.Scan() {
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		var entry manifestEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			fmt.Fprintf(os.Stderr, "skip malformed line: %v\n", err)
			continue
		}
		if *dumpSegments {
			emitJSON(out, dumpEntry(entry, *baseDir))
		} else {
			emit(out, evalEntry(ctx, engine, entry, *baseDir, *reviewThreshold, *blockThreshold))
		}
	}
	if err := scanner.Err(); err != nil {
		log.Fatalf("scan: %v", err)
	}
}

func evalEntry(ctx context.Context, engine *piguard.Engine, entry manifestEntry,
	baseDir string, reviewThreshold, blockThreshold float64) predictionRecord {

	emlPath := filepath.Join(baseDir, entry.EMLPath)
	rawEML, err := os.ReadFile(emlPath)
	if err != nil {
		return predictionRecord{ID: entry.ID, Detector: "piguard",
			Error: fmt.Sprintf("read eml: %v", err)}
	}

	start := time.Now()
	segments, signals, _ := piguard.Extract(rawEML, 0)
	req := piguard.Request{
		Direction: piguard.DirectionInput,
		Segments:  segments,
		Signals:   signals,
	}
	agg := engine.Evaluate(ctx, req)
	latencyMS := int(time.Since(start).Milliseconds())

	action := agg.Action(reviewThreshold, blockThreshold)
	return predictionRecord{
		ID:         entry.ID,
		Detector:   "piguard",
		Flagged:    agg.Flagged || action == piguard.ActionReview || action == piguard.ActionBlock,
		Score:      agg.Score,
		Action:     string(action),
		Categories: agg.Categories,
		LatencyMS:  latencyMS,
		Degraded:   agg.Degraded,
		Truncated:  agg.Truncated,
		Signals: signalsSummary{
			UnicodeTags:      signals.UnicodeTags,
			ZeroWidth:        signals.ZeroWidth,
			HiddenCSSText:    signals.HiddenCSSText,
			HomoglyphRatio:   signals.HomoglyphRatio,
			PlainHTMLDiverge: signals.PlainHTMLDiverge,
			FragmentedURL:    signals.FragmentedURL,
			Truncated:        signals.Truncated,
			Unscannable:      signals.Unscannable,
		},
	}
}

func emit(w *bufio.Writer, rec predictionRecord) {
	b, _ := json.Marshal(rec)
	w.Write(b)
	w.WriteByte('\n')
}

func emitJSON(w *bufio.Writer, v any) {
	b, _ := json.Marshal(v)
	w.Write(b)
	w.WriteByte('\n')
}

// dumpEntry emits the canonical segments + signals piguard's Extract produces
// for an .eml — the exact input the engine's detectors screen.
func dumpEntry(entry manifestEntry, baseDir string) segmentDumpRecord {
	rawEML, err := os.ReadFile(filepath.Join(baseDir, entry.EMLPath))
	if err != nil {
		return segmentDumpRecord{ID: entry.ID, Error: fmt.Sprintf("read eml: %v", err)}
	}
	segs, signals, _ := piguard.Extract(rawEML, 0)
	out := make([]segmentOut, len(segs))
	for i, s := range segs {
		out[i] = segmentOut{Type: string(s.Type), Content: s.Content, Ref: s.Ref}
	}
	return segmentDumpRecord{
		ID:       entry.ID,
		Segments: out,
		Signals: signalsSummary{
			UnicodeTags:      signals.UnicodeTags,
			ZeroWidth:        signals.ZeroWidth,
			HiddenCSSText:    signals.HiddenCSSText,
			HomoglyphRatio:   signals.HomoglyphRatio,
			PlainHTMLDiverge: signals.PlainHTMLDiverge,
			FragmentedURL:    signals.FragmentedURL,
			Truncated:        signals.Truncated,
			Unscannable:      signals.Unscannable,
		},
	}
}
