// Package stats turns finalized utterances into rolling per-stage distributions
// (min/avg/p50/p95/max) plus session counts. The live dashboard reads a
// windowed Snapshot (recency-weighted); the post-run report reads a Snapshot
// over the whole session.
package stats

import (
	"math"
	"sort"
	"sync"

	"github.com/ai-receptionist/orchestrator/internal/collect"
)

// LiveWindow is how many recent samples the dashboard's percentiles consider.
const LiveWindow = 200

// StageStats is a summary of one metric's distribution.
type StageStats struct {
	Count int     `json:"count"`
	Min   float64 `json:"min"`
	Avg   float64 `json:"avg"`
	P50   float64 `json:"p50"`
	P95   float64 `json:"p95"`
	Max   float64 `json:"max"`
}

// PathCounts tallies which response path each utterance took. The flags are not
// mutually exclusive (an utterance can play a filler and then an LLM reply), so
// these are independent counts.
type PathCounts struct {
	Filler        int `json:"filler"`        // a time-buying filler was spoken
	Catalog       int `json:"catalog"`       // a predetermined catalog answer was spoken
	LLM           int `json:"llm"`           // a Gemini-generated reply was spoken
	Gemini        int `json:"gemini"`        // a Gemini call was made (verify or answer)
	Uncategorized int `json:"uncategorized"` // a spoken utterance neither regex nor embedding categorized (miss)
}

// Snapshot is a point-in-time view of every metric plus session counts.
type Snapshot struct {
	Stages    map[string]StageStats `json:"stages"`
	Total     int                   `json:"total"`
	Completed int                   `json:"completed"` // reached tts.played
	NoSpeak   int                   `json:"no_speak"`  // STT final but no audio produced
	Errored   int                   `json:"errored"`
	Paths     PathCounts            `json:"paths"`
}

// Aggregator accumulates samples per derived metric key.
type Aggregator struct {
	mu        sync.Mutex
	series    map[string][]float64
	total     int
	completed int
	noSpeak   int
	errored   int
	paths     PathCounts
}

// New returns an empty Aggregator.
func New() *Aggregator {
	return &Aggregator{series: map[string][]float64{}}
}

// Observe folds a finalized utterance into the distributions. Safe for
// concurrent use (called from the collector's finalize callback).
func (a *Aggregator) Observe(u *collect.Utterance) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.total++
	switch {
	case u.Err != "":
		a.errored++
	case u.NoSpeak:
		a.noSpeak++
	default:
		a.completed++
	}
	if u.UsedFiller() {
		a.paths.Filler++
	}
	if u.UsedCatalog() {
		a.paths.Catalog++
	}
	if u.UsedLLM() {
		a.paths.LLM++
	}
	if u.GeminiCalled() {
		a.paths.Gemini++
	}
	// A miss = a real spoken utterance that neither categorizer committed
	// (it fell through to the LLM). Skip no-speak/errored turns.
	if u.Err == "" && !u.NoSpeak && u.CatSource() == "miss" {
		a.paths.Uncategorized++
	}
	for _, d := range collect.DisplayOrder {
		if v, ok := u.Metric(d.Key); ok {
			a.series[d.Key] = append(a.series[d.Key], v)
		}
	}
}

// Snapshot computes a summary. window<=0 uses all samples (the report); a
// positive window uses only the most recent N (the live dashboard).
func (a *Aggregator) Snapshot(window int) Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := Snapshot{
		Stages:    map[string]StageStats{},
		Total:     a.total,
		Completed: a.completed,
		NoSpeak:   a.noSpeak,
		Errored:   a.errored,
		Paths:     a.paths,
	}
	for _, d := range collect.DisplayOrder {
		samples := a.series[d.Key]
		if window > 0 && len(samples) > window {
			samples = samples[len(samples)-window:]
		}
		s.Stages[d.Key] = summarize(samples)
	}
	return s
}

func summarize(samples []float64) StageStats {
	n := len(samples)
	if n == 0 {
		return StageStats{}
	}
	sorted := make([]float64, n)
	copy(sorted, samples)
	sort.Float64s(sorted)
	var sum float64
	for _, v := range sorted {
		sum += v
	}
	return StageStats{
		Count: n,
		Min:   sorted[0],
		Avg:   sum / float64(n),
		P50:   percentile(sorted, 50),
		P95:   percentile(sorted, 95),
		Max:   sorted[n-1],
	}
}

// percentile does linear interpolation on a pre-sorted slice.
func percentile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	rank := p / 100 * float64(n-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if lo == hi {
		return sorted[lo]
	}
	frac := rank - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}
