package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	sharedmetrics "github.com/ai-receptionist/shared/metrics"
	"github.com/ai-receptionist/shared/wire"

	"github.com/ai-receptionist/orchestrator/internal/collect"
	"github.com/ai-receptionist/orchestrator/internal/stats"
)

const (
	maxRows         = 12 // recent utterance rows kept on screen
	defaultWidth    = 120
	defaultHeight   = 30
	refreshEvery    = 1 * time.Second
	spinnerInterval = 120 * time.Millisecond // animation cadence
	idleAfter       = 2500 * time.Millisecond // no events for this long => the pipeline is idle
)

// Pipeline steps shown in the live ribbon, in order.
var (
	stepNames      = []string{"Listen", "Transcribe", "Think", "Synthesize", "Speak"}
	stepActivities = []string{"Listening", "Transcribing", "Thinking", "Synthesizing", "Speaking"}
	spinnerFrames  = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
)

// pipelineStep maps a metric stage to a ribbon step index. ok=false means the
// stage doesn't advance the ribbon (but still counts as activity).
func pipelineStep(s sharedmetrics.Stage) (step int, ok bool) {
	switch s {
	case sharedmetrics.StageSpeechStart, sharedmetrics.StageSTTPartial:
		return 0, true // Listen
	case sharedmetrics.StageSTTFinal:
		return 1, true // Transcribe
	case sharedmetrics.StageLLMRegexHit, sharedmetrics.StageLLMEmbeddingHit,
		sharedmetrics.StageLLMDecision, sharedmetrics.StageLLMGeminiStart,
		sharedmetrics.StageLLMGemini, sharedmetrics.StageLLMEmit, sharedmetrics.StageLLMDropped:
		return 2, true // Think
	case sharedmetrics.StageTTSArrival, sharedmetrics.StageTTSDial, sharedmetrics.StageTTSFirstByte:
		return 3, true // Synthesize
	case sharedmetrics.StageTTSPlayed, sharedmetrics.StageTTSFinal:
		return 4, true // Speak
	}
	return 0, false // tts.dropped / tts.interrupt: activity, but don't advance
}

// Message types pushed into the model via tea.Program.Send.

type utteranceMsg struct{ u *collect.Utterance }
type statsMsg struct{ snap stats.Snapshot }
type childStatusMsg struct{ name, state string }
type errorMsg struct{ source, msg string }
type stageMsg struct{ ev sharedmetrics.MetricEvent }
type tickMsg struct{}
type spinnerMsg struct{}

// model holds whatever the collector / supervisor have pushed in; it owns no
// pipeline logic.
type model struct {
	rows     []*collect.Utterance // most recent finalized utterances (tail)
	snap     stats.Snapshot
	children map[string]string // "stt"/"llm"/"tts" -> state
	lastErr  string

	// live pipeline state
	liveUtt      wire.UttID
	liveStep     int // furthest ribbon step reached by the in-flight utterance
	liveCategory string
	lastEventAt  time.Time
	totalEvents  int
	frame        int // spinner frame counter

	width, height int
}

func newModel() model {
	return model{
		// The orchestrator only creates the TUI after all three children pass
		// readiness, so they start "ready" here; the supervisor sends "dead" via
		// a later (post-Run) Send if one exits.
		children: map[string]string{"stt": "ready", "llm": "ready", "tts": "ready"},
		snap:     stats.Snapshot{Stages: map[string]stats.StageStats{}},
		liveStep: -1,
		width:    defaultWidth,
		height:   defaultHeight,
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(refreshEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

func spinnerCmd() tea.Cmd {
	return tea.Tick(spinnerInterval, func(time.Time) tea.Msg { return spinnerMsg{} })
}

func (m model) Init() tea.Cmd { return tea.Batch(tickCmd(), spinnerCmd()) }

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

	case spinnerMsg:
		m.frame++
		return m, spinnerCmd()

	case stageMsg:
		m.totalEvents++
		m.lastEventAt = time.Now()
		if msg.ev.UttID != m.liveUtt {
			// A new utterance is in flight: reset the ribbon to its start.
			m.liveUtt = msg.ev.UttID
			m.liveStep = -1
			m.liveCategory = ""
		}
		if step, ok := pipelineStep(msg.ev.Stage); ok && step > m.liveStep {
			m.liveStep = step
		}
		if msg.ev.Category != "" {
			m.liveCategory = msg.ev.Category
		}

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

// liveActive reports whether an utterance is currently moving through the
// pipeline (events seen recently).
func (m model) liveActive() bool {
	return m.totalEvents > 0 && time.Since(m.lastEventAt) <= idleAfter
}
