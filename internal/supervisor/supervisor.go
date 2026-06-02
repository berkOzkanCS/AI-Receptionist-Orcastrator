// Package supervisor launches the three pipeline children as subprocesses,
// gates each on a readiness probe, and enforces a fail-fast policy: the moment
// any child exits, the other two are stopped and the cause is reported. It owns
// process lifecycle only — it knows nothing about metrics or the TUI.
package supervisor

import (
	"context"
	"fmt"
	"syscall"
	"time"

	"github.com/ai-receptionist/orchestrator/internal/config"
)

// StatusFunc is an optional callback invoked as each child changes state
// ("starting" | "ready" | "dead"), so a dashboard can reflect it live.
type StatusFunc func(name config.ChildName, state string)

// Supervisor launches and supervises the children.
type Supervisor struct {
	cfg      config.Config
	children []*Child
	onStatus StatusFunc

	firstDie chan *Child // buffered(1): the first child to exit wins
}

// New builds a Supervisor. onStatus may be nil.
func New(cfg config.Config, onStatus StatusFunc) *Supervisor {
	if onStatus == nil {
		onStatus = func(config.ChildName, string) {}
	}
	s := &Supervisor{cfg: cfg, onStatus: onStatus, firstDie: make(chan *Child, 1)}
	for _, spec := range cfg.Children() {
		s.children = append(s.children, newChild(spec))
	}
	return s
}

// Start launches the children in order, gating each on its readiness probe. On
// the first failure it stops whatever already started and returns the error.
// Each child also gets an exit-watcher that feeds the fail-fast channel.
func (s *Supervisor) Start(ctx context.Context) error {
	for _, c := range s.children {
		s.onStatus(c.spec.Name, "starting")
		if err := c.start(); err != nil {
			s.Shutdown()
			return fmt.Errorf("start %s: %w", c.spec.Name, err)
		}
		// Watch for this child's exit and report the first death.
		go func(c *Child) {
			<-c.exited
			select {
			case s.firstDie <- c:
			default:
			}
		}(c)

		if err := waitReady(ctx, c, s.cfg.ReadyTimeout); err != nil {
			s.onStatus(c.spec.Name, "dead")
			s.Shutdown()
			return err
		}
		s.onStatus(c.spec.Name, "ready")
	}
	return nil
}

// Wait blocks until ctx is cancelled (graceful) or a child exits (fail-fast).
// It returns the dead child and a descriptive cause on fail-fast, or (nil, nil)
// on graceful shutdown.
func (s *Supervisor) Wait(ctx context.Context) (died *Child, cause error) {
	select {
	case <-ctx.Done():
		return nil, nil
	case c := <-s.firstDie:
		s.onStatus(c.spec.Name, "dead")
		return c, fmt.Errorf("child %s exited: %v\nlast stderr:\n%s", c.spec.Name, c.waitErr, c.stderr.Tail())
	}
}

// Shutdown stops every still-running child in reverse start order
// (tts -> llm -> stt) so consumers stop before their producers. Idempotent.
func (s *Supervisor) Shutdown() {
	for i := len(s.children) - 1; i >= 0; i-- {
		c := s.children[i]
		if !c.running() {
			continue
		}
		c.signal(syscall.SIGTERM)
		select {
		case <-c.exited:
		case <-time.After(s.cfg.StopGrace):
			c.signal(syscall.SIGKILL)
			<-c.exited
		}
	}
}

// DeadChildName returns the stage name for a Child (used by callers building a
// report after fail-fast).
func DeadChildName(c *Child) config.ChildName {
	if c == nil {
		return ""
	}
	return c.spec.Name
}
