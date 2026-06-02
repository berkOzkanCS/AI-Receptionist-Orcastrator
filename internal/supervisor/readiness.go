package supervisor

import (
	"context"
	"fmt"
	"net"
	"time"
)

const dialProbeInterval = 50 * time.Millisecond

// waitReady blocks until the child is considered ready or ctx/timeout fires.
// If the spec names a readiness socket, readiness = that socket becomes
// dialable (the child's server is up). Otherwise (a pure consumer like TTS),
// readiness = the process is still alive after a short settle, i.e. it didn't
// immediately exit on a missing .env.
func waitReady(ctx context.Context, c *Child, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	probeCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	if c.spec.ReadySock == "" {
		return settleReady(probeCtx, c)
	}

	tick := time.NewTicker(dialProbeInterval)
	defer tick.Stop()
	for {
		select {
		case <-c.exited:
			return fmt.Errorf("%s exited during startup: %v\n%s", c.spec.Name, c.waitErr, c.stderr.Tail())
		case <-probeCtx.Done():
			return fmt.Errorf("%s not ready after %s (socket %s never came up)\n%s",
				c.spec.Name, timeout, c.spec.ReadySock, c.stderr.Tail())
		case <-tick.C:
			conn, err := net.Dial("unix", c.spec.ReadySock)
			if err == nil {
				_ = conn.Close()
				return nil
			}
		}
	}
}

// settleReady waits a short while; if the process is still running it's ready,
// otherwise it died on startup and we surface the stderr tail.
func settleReady(ctx context.Context, c *Child) error {
	const settle = 800 * time.Millisecond
	select {
	case <-c.exited:
		return fmt.Errorf("%s exited during startup: %v\n%s", c.spec.Name, c.waitErr, c.stderr.Tail())
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(settle):
		if !c.running() {
			return fmt.Errorf("%s exited during startup: %v\n%s", c.spec.Name, c.waitErr, c.stderr.Tail())
		}
		return nil
	}
}
