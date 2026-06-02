package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/ai-receptionist/orchestrator/internal/collect"
	"github.com/ai-receptionist/orchestrator/internal/stats"
)

func (m model) View() string {
	status := m.renderStatusBar()
	body := lipgloss.JoinHorizontal(lipgloss.Top, m.renderUtterances(), m.renderAggregates())
	hint := m.renderHintBar()
	return lipgloss.JoinVertical(lipgloss.Left, status, body, hint)
}

func (m model) renderStatusBar() string {
	dot := styleDot.Render("●")
	children := fmt.Sprintf("stt:%s  llm:%s  tts:%s",
		colorState(m.children["stt"]), colorState(m.children["llm"]), colorState(m.children["tts"]))
	counts := styleDim.Render(fmt.Sprintf("utt %d  ok %d  no-speak %d  err %d",
		m.snap.Total, m.snap.Completed, m.snap.NoSpeak, m.snap.Errored))
	line := fmt.Sprintf("%s orchestrator   %s   %s", dot, children, counts)
	return styleStatusBar.Width(maxInt(m.width, 40)).Render(line)
}

func colorState(s string) string {
	switch s {
	case "ready":
		return styleOK.Render(s)
	case "dead":
		return styleErr.Render(s)
	case "starting":
		return styleWarn.Render(s)
	default:
		return stylePlaceholder.Render(s)
	}
}

func (m model) renderUtterances() string {
	title := styleTitle.Render("Recent Utterances")
	header := styleHeader.Render(fmt.Sprintf("%-9s %6s %5s %6s %5s %6s %7s",
		"utt", "stt", "dec", "gem", "1stB", "play", "e2e"))

	var lines []string
	lines = append(lines, title, header)
	if len(m.rows) == 0 {
		lines = append(lines, stylePlaceholder.Render("  (speak into the mic — utterances appear here)"))
	}
	for _, u := range m.rows {
		lines = append(lines, m.renderRow(u))
	}
	content := strings.Join(lines, "\n")
	return stylePaneBorder.Width(56).Render(content)
}

func (m model) renderRow(u *collect.Utterance) string {
	id := shortID(string(u.UttID))
	idCell := styleAccent.Render(fmt.Sprintf("%-9s", id))

	cell := func(key string, warn, bad float64) string {
		v, ok := u.Metric(key)
		if !ok {
			return stylePlaceholder.Render(fmt.Sprintf("%6s", "—"))
		}
		return latencyStyle(v, warn, bad).Render(fmt.Sprintf("%6s", fmtMs(v)))
	}

	stt := cell(collect.MStt, 500, 900)
	dec := latencyCellNarrow(u, collect.MDecision, 80, 200)
	gem := cell(collect.MGemini, 600, 1200)
	tb := latencyCellNarrow(u, collect.MFirstByte, 250, 500)
	play := cell(collect.MPlayed, 350, 700)
	e2e := cell(collect.ME2E, 1200, 2000)

	tag := ""
	switch {
	case u.Err != "":
		tag = " " + styleErr.Render("ERR")
	case u.NoSpeak:
		tag = " " + stylePlaceholder.Render("no-speak")
	}
	// dec is 5 wide, the rest 6/7; assemble in the header's column order.
	return fmt.Sprintf("%s %s %s %s %s %s %s%s", idCell, stt, dec, gem, tb, play, e2e, tag)
}

// latencyCellNarrow renders a 5-wide metric cell.
func latencyCellNarrow(u *collect.Utterance, key string, warn, bad float64) string {
	v, ok := u.Metric(key)
	if !ok {
		return stylePlaceholder.Render(fmt.Sprintf("%5s", "—"))
	}
	return latencyStyle(v, warn, bad).Render(fmt.Sprintf("%5s", fmtMs(v)))
}

func (m model) renderAggregates() string {
	title := styleTitle.Render("Aggregates (rolling)")
	header := styleHeader.Render(fmt.Sprintf("%-12s %4s %6s %6s %6s", "stage", "n", "p50", "p95", "max"))

	lines := []string{title, header}
	for _, d := range collect.DisplayOrder {
		st := m.snap.Stages[d.Key]
		label := d.Label
		if d.Key == collect.ME2E {
			label = styleAccent.Render(fmt.Sprintf("%-12s", d.Label))
		} else {
			label = styleBright.Render(fmt.Sprintf("%-12s", label))
		}
		if st.Count == 0 {
			lines = append(lines, fmt.Sprintf("%s %s", label,
				stylePlaceholder.Render(fmt.Sprintf("%4s %6s %6s %6s", "0", "—", "—", "—"))))
			continue
		}
		lines = append(lines, fmt.Sprintf("%s %4d %6s %6s %6s",
			label, st.Count, fmtMs(st.P50), fmtMs(st.P95), fmtMs(st.Max)))
	}
	content := strings.Join(lines, "\n")
	return stylePaneBorder.Width(44).Render(content)
}

func (m model) renderHintBar() string {
	left := stylePlaceholder.Render("q/ctrl+c quit")
	if m.lastErr != "" {
		return styleStatusBar.Width(maxInt(m.width, 40)).Render(
			styleErr.Render("err ")+styleDim.Render(truncate(m.lastErr, 80))+"   "+left)
	}
	return styleStatusBar.Width(maxInt(m.width, 40)).Render(left)
}

// --- formatting helpers ---

func fmtMs(v float64) string {
	switch {
	case v >= 10000:
		return fmt.Sprintf("%.1fk", v/1000)
	default:
		return fmt.Sprintf("%.0f", v)
	}
}

func shortID(id string) string {
	if i := strings.LastIndex(id, "-"); i >= 0 && i+1 < len(id) {
		return "#" + id[i+1:]
	}
	if len(id) > 8 {
		return id[len(id)-8:]
	}
	return id
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Snapshot summary line used by the report (kept here so the table style stays
// next to the dashboard style).
func StageStatsLine(label string, st stats.StageStats) string {
	if st.Count == 0 {
		return fmt.Sprintf("%-14s %5s %6s %6s %6s %6s %6s", label, "0", "—", "—", "—", "—", "—")
	}
	return fmt.Sprintf("%-14s %5d %6s %6s %6s %6s %6s",
		label, st.Count, fmtMs(st.Min), fmtMs(st.Avg), fmtMs(st.P50), fmtMs(st.P95), fmtMs(st.Max))
}
