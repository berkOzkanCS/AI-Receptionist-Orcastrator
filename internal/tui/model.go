package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ai-receptionist/orchestrator/internal/collect"
	"github.com/ai-receptionist/orchestrator/internal/stats"
)

const (
	maxRows       = 16 // recent utterance rows kept on screen
	defaultWidth  = 120
	defaultHeight = 30
	refreshEvery  = 1 * time.Second
)

// Message types pushed into the model via tea.Program.Send.

type utteranceMsg struct{ u *collect.Utterance }
type statsMsg struct{ snap stats.Snapshot }
type childStatusMsg struct{ name, state string }
type errorMsg struct{ source, msg string }
type tickMsg struct{}

// model holds whatever the collector / supervisor have pushed in; it owns no
// pipeline logic.
type model struct {
	rows     []*collect.Utterance // most recent finalized utterances (tail)
	snap     stats.Snapshot
	children map[string]string // "stt"/"llm"/"tts" -> state
	lastErr  string

	width, height int
}

func newModel() model {
	return model{
		children: map[string]string{"stt": "…", "llm": "…", "tts": "…"},
		snap:     stats.Snapshot{Stages: map[string]stats.StageStats{}},
		width:    defaultWidth,
		height:   defaultHeight,
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(refreshEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m model) Init() tea.Cmd { return tickCmd() }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		}

	case tickMsg:
		return m, tickCmd()

	case utteranceMsg:
		m.rows = append(m.rows, msg.u)
		if len(m.rows) > maxRows {
			m.rows = m.rows[len(m.rows)-maxRows:]
		}

	case statsMsg:
		m.snap = msg.snap

	case childStatusMsg:
		m.children[msg.name] = msg.state

	case errorMsg:
		m.lastErr = msg.source + ": " + msg.msg
	}
	return m, nil
}
