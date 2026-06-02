// Package config holds the orchestrator's runtime configuration: where the
// three child projects live, which socket paths the pipeline uses, the child
// launch commands, and the supervision timeouts. Everything is derived from a
// small set of flags with sensible defaults so a bare `go run ./cmd/orchestrator`
// works when the four repos sit side by side.
package config

import (
	"path/filepath"
	"time"

	sharedmetrics "github.com/ai-receptionist/shared/metrics"
	"github.com/ai-receptionist/shared/wire"
)

// Socket paths used across the pipeline. The data sockets are owned by the
// children; the metrics socket is owned by the orchestrator.
const (
	STTSocket = "/tmp/stt-system.sock" // STT listens, LLM dials
	LLMSocket = "/tmp/llm-system.sock" // LLM listens, TTS dials
)

// Config is the fully-resolved orchestrator configuration.
type Config struct {
	ProjectsRoot string        // dir containing STT-system / LLM-System / TTS-System
	MetricsSock  string        // orchestrator-owned metrics socket
	Prebuilt     bool          // launch ./bin/<name> instead of `go run`
	FailFast     bool          // stop all children when any one exits (always true here; flag kept for clarity)
	ReadyTimeout time.Duration // max wait for a child's readiness probe
	StopGrace    time.Duration // SIGTERM -> SIGKILL window per child
	RunsDir      string        // where post-run reports are written
}

// ChildName is one of the three pipeline stages.
type ChildName string

const (
	STT ChildName = "stt"
	LLM ChildName = "llm"
	TTS ChildName = "tts"
)

// ChildSpec fully describes how to launch and supervise one child process.
type ChildSpec struct {
	Name      ChildName
	Dir       string   // cmd.Dir — the child's own project root (load-bearing: children read files relative to CWD)
	Args      []string // argv, e.g. {"go","run","./cmd"}
	Env       []string // extra KEY=VAL appended to os.Environ()
	ReadySock string   // unix socket to poll-dial for readiness; "" => ready after a short settle
}

// Defaults returns a Config with the standard sibling layout. cwd is the
// orchestrator's working directory; the three projects are expected one level up.
func Defaults(cwd string) Config {
	return Config{
		ProjectsRoot: filepath.Dir(cwd),
		MetricsSock:  sharedmetrics.DefaultPath,
		Prebuilt:     false,
		FailFast:     true,
		ReadyTimeout: 90 * time.Second, // STT's embedding model can take a while on first run
		StopGrace:    5 * time.Second,
		RunsDir:      filepath.Join(cwd, "runs"),
	}
}

// Children returns the three child specs in launch order (STT -> LLM -> TTS),
// with the headless flag and metrics-socket env injected into each.
func (c Config) Children() []ChildSpec {
	env := []string{
		"ORCH_HEADLESS=1",
		sharedmetrics.EnvSocket + "=" + c.MetricsSock,
		// Half-duplex speaking gate: both STT and TTS agree on this socket so STT
		// can mute its mic while TTS is talking (stops acoustic feedback).
		wire.EnvSpeakingSocket + "=" + wire.DefaultSpeakingSocket,
	}
	stt := []string{"go", "run", "./cmd"}
	llm := []string{"go", "run", "./cmd/consumer"}
	tts := []string{"go", "run", "./cmd/tts"}
	if c.Prebuilt {
		stt = []string{"./bin/stt"}
		llm = []string{"./bin/llm"}
		tts = []string{"./bin/tts"}
	}
	return []ChildSpec{
		{Name: STT, Dir: filepath.Join(c.ProjectsRoot, "STT-system"), Args: stt, Env: env, ReadySock: STTSocket},
		{Name: LLM, Dir: filepath.Join(c.ProjectsRoot, "LLM-System"), Args: llm, Env: env, ReadySock: LLMSocket},
		{Name: TTS, Dir: filepath.Join(c.ProjectsRoot, "TTS-System"), Args: tts, Env: env, ReadySock: ""},
	}
}
