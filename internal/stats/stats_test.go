package stats

import (
	"testing"

	sharedmetrics "github.com/ai-receptionist/shared/metrics"
	"github.com/ai-receptionist/shared/wire"

	"github.com/ai-receptionist/orchestrator/internal/collect"
)

// completed builds a finalized utterance whose end-to-end latency is exactly
// e2eMs, by feeding it through a Joiner with realistic (non-zero) timestamps.
func completed(id wire.UttID, e2eMs int64) *collect.Utterance {
	const base int64 = 1_000_000 // a non-zero wall-clock-ish anchor
	var got *collect.Utterance
	j := collect.NewJoiner(func(u *collect.Utterance) { got = u }, 0)
	j.Add(sharedmetrics.MetricEvent{UttID: id, Stage: sharedmetrics.StageSpeechStart, TsMs: base})
	j.Add(sharedmetrics.MetricEvent{UttID: id, Stage: sharedmetrics.StageSTTFinal, TsMs: base + 100})
	j.Add(sharedmetrics.MetricEvent{UttID: id, Stage: sharedmetrics.StageTTSArrival, TsMs: base + 100})
	j.Add(sharedmetrics.MetricEvent{UttID: id, Stage: sharedmetrics.StageTTSPlayed, TsMs: base + e2eMs})
	return got
}

func TestAggregatePercentiles(t *testing.T) {
	a := New()
	// end-to-end values: 100,200,...,1000 (10 samples)
	for i := int64(1); i <= 10; i++ {
		a.Observe(completed(wire.UttID("u"+string(rune('0'+i))), i*100))
	}
	snap := a.Snapshot(0)
	if snap.Total != 10 || snap.Completed != 10 {
		t.Fatalf("counts: total=%d completed=%d", snap.Total, snap.Completed)
	}
	e2e := snap.Stages[collect.ME2E]
	if e2e.Count != 10 {
		t.Fatalf("e2e count = %d, want 10", e2e.Count)
	}
	if e2e.Min != 100 || e2e.Max != 1000 {
		t.Errorf("min/max = %.0f/%.0f, want 100/1000", e2e.Min, e2e.Max)
	}
	if e2e.Avg != 550 {
		t.Errorf("avg = %.0f, want 550", e2e.Avg)
	}
	// p50 over [100..1000] with linear interpolation = 550.
	if e2e.P50 != 550 {
		t.Errorf("p50 = %.0f, want 550", e2e.P50)
	}
	// p95 = interpolate at rank 0.95*9 = 8.55 between 900 and 1000 = 955.
	if e2e.P95 < 950 || e2e.P95 > 960 {
		t.Errorf("p95 = %.1f, want ~955", e2e.P95)
	}
}

func TestSnapshotWindow(t *testing.T) {
	a := New()
	for i := int64(1); i <= 5; i++ {
		a.Observe(completed(wire.UttID("w"+string(rune('0'+i))), i*100))
	}
	// Window of 2 keeps only the last two samples (400, 500).
	snap := a.Snapshot(2)
	e2e := snap.Stages[collect.ME2E]
	if e2e.Count != 2 || e2e.Min != 400 || e2e.Max != 500 {
		t.Errorf("windowed e2e = %+v, want count2 min400 max500", e2e)
	}
}
