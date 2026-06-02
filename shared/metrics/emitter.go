package metrics

import (
	"context"
	"encoding/json"
	"net"
	"sync/atomic"
	"time"

	"github.com/ai-receptionist/shared/wire"
)

// Emitter tunables.
const (
	emitBuf      = 512                    // pending lines before drop-on-full
	writeTimeout = 50 * time.Millisecond  // per-write deadline; a wedged collector never stalls a child
	redialEvery  = 1 * time.Second        // retry cadence when the socket is absent
	network      = "unix"
)

// Emitter is a fire-and-forget metrics client. A single background goroutine
// (Run) owns the connection: it dials the orchestrator metrics socket, and if
// the socket is absent (a standalone run) it keeps retrying and silently drops.
// Emit never blocks and never errors out to the caller — metrics are diagnostic
// and must never backpressure the audio path.
type Emitter struct {
	system  string
	path    string
	pending chan []byte
	closed  atomic.Bool
}

// NewEmitter returns an Emitter tagged with the given system ("stt"|"llm"|"tts").
// An empty path uses DefaultPath. Pass os.Getenv(EnvSocket) for the path so the
// orchestrator can redirect it on spawn.
func NewEmitter(system, path string) *Emitter {
	if path == "" {
		path = DefaultPath
	}
	return &Emitter{
		system:  system,
		path:    path,
		pending: make(chan []byte, emitBuf),
	}
}

// Run owns the connection until ctx is done. It is safe to start even when the
// socket never exists — it just keeps dropping. Start it once, in its own
// goroutine, from the system's main.
func (e *Emitter) Run(ctx context.Context) {
	var conn net.Conn
	defer func() {
		if conn != nil {
			_ = conn.Close()
		}
	}()

	for {
		if conn == nil {
			c, err := net.Dial(network, e.path)
			if err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(redialEvery):
					continue
				}
			}
			conn = c
		}

		select {
		case <-ctx.Done():
			return
		case b := <-e.pending:
			_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if _, err := conn.Write(b); err != nil {
				_ = conn.Close()
				conn = nil // redial on next iteration; this line is lost
			}
		}
	}
}

// Emit stamps system + wall-clock and does a non-blocking marshal+send. A full
// buffer or absent connection drops the event.
func (e *Emitter) Emit(m MetricEvent) {
	if e == nil || e.closed.Load() {
		return
	}
	if m.System == "" {
		m.System = e.system
	}
	if m.TsMs == 0 {
		m.TsMs = time.Now().UnixMilli()
	}
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	b = append(b, '\n')
	select {
	case e.pending <- b:
	default: // buffer full: drop
	}
}

// Mark is a one-liner for a checkpoint with no extra payload.
func (e *Emitter) Mark(u wire.UttID, s Stage) {
	e.Emit(MetricEvent{UttID: u, Stage: s})
}

// MarkDelta is a one-liner for a checkpoint carrying a stage-local duration.
func (e *Emitter) MarkDelta(u wire.UttID, s Stage, deltaMs float64) {
	e.Emit(MetricEvent{UttID: u, Stage: s, DeltaMs: deltaMs})
}

// Close makes subsequent Emit calls no-ops. The Run goroutine still exits on
// ctx cancellation.
func (e *Emitter) Close() {
	if e != nil {
		e.closed.Store(true)
	}
}
