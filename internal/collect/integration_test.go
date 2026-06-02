package collect

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	sharedmetrics "github.com/ai-receptionist/shared/metrics"
	"github.com/ai-receptionist/shared/wire"
)

// TestEmitterListenerRoundTrip exercises the real transport the live run uses:
// a child-side Emitter dials the orchestrator-side Listener over a Unix socket,
// the Collector joins the events, and a complete utterance is finalized with the
// expected end-to-end latency. This is the integration the dashboard depends on.
func TestEmitterListenerRoundTrip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Short socket path (macOS caps unix socket paths ~104 chars).
	path := fmt.Sprintf("/tmp/orch-it-%d.sock", os.Getpid())
	defer os.Remove(path)

	ln := sharedmetrics.NewListener(path)
	if err := ln.Start(ctx); err != nil {
		t.Fatalf("listener: %v", err)
	}
	defer ln.Close()

	done := make(chan *Utterance, 1)
	joiner := NewJoiner(func(u *Utterance) { done <- u }, 8*time.Second)
	go NewCollector(ln.Events(), joiner, nil).Run(ctx)

	em := sharedmetrics.NewEmitter("test", path)
	go em.Run(ctx)

	const id wire.UttID = "rt-1"
	stages := []struct {
		s  sharedmetrics.Stage
		ts int64
	}{
		{sharedmetrics.StageSpeechStart, 1000},
		{sharedmetrics.StageSTTFinal, 1400},
		{sharedmetrics.StageTTSArrival, 1450},
		{sharedmetrics.StageTTSFirstByte, 1700},
		{sharedmetrics.StageTTSPlayed, 1850},
	}
	for _, st := range stages {
		em.Emit(sharedmetrics.MetricEvent{UttID: id, Stage: st.s, TsMs: st.ts})
	}

	select {
	case u := <-done:
		if v, ok := u.Metric(ME2E); !ok || v != 850 {
			t.Fatalf("end-to-end = %.0f (ok=%v), want 850", v, ok)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("utterance never finalized over the socket — transport broken")
	}
}
