package metrics

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
)

const (
	scanBufInit  = 64 * 1024 // initial scanner buffer
	scanBufMax   = 1 << 20   // max metrics line (1 MiB)
	eventBufInit = 1024      // decoded events buffered toward the consumer
)

// Listener is the orchestrator-side server for the metrics socket. It owns the
// socket file, accepts any number of child connections concurrently, decodes
// one MetricEvent per line, and delivers them on Events(). A child that floods
// is bounded only by the event buffer; the orchestrator is expected to drain
// fast, so sends block (with a ctx escape) rather than drop, to avoid losing
// timing data.
type Listener struct {
	path   string
	ln     net.Listener
	events chan MetricEvent
}

// NewListener returns a Listener bound to path (DefaultPath if empty).
func NewListener(path string) *Listener {
	if path == "" {
		path = DefaultPath
	}
	return &Listener{
		path:   path,
		events: make(chan MetricEvent, eventBufInit),
	}
}

// Events is the decoded MetricEvent stream.
func (l *Listener) Events() <-chan MetricEvent { return l.events }

// Start removes any stale socket, listens, and accepts in the background until
// ctx is cancelled. Must be called (and return nil) before any child is
// launched, so no child's first emit is refused.
func (l *Listener) Start(ctx context.Context) error {
	_ = os.Remove(l.path)
	ln, err := net.Listen(network, l.path)
	if err != nil {
		return err
	}
	l.ln = ln
	go l.acceptLoop(ctx)
	return nil
}

func (l *Listener) acceptLoop(ctx context.Context) {
	go func() {
		<-ctx.Done()
		_ = l.ln.Close()
	}()
	for {
		conn, err := l.ln.Accept()
		if err != nil {
			return // listener closed (ctx done) or fatal accept error
		}
		go l.serve(ctx, conn)
	}
}

func (l *Listener) serve(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, scanBufInit), scanBufMax)
	for sc.Scan() {
		var m MetricEvent
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue // skip a malformed line, keep reading
		}
		select {
		case l.events <- m:
		case <-ctx.Done():
			return
		}
	}
}

// Close stops accepting and removes the socket file.
func (l *Listener) Close() error {
	if l.ln != nil {
		_ = l.ln.Close()
	}
	_ = os.Remove(l.path)
	return nil
}
