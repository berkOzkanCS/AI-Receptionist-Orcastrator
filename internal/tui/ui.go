// Package tui is the orchestrator's live dashboard. It mirrors the STT-system
// house style: a UI struct wraps a *tea.Program and exposes typed On* senders so
// the collector, stats aggregator, and supervisor never touch tea.Msg directly.
// The UI owns the terminal (alt-screen); the children run headless.
package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ai-receptionist/orchestrator/internal/collect"
	"github.com/ai-receptionist/orchestrator/internal/stats"
)

// UI wraps the Bubble Tea program.
type UI struct {
	prog *tea.Program
}

// New constructs the dashboard. Nothing renders until Run is called.
func New() *UI {
	return &UI{prog: tea.NewProgram(newModel(), tea.WithAltScreen())}
}

// Run starts the event loop. It blocks until the user quits (q/esc/ctrl+c) or
// ctx is cancelled (e.g. the supervisor reports a fail-fast), then returns.
func (u *UI) Run(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		u.prog.Quit()
	}()
	_, err := u.prog.Run()
	return err
}

// OnUtterance reports a finalized utterance row.
func (u *UI) OnUtterance(utt *collect.Utterance) { u.prog.Send(utteranceMsg{u: utt}) }

// OnStats refreshes the aggregates pane.
func (u *UI) OnStats(s stats.Snapshot) { u.prog.Send(statsMsg{snap: s}) }

// OnChildStatus reflects a child's lifecycle state in the status bar.
func (u *UI) OnChildStatus(name, state string) { u.prog.Send(childStatusMsg{name: name, state: state}) }

// OnError surfaces an async error in the hint bar.
func (u *UI) OnError(source, msg string) { u.prog.Send(errorMsg{source: source, msg: msg}) }
