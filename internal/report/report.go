// Package report builds the post-run session summary — a per-stage latency
// table plus session counts and exit reason — and persists it as report.txt,
// report.json, and the unified raw metrics.jsonl. It runs on both graceful
// shutdown and fail-fast.
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sharedmetrics "github.com/ai-receptionist/shared/metrics"

	"github.com/ai-receptionist/orchestrator/internal/collect"
	"github.com/ai-receptionist/orchestrator/internal/stats"
	"github.com/ai-receptionist/orchestrator/internal/tui"
)

// Session captures the run-level facts shown at the top of the report.
type Session struct {
	StartedMs   int64  `json:"started_ms"`
	EndedMs     int64  `json:"ended_ms"`
	DurationMs  int64  `json:"duration_ms"`
	Mode        string `json:"mode"`         // "live"
	Exit        string `json:"exit"`         // "graceful" | "fail_fast"
	FailedChild string `json:"failed_child,omitempty"`
	FailCause   string `json:"fail_cause,omitempty"`
}

// Report is the full session summary.
type Report struct {
	Session Session                      `json:"session"`
	Stages  map[string]stats.StageStats  `json:"stages"`
	Counts  Counts                       `json:"counts"`
}

// Counts is the breakdown of how utterances ended.
type Counts struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
	NoSpeak   int `json:"no_speak"`
	Errored   int `json:"errored"`
}

// Build assembles a Report from a full-session stats Snapshot and the session facts.
func Build(snap stats.Snapshot, sess Session) Report {
	return Report{
		Session: sess,
		Stages:  snap.Stages,
		Counts: Counts{
			Total:     snap.Total,
			Completed: snap.Completed,
			NoSpeak:   snap.NoSpeak,
			Errored:   snap.Errored,
		},
	}
}

// RenderText returns the human-readable report (no ANSI), suitable for stdout
// scrollback and report.txt.
func (r Report) RenderText() string {
	var b strings.Builder
	fmt.Fprintln(&b, "==================== session report ====================")
	fmt.Fprintf(&b, "mode:     %s\n", r.Session.Mode)
	fmt.Fprintf(&b, "duration: %.1fs\n", float64(r.Session.DurationMs)/1000)
	fmt.Fprintf(&b, "exit:     %s\n", r.Session.Exit)
	if r.Session.Exit == "fail_fast" {
		fmt.Fprintf(&b, "failed:   %s\n", r.Session.FailedChild)
		if r.Session.FailCause != "" {
			fmt.Fprintf(&b, "cause:    %s\n", firstLine(r.Session.FailCause))
		}
	}
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "%-14s %5s %6s %6s %6s %6s %6s\n", "stage", "count", "min", "avg", "p50", "p95", "max")
	for _, d := range collect.DisplayOrder {
		fmt.Fprintln(&b, tui.StageStatsLine(d.Label, r.Stages[d.Key]))
	}
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "utterances: %d total — %d completed, %d no-speak, %d errored\n",
		r.Counts.Total, r.Counts.Completed, r.Counts.NoSpeak, r.Counts.Errored)
	if r.Session.Exit == "fail_fast" && r.Session.FailCause != "" {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "---- failed child stderr ----")
		fmt.Fprintln(&b, r.Session.FailCause)
	}
	fmt.Fprintln(&b, "========================================================")
	return b.String()
}

// WriteFiles persists report.json, report.txt, and the raw metrics.jsonl into
// dir (created if needed). Returns the paths written.
func (r Report) WriteFiles(dir string, raw []sharedmetrics.MetricEvent) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var paths []string

	txtPath := filepath.Join(dir, "report.txt")
	if err := os.WriteFile(txtPath, []byte(r.RenderText()), 0o644); err != nil {
		return paths, err
	}
	paths = append(paths, txtPath)

	jsonPath := filepath.Join(dir, "report.json")
	jb, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return paths, err
	}
	if err := os.WriteFile(jsonPath, append(jb, '\n'), 0o644); err != nil {
		return paths, err
	}
	paths = append(paths, jsonPath)

	rawPath := filepath.Join(dir, "metrics.jsonl")
	var mb strings.Builder
	enc := json.NewEncoder(&mb)
	for _, ev := range raw {
		_ = enc.Encode(ev)
	}
	if err := os.WriteFile(rawPath, []byte(mb.String()), 0o644); err != nil {
		return paths, err
	}
	paths = append(paths, rawPath)

	return paths, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
