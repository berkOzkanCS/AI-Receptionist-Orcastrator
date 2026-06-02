package collect

import (
	"testing"
	"time"

	sharedmetrics "github.com/ai-receptionist/shared/metrics"
	"github.com/ai-receptionist/shared/wire"
)

// ev is a tiny helper to build a MetricEvent for one utterance.
func ev(id wire.UttID, stage sharedmetrics.Stage, tsMs int64, deltaMs float64) sharedmetrics.MetricEvent {
	return sharedmetrics.MetricEvent{UttID: id, Stage: stage, TsMs: tsMs, DeltaMs: deltaMs}
}

// A full utterance flowing through every stage finalizes at tts.played and the
// derived per-stage + end-to-end latencies are computed from the joined wall
// timestamps.
func TestJoinFullUtterance(t *testing.T) {
	var got *Utterance
	j := NewJoiner(func(u *Utterance) { got = u }, 8*time.Second)

	const id wire.UttID = "u-1"
	j.Add(ev(id, sharedmetrics.StageSpeechStart, 1000, 0))
	j.Add(ev(id, sharedmetrics.StageSTTFinal, 1400, 380))
	j.Add(ev(id, sharedmetrics.StageLLMDecision, 1420, 20))
	j.Add(ev(id, sharedmetrics.StageLLMGemini, 1900, 480))
	j.Add(ev(id, sharedmetrics.StageLLMEmit, 1450, 0))
	j.Add(ev(id, sharedmetrics.StageTTSArrival, 1460, 0))
	j.Add(ev(id, sharedmetrics.StageTTSFirstByte, 1700, 240))
	if got != nil {
		t.Fatalf("utterance finalized too early")
	}
	j.Add(ev(id, sharedmetrics.StageTTSPlayed, 1800, 340)) // terminal

	if got == nil {
		t.Fatal("utterance did not finalize on tts.played")
	}
	want := map[string]float64{
		MStt:       400, // 1400-1000
		MDecision:  20,  // 1420-1400
		MGemini:    480, // gemini call delta
		MFirstByte: 240, // 1700-1460
		MPlayed:    340, // 1800-1460
		ME2E:       800, // 1800-1000
	}
	for key, exp := range want {
		v, ok := got.Metric(key)
		if !ok {
			t.Errorf("%s: not computed", key)
			continue
		}
		if v != exp {
			t.Errorf("%s = %.0f, want %.0f", key, v, exp)
		}
	}
	if got.NoSpeak || got.Err != "" {
		t.Errorf("expected a clean completed utterance, got NoSpeak=%v Err=%q", got.NoSpeak, got.Err)
	}
}

// An utterance that reaches STT final but never produces audio is finalized by
// the idle sweep as a no-speak turn, still carrying its STT latency.
func TestJoinNoSpeakSweep(t *testing.T) {
	var got *Utterance
	j := NewJoiner(func(u *Utterance) { got = u }, time.Millisecond)

	const id wire.UttID = "u-2"
	j.Add(ev(id, sharedmetrics.StageSpeechStart, 1000, 0))
	j.Add(ev(id, sharedmetrics.StageSTTFinal, 1400, 380))
	if got != nil {
		t.Fatal("finalized before sweep")
	}
	// lastMs is a tiny epoch value, so it is already far past the timeout.
	j.Sweep()
	if got == nil {
		t.Fatal("no-speak utterance was not swept")
	}
	if !got.NoSpeak {
		t.Errorf("want NoSpeak=true")
	}
	if v, ok := got.Metric(MStt); !ok || v != 400 {
		t.Errorf("MStt = %.0f (ok=%v), want 400", v, ok)
	}
	if _, ok := got.Metric(ME2E); ok {
		t.Errorf("no-speak utterance should have no end-to-end metric")
	}
}

// An error stage finalizes the utterance immediately and marks it errored.
func TestJoinErrorFinalizes(t *testing.T) {
	var got *Utterance
	j := NewJoiner(func(u *Utterance) { got = u }, 8*time.Second)

	const id wire.UttID = "u-3"
	j.Add(ev(id, sharedmetrics.StageSTTFinal, 1400, 380))
	j.Add(sharedmetrics.MetricEvent{UttID: id, Stage: sharedmetrics.StageTTSFinal, TsMs: 1500, Err: "ws closed"})

	if got == nil {
		t.Fatal("error did not finalize the utterance")
	}
	if got.Err != "ws closed" {
		t.Errorf("Err = %q, want %q", got.Err, "ws closed")
	}
}

// Events without a utt_id are ignored (a legacy/standalone producer).
func TestJoinIgnoresEmptyID(t *testing.T) {
	fired := false
	j := NewJoiner(func(*Utterance) { fired = true }, 8*time.Second)
	j.Add(ev("", sharedmetrics.StageTTSPlayed, 1, 0))
	if fired {
		t.Fatal("empty utt_id should be ignored")
	}
}
