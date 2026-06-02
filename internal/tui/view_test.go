package tui

import (
	"strings"
	"testing"

	sharedmetrics "github.com/ai-receptionist/shared/metrics"
	"github.com/ai-receptionist/shared/wire"

	"github.com/ai-receptionist/orchestrator/internal/collect"
)

func apply(m model, stage sharedmetrics.Stage, utt wire.UttID) model {
	nm, _ := m.Update(stageMsg{ev: sharedmetrics.MetricEvent{UttID: utt, Stage: stage}})
	return nm.(model)
}

// The live panel must show a clear empty state (so a user who sees nothing knows
// to speak / that no metrics arrived), advance through the pipeline stages as
// events arrive, and surface a heartbeat with the event count.
func TestLivePanelStages(t *testing.T) {
	m := newModel()

	// Empty state: names the steps and tells the user to speak.
	v := m.View()
	for _, want := range []string{"ready", "Listen", "Transcribe", "Think", "Synthesize", "Speak", "no metrics received yet"} {
		if !strings.Contains(v, want) {
			t.Fatalf("empty-state view missing %q\n%s", want, v)
		}
	}

	// Speech detected -> Listening.
	m = apply(m, sharedmetrics.StageSpeechStart, "u-1")
	if v := m.View(); !strings.Contains(v, "Listening") {
		t.Fatalf("after speech_start, expected 'Listening':\n%s", v)
	}

	// Walk the rest of the pipeline.
	m = apply(m, sharedmetrics.StageSTTFinal, "u-1")
	m = apply(m, sharedmetrics.StageLLMDecision, "u-1")
	m = apply(m, sharedmetrics.StageLLMGemini, "u-1")
	m = apply(m, sharedmetrics.StageTTSArrival, "u-1")
	m = apply(m, sharedmetrics.StageTTSPlayed, "u-1")

	v = m.View()
	if !strings.Contains(v, "Speaking") {
		t.Fatalf("after tts.played, expected 'Speaking':\n%s", v)
	}
	if !strings.Contains(v, "✓") {
		t.Fatalf("expected completed-step check marks in the ribbon:\n%s", v)
	}
	if !strings.Contains(v, "events 6") {
		t.Fatalf("expected heartbeat 'events 6':\n%s", v)
	}

	// A new utterance resets the ribbon to its start.
	m = apply(m, sharedmetrics.StageSpeechStart, "u-2")
	if m.liveStep != 0 || m.liveUtt != "u-2" {
		t.Fatalf("new utterance should reset ribbon: step=%d utt=%s", m.liveStep, m.liveUtt)
	}
}

// The detail pane renders the last utterance's per-step durations.
func TestDetailPaneRendersPhases(t *testing.T) {
	// Build a finalized utterance via the joiner.
	var u *collect.Utterance
	j := collect.NewJoiner(func(x *collect.Utterance) { u = x }, 0)
	const id wire.UttID = "d-1"
	for _, s := range []struct {
		st    sharedmetrics.Stage
		ts    int64
		delta float64
	}{
		{sharedmetrics.StageSpeechStart, 1000, 0},
		{sharedmetrics.StageSTTFinal, 1400, 380},
		{sharedmetrics.StageTTSArrival, 1450, 0},
		{sharedmetrics.StageTTSFirstByte, 1700, 240},
		{sharedmetrics.StageTTSPlayed, 1800, 340},
	} {
		j.Add(sharedmetrics.MetricEvent{UttID: id, Stage: s.st, TsMs: s.ts, DeltaMs: s.delta})
	}
	if u == nil {
		t.Fatal("utterance not finalized")
	}

	m := newModel()
	nm, _ := m.Update(utteranceMsg{u: u})
	m = nm.(model)

	v := m.View()
	for _, want := range []string{"Last utterance", "Transcription", "TTS synthesis", "TTS playback", "End-to-end"} {
		if !strings.Contains(v, want) {
			t.Fatalf("detail pane missing %q\n%s", want, v)
		}
	}
}

// The spinner frame index must stay in bounds across many ticks.
func TestSpinnerNoPanic(t *testing.T) {
	m := newModel()
	for i := 0; i < 50; i++ {
		nm, _ := m.Update(spinnerMsg{})
		m = nm.(model)
		_ = m.View()
	}
}
