// Command orchestrator launches the STT, LLM, and TTS systems as headless child
// processes, owns the terminal with a live latency dashboard, collects each
// stage's per-utterance timing over a metrics socket, joins them by utt_id into
// true end-to-end latency, and writes a post-run report. It is a process
// supervisor with a fail-fast policy: if any child exits, the others are stopped
// and the run ends with a report.
//
//	cd AI-Receptionist-Orcastrator && go run ./cmd/orchestrator
//
// Stop with Ctrl-C (graceful) — the report prints to the restored terminal and
// lands in runs/<timestamp>/.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	sharedmetrics "github.com/ai-receptionist/shared/metrics"

	"github.com/ai-receptionist/orchestrator/internal/collect"
	"github.com/ai-receptionist/orchestrator/internal/config"
	"github.com/ai-receptionist/orchestrator/internal/report"
	"github.com/ai-receptionist/orchestrator/internal/stats"
	"github.com/ai-receptionist/orchestrator/internal/supervisor"
	"github.com/ai-receptionist/orchestrator/internal/tui"
)

func main() {
	cwd, _ := os.Getwd()
	cfg := config.Defaults(cwd)

	flag.StringVar(&cfg.ProjectsRoot, "projects-root", cfg.ProjectsRoot, "dir containing STT-system / LLM-System / TTS-System")
	flag.StringVar(&cfg.MetricsSock, "metrics-sock", cfg.MetricsSock, "orchestrator-owned metrics socket")
	flag.BoolVar(&cfg.Prebuilt, "prebuilt", cfg.Prebuilt, "launch ./bin/<name> instead of `go run`")
	flag.DurationVar(&cfg.ReadyTimeout, "ready-timeout", cfg.ReadyTimeout, "max wait for a child to become ready")
	flag.DurationVar(&cfg.StopGrace, "stop-grace", cfg.StopGrace, "SIGTERM->SIGKILL window per child")
	flag.Parse()

	// Root context: cancelled by SIGINT/SIGTERM, by the TUI quitting, or by a
	// fail-fast child death.
	base, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(base)
	defer cancel()

	startMs := time.Now().UnixMilli()

	// The live UI is created only after all children are ready (so its alt-screen
	// doesn't fight the startup logs). Until then, status goes to stderr.
	var uiPtr atomic.Pointer[tui.UI]
	logStartup := func(format string, a ...any) { fmt.Fprintf(os.Stderr, "[orchestrator] "+format+"\n", a...) }

	// --- metrics collection (must be listening before any child starts) ---
	listener := sharedmetrics.NewListener(cfg.MetricsSock)
	if err := listener.Start(ctx); err != nil {
		logStartup("metrics socket %s failed: %v", cfg.MetricsSock, err)
		os.Exit(1)
	}
	logStartup("metrics socket up: %s", cfg.MetricsSock)

	agg := stats.New()
	var uttMu sync.Mutex
	var doneUtts []*collect.Utterance // every finalized utterance, for the detail file
	onFinal := func(u *collect.Utterance) {
		agg.Observe(u)
		uttMu.Lock()
		doneUtts = append(doneUtts, u)
		uttMu.Unlock()
		if ui := uiPtr.Load(); ui != nil {
			ui.OnUtterance(u)
			ui.OnStats(agg.Snapshot(stats.LiveWindow))
		}
	}
	joiner := collect.NewJoiner(onFinal, 8*time.Second)

	var rawMu sync.Mutex
	var rawBuf []sharedmetrics.MetricEvent
	onRaw := func(ev sharedmetrics.MetricEvent) {
		rawMu.Lock()
		rawBuf = append(rawBuf, ev)
		rawMu.Unlock()
		// Drive the live pipeline indicator: every event, not just finalized
		// utterances, so the user sees which stage is active in real time.
		if ui := uiPtr.Load(); ui != nil {
			ui.OnStage(ev)
		}
	}
	collector := collect.NewCollector(listener.Events(), joiner, onRaw)
	go collector.Run(ctx)

	// --- supervise the children ---
	onStatus := func(name config.ChildName, state string) {
		if ui := uiPtr.Load(); ui != nil {
			ui.OnChildStatus(string(name), state)
		} else {
			logStartup("%s: %s", name, state)
		}
	}
	sup := supervisor.New(cfg, onStatus)

	if err := sup.Start(ctx); err != nil {
		// A child never came up: stop the rest and exit without a TUI.
		sup.Shutdown()
		_ = listener.Close()
		logStartup("startup failed: %v", err)
		os.Exit(1)
	}

	// --- go live ---
	// All three children are ready by now (Start gated on it). The model is
	// seeded with that state; do NOT Send to the program before ui.Run starts
	// its loop — Bubble Tea's Send blocks until the loop receives, so a pre-Run
	// Send on this goroutine would deadlock before ui.Run is ever reached.
	ui := tui.New()
	uiPtr.Store(ui)

	// Fail-fast watcher: the first child to die records the cause and cancels ctx
	// (which quits the TUI). On graceful shutdown, Wait returns once ctx is done.
	var (
		failMu      sync.Mutex
		failedChild config.ChildName
		failCause   string
	)
	waitDone := make(chan struct{})
	go func() {
		died, cause := sup.Wait(ctx)
		if died != nil {
			failMu.Lock()
			failedChild = supervisor.DeadChildName(died)
			failCause = cause.Error()
			failMu.Unlock()
		}
		cancel()
		close(waitDone)
	}()

	// Blocks until the user quits or ctx is cancelled (signal / fail-fast).
	_ = ui.Run(ctx)
	cancel()
	<-waitDone

	// --- teardown + report ---
	sup.Shutdown()
	_ = listener.Close()

	endMs := time.Now().UnixMilli()
	sess := report.Session{StartedMs: startMs, EndedMs: endMs, DurationMs: endMs - startMs, Mode: "live"}
	failMu.Lock()
	if failedChild != "" {
		sess.Exit = "fail_fast"
		sess.FailedChild = string(failedChild)
		sess.FailCause = failCause
	} else {
		sess.Exit = "graceful"
	}
	failMu.Unlock()

	rep := report.Build(agg.Snapshot(0), sess)
	rawMu.Lock()
	rawCopy := append([]sharedmetrics.MetricEvent(nil), rawBuf...)
	rawMu.Unlock()

	dir := filepath.Join(cfg.RunsDir, time.Now().Format("20060102-150405"))
	paths, err := rep.WriteFiles(dir, rawCopy)

	// Full per-utterance step timeline (every stage of every utterance).
	uttMu.Lock()
	uttCopy := append([]*collect.Utterance(nil), doneUtts...)
	uttMu.Unlock()
	if derr := os.MkdirAll(dir, 0o755); derr == nil {
		detailsPath := filepath.Join(dir, "details.txt")
		if werr := os.WriteFile(detailsPath, []byte(report.RenderDetails(uttCopy)), 0o644); werr == nil {
			paths = append(paths, detailsPath)
		}
	}

	fmt.Print(rep.RenderText())
	if err != nil {
		fmt.Fprintf(os.Stderr, "report: write failed: %v\n", err)
	}
	for _, p := range paths {
		fmt.Println("wrote", p)
	}

	if sess.Exit == "fail_fast" {
		os.Exit(1)
	}
}
