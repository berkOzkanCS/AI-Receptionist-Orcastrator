package supervisor

import (
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/ai-receptionist/orchestrator/internal/config"
)

// ringBuffer keeps the last N lines of a child's stderr so a fail-fast report
// can show why it died, without holding the whole log in memory.
type ringBuffer struct {
	mu    sync.Mutex
	lines []string
	max   int
	buf   []byte // carry for partial lines across Write calls
}

func newRingBuffer(max int) *ringBuffer { return &ringBuffer{max: max} }

// Write implements io.Writer; it splits on newlines and keeps the last max lines.
func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	for {
		i := indexByte(r.buf, '\n')
		if i < 0 {
			break
		}
		line := string(r.buf[:i])
		r.buf = r.buf[i+1:]
		r.lines = append(r.lines, line)
		if len(r.lines) > r.max {
			r.lines = r.lines[len(r.lines)-r.max:]
		}
	}
	return len(p), nil
}

// Tail returns the buffered lines joined by newlines (plus any partial line).
func (r *ringBuffer) Tail() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := ""
	for _, l := range r.lines {
		out += l + "\n"
	}
	if len(r.buf) > 0 {
		out += string(r.buf)
	}
	return out
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// Child is one supervised process plus the bookkeeping needed to detect its
// exit and stop it cleanly.
type Child struct {
	spec    config.ChildSpec
	cmd     *exec.Cmd
	stderr  *ringBuffer
	exited  chan struct{} // closed when Wait returns
	waitErr error         // set before exited closes
}

func newChild(spec config.ChildSpec) *Child {
	return &Child{spec: spec, stderr: newRingBuffer(40), exited: make(chan struct{})}
}

// start launches the process in its own process group (so the whole group —
// `go run`'s compiled binary, STT's Python sidecar — can be signalled together)
// and begins watching for its exit.
func (c *Child) start() error {
	cmd := exec.Command(c.spec.Args[0], c.spec.Args[1:]...)
	cmd.Dir = c.spec.Dir
	cmd.Env = append(os.Environ(), c.spec.Env...)
	cmd.Stdout = c.stderr // child stdout is suppressed in headless mode; capture anything that leaks
	cmd.Stderr = c.stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return err
	}
	c.cmd = cmd

	go func() {
		c.waitErr = cmd.Wait()
		close(c.exited)
	}()
	return nil
}

// running reports whether the process has not yet exited.
func (c *Child) running() bool {
	select {
	case <-c.exited:
		return false
	default:
		return c.cmd != nil
	}
}

// stop signals the process group: SIGTERM, wait up to grace via the caller, then
// the caller escalates with kill(). Signalling the negative pgid hits the whole
// group, reaping `go run`'s grandchild and any sidecars.
func (c *Child) signal(sig syscall.Signal) {
	if c.cmd == nil || c.cmd.Process == nil {
		return
	}
	pgid, err := syscall.Getpgid(c.cmd.Process.Pid)
	if err == nil {
		_ = syscall.Kill(-pgid, sig)
		return
	}
	_ = c.cmd.Process.Signal(sig)
}
