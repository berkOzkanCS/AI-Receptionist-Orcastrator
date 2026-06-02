// Package collect owns the orchestrator's metrics join: it consumes the
// MetricEvent stream from the shared metrics Listener and stitches the per-stage
// timings of each utterance (keyed by utt_id) back into one Utterance, from
// which true end-to-end and per-stage latencies are derived. It depends on
// nothing else in the orchestrator — finalized utterances are handed out via a
// callback so the stats aggregator, TUI, and report can each react.
package collect

import (
	"context"
	"sync"
	"time"

	sharedmetrics "github.com/ai-receptionist/shared/metrics"
	"github.com/ai-receptionist/shared/wire"
)

// Derived display-metric keys. Each maps an utterance's joined stage timestamps
// to a single latency number for the dashboard and report. The "from speech
// start" metrics (transcribe, categorize, decide) are elapsed-since-first-word
// so they're directly comparable; gemini and the tts.* ones are stage durations.
const (
	MStt        = "stt_final"      // speech-start -> STT final transcript (transcription time)
	MCategorize = "categorize"     // speech-start -> first categorizer hit (how fast intent was known)
	MDecision   = "llm_decision"   // speech-start -> decision emitted (filler/answer chosen)
	MGemini     = "llm_gemini"     // Gemini call duration
	MFirstByte  = "tts_first_byte" // TTS command arrival -> first PCM byte
	MPlayed     = "tts_played"     // TTS command arrival -> first audio audible
	ME2E        = "end_to_end"     // speech-start -> first audio audible
)

// DisplayOrder is the canonical ordering + labels for the metrics, shared by the
// dashboard and the report so both read the same.
var DisplayOrder = []struct{ Key, Label string }{
	{MStt, "Transcribe"},
	{MCategorize, "Categorize"},
	{MDecision, "Decide"},
	{MGemini, "LLM (Gemini)"},
	{MFirstByte, "TTS 1st byte"},
	{MPlayed, "TTS played"},
	{ME2E, "END-TO-END"},
}

type stageRec struct {
	TsMs    int64
	DeltaMs float64
	Err     string
}

// Utterance is the joined per-utterance view assembled from MetricEvents.
type Utterance struct {
	UttID    wire.UttID
	Stages   map[sharedmetrics.Stage]stageRec
	Category string
	Kind     string
	Text     string
	Err      string // first stage error seen, if any
	NoSpeak  bool   // finalized by timeout without ever reaching tts.played

	// Response-path tracking: which kinds of line were spoken, whether Gemini
	// was called, and how the intent was categorized. Drives the Path() label
	// and the per-path counts in the report.
	emitKinds    map[string]bool // "filler" | "answer" | "llm" that were emitted to TTS
	geminiCalled bool            // a Gemini verify/answer call was dispatched
	geminiKind   string          // "verify" (checked a catalog answer) | "llm" (generated a reply)
	catSource    string          // "regex" | "embedding" | "" (uncategorized)

	firstMs int64
	lastMs  int64
}

func (u *Utterance) tsOf(s sharedmetrics.Stage) (int64, bool) {
	r, ok := u.Stages[s]
	if !ok || r.TsMs == 0 {
		return 0, false
	}
	return r.TsMs, true
}

func (u *Utterance) deltaOf(s sharedmetrics.Stage) (float64, bool) {
	r, ok := u.Stages[s]
	if !ok || r.DeltaMs == 0 {
		return 0, false
	}
	return r.DeltaMs, true
}

func (u *Utterance) gap(a, b sharedmetrics.Stage) (float64, bool) {
	ta, oka := u.tsOf(a)
	tb, okb := u.tsOf(b)
	if !oka || !okb || tb < ta {
		return 0, false
	}
	return float64(tb - ta), true
}

// Metric returns the derived latency in milliseconds for a display key, and
// whether the utterance had enough joined stages to compute it.
func (u *Utterance) Metric(key string) (float64, bool) {
	switch key {
	case MStt:
		if v, ok := u.gap(sharedmetrics.StageSpeechStart, sharedmetrics.StageSTTFinal); ok {
			return v, true
		}
		return u.deltaOf(sharedmetrics.StageSTTFinal)
	case MCategorize:
		// Earliest categorizer hit, measured as elapsed-from-first-word.
		r, okR := u.deltaOf(sharedmetrics.StageLLMRegexHit)
		e, okE := u.deltaOf(sharedmetrics.StageLLMEmbeddingHit)
		switch {
		case okR && okE:
			if e < r {
				return e, true
			}
			return r, true
		case okR:
			return r, true
		case okE:
			return e, true
		}
		return 0, false
	case MDecision:
		// Elapsed-from-first-word to the decision (filler/answer chosen). This is
		// the engine's own elapsed clock, always positive — the decision can fire
		// before the final transcript, so a wall gap from stt.final would be empty.
		return u.deltaOf(sharedmetrics.StageLLMDecision)
	case MGemini:
		return u.deltaOf(sharedmetrics.StageLLMGemini)
	case MFirstByte:
		if v, ok := u.gap(sharedmetrics.StageTTSArrival, sharedmetrics.StageTTSFirstByte); ok {
			return v, true
		}
		return u.deltaOf(sharedmetrics.StageTTSFirstByte)
	case MPlayed:
		if v, ok := u.gap(sharedmetrics.StageTTSArrival, sharedmetrics.StageTTSPlayed); ok {
			return v, true
		}
		return u.deltaOf(sharedmetrics.StageTTSPlayed)
	case ME2E:
		if v, ok := u.gap(sharedmetrics.StageSpeechStart, sharedmetrics.StageTTSPlayed); ok {
			return v, true
		}
		return u.gap(sharedmetrics.StageSTTFinal, sharedmetrics.StageTTSPlayed)
	}
	return 0, false
}

// UsedFiller reports whether a time-buying filler line was spoken.
func (u *Utterance) UsedFiller() bool { return u.emitKinds[wire.KindFiller] }

// UsedCatalog reports whether a predetermined catalog answer was spoken.
func (u *Utterance) UsedCatalog() bool { return u.emitKinds[wire.KindAnswer] }

// UsedLLM reports whether a Gemini-generated reply was spoken.
func (u *Utterance) UsedLLM() bool { return u.emitKinds[wire.KindLLM] }

// GeminiCalled reports whether a Gemini verify/answer call was dispatched.
func (u *Utterance) GeminiCalled() bool { return u.geminiCalled }

// Path is a short label for how this utterance was answered, for the dashboard
// and report: which line(s) were spoken and whether Gemini was involved.
func (u *Utterance) Path() string {
	switch {
	case u.emitKinds[wire.KindLLM]:
		if u.emitKinds[wire.KindFiller] {
			return "filler→llm"
		}
		return "llm"
	case u.emitKinds[wire.KindAnswer]:
		if u.geminiKind == "verify" {
			return "catalog✓" // predetermined line, Gemini-verified
		}
		return "catalog"
	case u.emitKinds[wire.KindFiller]:
		return "filler"
	case u.Err != "":
		return "error"
	case u.NoSpeak:
		return "no-speak"
	}
	return "—"
}

// Phase is one pipeline step and how long it took (a duration, in ms), with a
// plain-language note of what it measures. This is the simple "where did the
// time go" view — each phase is an independently-measured duration, not a
// cumulative offset.
type Phase struct {
	Name   string
	TookMs float64
	OK     bool // false => this step didn't happen / wasn't measured
	Note   string
}

// Phases returns the per-step durations for the utterance: how long each part of
// the pipeline took. They are measured independently (STT, the LLM call, TTS run
// partly in parallel with categorization and any early filler), so they do not
// necessarily sum to the end-to-end total — each answers "how long did THIS step
// take".
func (u *Utterance) Phases() []Phase {
	var ps []Phase

	if d, ok := u.deltaOf(sharedmetrics.StageSTTFinal); ok {
		ps = append(ps, Phase{"Transcription", d, true, "finished speaking → text ready"})
	}
	if d, ok := u.Metric(MCategorize); ok {
		ps = append(ps, Phase{"Categorization", d, true, "started speaking → intent known"})
	}
	if u.geminiCalled {
		d, ok := u.deltaOf(sharedmetrics.StageLLMGemini)
		note := "the LLM request"
		if u.geminiKind == "verify" {
			note = "background check, didn't delay the reply"
		}
		ps = append(ps, Phase{"LLM (Gemini) call", d, ok, note})
	}
	fb, okFb := u.deltaOf(sharedmetrics.StageTTSFirstByte)
	if okFb {
		ps = append(ps, Phase{"TTS synthesis", fb, true, "text sent → first audio byte"})
	}
	if pl, okPl := u.deltaOf(sharedmetrics.StageTTSPlayed); okPl {
		if okFb && pl >= fb {
			ps = append(ps, Phase{"TTS playback", pl - fb, true, "first byte → you hear it"})
		} else {
			ps = append(ps, Phase{"TTS playback", pl, true, "text sent → you hear it"})
		}
	}
	return ps
}

// Timing is the JSON-serializable per-utterance record written live to the
// timings file the moment an utterance finishes, so the data is never lost when
// the on-screen view moves to the next phrase.
type Timing struct {
	UttID      string             `json:"utt_id"`
	Category   string             `json:"category,omitempty"`
	Path       string             `json:"path"`
	StepsMs    map[string]float64 `json:"steps_ms"` // step name -> how long it took
	EndToEndMs float64            `json:"end_to_end_ms,omitempty"`
	NoSpeak    bool               `json:"no_speak,omitempty"`
	Err        string             `json:"err,omitempty"`
}

// Timing builds the serializable record for this utterance.
func (u *Utterance) Timing() Timing {
	steps := map[string]float64{}
	for _, p := range u.Phases() {
		if p.OK {
			steps[p.Name] = p.TookMs
		}
	}
	t := Timing{
		UttID:    string(u.UttID),
		Category: u.Category,
		Path:     u.Path(),
		StepsMs:  steps,
		NoSpeak:  u.NoSpeak,
		Err:      u.Err,
	}
	if v, ok := u.Metric(ME2E); ok {
		t.EndToEndMs = v
	}
	return t
}

// Joiner accumulates MetricEvents into open utterances and finalizes them.
type Joiner struct {
	mu      sync.Mutex
	open    map[wire.UttID]*Utterance
	onFinal func(*Utterance)
	timeout time.Duration
}

// NewJoiner returns a Joiner. onFinal fires exactly once per utterance, when it
// reaches tts.played / errors / is dropped, or when it goes idle past timeout
// (a no-speak turn). A zero timeout defaults to 8s.
func NewJoiner(onFinal func(*Utterance), timeout time.Duration) *Joiner {
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	return &Joiner{open: map[wire.UttID]*Utterance{}, onFinal: onFinal, timeout: timeout}
}

// Add folds one MetricEvent into its utterance, finalizing if terminal.
func (j *Joiner) Add(ev sharedmetrics.MetricEvent) {
	if ev.UttID == "" {
		return
	}
	j.mu.Lock()
	u := j.open[ev.UttID]
	if u == nil {
		u = &Utterance{
			UttID:     ev.UttID,
			Stages:    map[sharedmetrics.Stage]stageRec{},
			emitKinds: map[string]bool{},
			firstMs:   ev.TsMs,
		}
		j.open[ev.UttID] = u
	}
	// Keep the earliest timestamp per stage (time-to-first semantics: a filler
	// plays before the answer; first audio is what the caller perceives).
	if prev, ok := u.Stages[ev.Stage]; !ok || (ev.TsMs > 0 && ev.TsMs < prev.TsMs) {
		u.Stages[ev.Stage] = stageRec{TsMs: ev.TsMs, DeltaMs: ev.DeltaMs, Err: ev.Err}
	}
	if ev.Category != "" && u.Category == "" {
		u.Category = ev.Category
	}
	if ev.Kind != "" && u.Kind == "" {
		u.Kind = ev.Kind
	}
	if ev.Text != "" {
		u.Text = ev.Text
	}
	if ev.Err != "" && u.Err == "" {
		u.Err = ev.Err
	}
	// Track the response path: how the intent was categorized, whether Gemini
	// was called, and which kinds of line were actually spoken.
	switch ev.Stage {
	case sharedmetrics.StageLLMRegexHit:
		if u.catSource == "" {
			u.catSource = "regex"
		}
	case sharedmetrics.StageLLMEmbeddingHit:
		if u.catSource == "" {
			u.catSource = "embedding"
		}
	case sharedmetrics.StageLLMGeminiStart:
		u.geminiCalled = true
	case sharedmetrics.StageLLMGemini:
		u.geminiCalled = true
		if ev.Kind != "" {
			u.geminiKind = ev.Kind
		}
	case sharedmetrics.StageLLMEmit:
		if ev.Kind != "" {
			u.emitKinds[ev.Kind] = true
		}
	}
	if ev.TsMs > u.lastMs {
		u.lastMs = ev.TsMs
	}
	if u.firstMs == 0 || (ev.TsMs > 0 && ev.TsMs < u.firstMs) {
		u.firstMs = ev.TsMs
	}

	if j.terminal(u) {
		delete(j.open, ev.UttID)
		j.mu.Unlock()
		j.onFinal(u)
		return
	}
	j.mu.Unlock()
}

func (j *Joiner) terminal(u *Utterance) bool {
	if u.Err != "" {
		return true
	}
	if _, ok := u.Stages[sharedmetrics.StageTTSPlayed]; ok {
		return true
	}
	if _, ok := u.Stages[sharedmetrics.StageTTSDropped]; ok {
		return true
	}
	return false
}

// Sweep finalizes utterances that have gone idle past the timeout as no-speak
// turns (e.g. STT final but the LLM chose to stay silent). Call ~1Hz.
func (j *Joiner) Sweep() {
	now := time.Now().UnixMilli()
	var ready []*Utterance
	j.mu.Lock()
	for id, u := range j.open {
		if u.lastMs > 0 && now-u.lastMs > j.timeout.Milliseconds() {
			u.NoSpeak = true
			ready = append(ready, u)
			delete(j.open, id)
		}
	}
	j.mu.Unlock()
	for _, u := range ready {
		j.onFinal(u)
	}
}

// Collector drives the Joiner from the Listener's event stream, plus a 1Hz
// sweep for idle utterances. It never blocks a child: it only reads events and
// updates the in-memory join. onRaw, if set, sees every decoded event (used to
// persist the unified metrics.jsonl for the report).
type Collector struct {
	events <-chan sharedmetrics.MetricEvent
	joiner *Joiner
	onRaw  func(sharedmetrics.MetricEvent)
}

// NewCollector wires the event stream to the joiner. onRaw may be nil.
func NewCollector(events <-chan sharedmetrics.MetricEvent, joiner *Joiner, onRaw func(sharedmetrics.MetricEvent)) *Collector {
	return &Collector{events: events, joiner: joiner, onRaw: onRaw}
}

// Run consumes events and sweeps until ctx is cancelled.
func (c *Collector) Run(ctx context.Context) {
	sweep := time.NewTicker(1 * time.Second)
	defer sweep.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-c.events:
			if !ok {
				return
			}
			if c.onRaw != nil {
				c.onRaw(ev)
			}
			c.joiner.Add(ev)
		case <-sweep.C:
			c.joiner.Sweep()
		}
	}
}
