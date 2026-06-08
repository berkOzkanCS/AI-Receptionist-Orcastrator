package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/ai-receptionist/orchestrator/internal/collect"
	"github.com/ai-receptionist/orchestrator/internal/stats"
)

func (m model) View() string {
	status := m.renderStatusBar()
	live := m.renderLivePanel()
	body := lipgloss.JoinHorizontal(lipgloss.Top, m.renderLastUtterance(), m.renderAggregates())
	hint := m.renderHintBar()
	return lipgloss.JoinVertical(lipgloss.Left, status, live, body, hint)
}

// renderLivePanel shows which pipeline stage the current utterance is at, with a
// heartbeat that makes a silent/broken pipeline obvious.
func (m model) renderLivePanel() string {
	title := styleTitle.Render("Live pipeline")
	spin := spinnerFrames[m.frame%len(spinnerFrames)]
	active := m.liveActive()

	var headline string
	switch {
	case active && m.liveStep >= 0 && m.liveStep < len(stepActivities):
		h := styleAccent.Render(spin + " " + stepActivities[m.liveStep] + "…")
		if m.liveCategory != "" {
			h += "   " + styleCategory.Render(m.liveCategory)
		}
		headline = h
	case m.liveStep >= 0:
		// Not active, but an utterance was processed — keep it on screen as done.
		h := styleOK.Render("✓ done")
		if m.liveCategory != "" {
			h += styleDim.Render(" · ") + styleCategory.Render(m.liveCategory)
		}
		headline = h + stylePlaceholder.Render("   — waiting for the next utterance")
	default:
		headline = stylePlaceholder.Render("◌ ready — speak into the mic to begin")
	}

	// The ribbon persists: completed steps stay green until the NEXT utterance
	// starts (which resets liveStep). Only the active step animates.
	var parts []string
	for i, name := range stepNames {
		var cell string
		switch {
		case m.liveStep < 0:
			cell = stylePlaceholder.Render("○ " + name)
		case i < m.liveStep:
			cell = styleOK.Render("✓ ") + styleBright.Render(name)
		case i == m.liveStep:
			if active {
				cell = styleAccent.Render(spin + " " + name)
			} else {
				cell = styleOK.Render("✓ ") + styleBright.Render(name)
			}
		default:
			cell = stylePlaceholder.Render("○ " + name)
		}
		parts = append(parts, cell)
	}
	ribbon := strings.Join(parts, stylePlaceholder.Render("  ─→  "))

	var beat string
	if m.totalEvents == 0 {
		beat = stylePlaceholder.Render("no metrics received yet — if this stays after you speak, check the mic and API keys")
	} else {
		id := ""
		if m.liveUtt != "" {
			id = "utterance " + shortID(string(m.liveUtt)) + " · "
		}
		beat = styleDim.Render(fmt.Sprintf("%sevents %d · last %s ago", id, m.totalEvents, fmtAge(time.Since(m.lastEventAt))))
	}

	content := strings.Join([]string{title, headline, ribbon, beat}, "\n")
	return stylePaneBorder.Width(106).Render(content)
}

func fmtAge(d time.Duration) string {
	s := d.Seconds()
	if s < 10 {
		return fmt.Sprintf("%.1fs", s)
	}
	return fmt.Sprintf("%.0fs", s)
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

// renderLastUtterance shows the most recent utterance as a simple list of how
// long each pipeline step took — one duration per step, plain-language note.
func (m model) renderLastUtterance() string {
	if len(m.rows) == 0 {
		title := styleTitle.Render("Last utterance — how long each step took")
		hint := stylePlaceholder.Render("  (speak — each step's duration appears here)")
		return stylePaneBorder.Width(58).Render(strings.Join([]string{title, "", hint}, "\n"))
	}
	u := m.rows[len(m.rows)-1]

	hdr := "Last utterance · " + shortID(string(u.UttID))
	if u.Category != "" {
		hdr += " · " + u.Category
	}
	hdr += " · cat:" + u.CatSource()
	title := styleTitle.Render(hdr) + "  " + pathStyle(u.Path()).Render(u.Path())
	colhdr := styleHeader.Render(fmt.Sprintf("%-18s %8s   %s", "step", "took", "what it measures"))

	lines := []string{title, colhdr}
	for _, p := range u.Phases() {
		took := stylePlaceholder.Render(fmt.Sprintf("%8s", "—"))
		if p.OK {
			took = latencyStyle(p.TookMs, 300, 700).Render(fmt.Sprintf("%8s", fmtMs(p.TookMs)+"ms"))
		}
		lines = append(lines, fmt.Sprintf("%s %s   %s",
			styleBright.Render(fmt.Sprintf("%-18s", p.Name)), took, styleDim.Render(p.Note)))
	}
	if v, ok := u.Metric(collect.ME2E); ok {
		lines = append(lines, stylePlaceholder.Render(strings.Repeat("─", 48)))
		lines = append(lines, fmt.Sprintf("%s %s   %s",
			styleAccent.Render(fmt.Sprintf("%-18s", "End-to-end (total)")),
			styleAccent.Render(fmt.Sprintf("%8s", fmtMs(v)+"ms")),
			styleDim.Render("you start speaking → you hear it")))
	}
	return stylePaneBorder.Width(58).Render(strings.Join(lines, "\n"))
}

// pathStyle color-codes the response path: catalog green, llm violet, filler
// amber, everything else muted.
func pathStyle(p string) lipgloss.Style {
	switch {
	case strings.HasPrefix(p, "catalog"):
		return styleOK
	case strings.Contains(p, "llm"):
		return styleAccent
	case strings.HasPrefix(p, "filler"):
		return styleWarn
	case p == "error":
		return styleErr
	default:
		return stylePlaceholder
	}
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
