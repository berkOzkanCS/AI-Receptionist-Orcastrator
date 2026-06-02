package supervisor

import (
	"context"
	"testing"
	"time"

	"github.com/ai-receptionist/orchestrator/internal/config"
)

// newTest builds a Supervisor with explicit dummy child specs (bypassing
// cfg.Children()) so the launch/fail-fast/shutdown machinery can be exercised
// without the real pipeline.
func newTest(specs []config.ChildSpec) *Supervisor {
	s := &Supervisor{
		cfg:      config.Config{StopGrace: time.Second, ReadyTimeout: 5 * time.Second},
		onStatus: func(config.ChildName, string) {},
		firstDie: make(chan *Child, 1),
	}
	for _, sp := range specs {
		s.children = append(s.children, newChild(sp))
	}
	return s
}

// A child that stays up past the readiness settle, then exits, must trip the
// fail-fast path; Shutdown must then stop the surviving child.
func TestSupervisorFailFast(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := newTest([]config.ChildSpec{
		{Name: "longlived", Args: []string{"sleep", "30"}},                    // no ReadySock => settle-ready
		{Name: "dying", Args: []string{"sh", "-c", "sleep 1.5; exit 3"}},
	})

	if err := s.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	died, cause := s.Wait(ctx)
	if died == nil {
		t.Fatal("expected a fail-fast child death, got graceful")
	}
	if got := DeadChildName(died); got != "dying" {
		t.Fatalf("dead child = %q, want dying", got)
	}
	if cause == nil {
		t.Fatal("expected a non-nil cause")
	}

	s.Shutdown()
	if s.children[0].running() {
		t.Fatal("Shutdown did not stop the surviving child")
	}
}

// A child whose process exits during startup must make Start fail (and clean up).
func TestSupervisorStartupFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := newTest([]config.ChildSpec{
		{Name: "instant", Args: []string{"sh", "-c", "exit 1"}}, // dies immediately, before settle
	})

	if err := s.Start(ctx); err == nil {
		t.Fatal("expected Start to fail when a child exits during startup")
	}
}

// Env propagation: the child must see its injected env vars.
func TestChildEnvPropagation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newChild(config.ChildSpec{
		Name: "envcheck",
		Args: []string{"sh", "-c", "echo MARK=$ORCH_HEADLESS; sleep 30"},
		Env:  []string{"ORCH_HEADLESS=1"},
	})
	if err := c.start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { c.signal(15); <-c.exited }()

	// Give the echo a moment to land in the captured stdout/stderr ring buffer.
	time.Sleep(200 * time.Millisecond)
	if tail := c.stderr.Tail(); tail == "" || !contains(tail, "MARK=1") {
		t.Fatalf("expected child to see ORCH_HEADLESS=1, captured: %q", tail)
	}
	_ = ctx
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
