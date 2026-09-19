package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"aven/internal/caddyconf"
	"aven/internal/config"
)

// View renders the lazydocker-style dashboard:
//
//	┌─ Domains ──┬─ myapp.aven ─────────────┐
//	│            │ tab: Details/Requests/...    │
//	├─ System ───┤                              │
//	│            │                              │
//	└────────────┴──────────────────────────────┘
//	legend (global keys)
//	status / error line
func (m model) View() string {
	if m.width == 0 {
		return stDim.Render("loading aven…")
	}
	const legendH = 3
	contentH := m.height - legendH
	if contentH < 8 {
		contentH = 8
	}
	leftW := m.width * 3 / 10
	if leftW < 30 {
		leftW = 30
	}
	if leftW > 46 {
		leftW = 46
	}
	rightW := m.width - leftW - 2 // border columns
	if rightW < 40 {
		rightW = 40
	}

	systemH := 8
	domainsH := contentH - systemH
	if domainsH < 3 {
		domainsH = 3
		systemH = contentH - domainsH
	}

	domains := box(m.renderDomains(domainsH-2), leftW, domainsH, "Domains")
	system := box(m.renderSystem(systemH-2), leftW, systemH, "System")
	mainTitle := "aven"
	if s, ok := m.selected(); ok {
		mainTitle = s.FQDN
	}
	if m.v == viewAdd {
		mainTitle = "Add domain"
	}
	main := box(m.renderMain(rightW-2, contentH-2), rightW, contentH, mainTitle)

	left := lipgloss.JoinVertical(lipgloss.Left, domains, system)
	gap := lipgloss.NewStyle().Width(1).Height(contentH).Render("")
	grid := lipgloss.JoinHorizontal(lipgloss.Top, left, gap, main)

	legend := m.renderLegend(rightW + leftW + 2)
	return grid + "\n" + legend
}

// box wraps content in a titled rounded border sized to exactly width x
// height outer cells: border(2) + title row(1) + body(height-3).
func box(content string, width, height int, title string) string {
	if height < 4 {
		height = 4
	}
	bodyH := height - 3
	lines := strings.Split(content, "\n")
	for i := range lines {
		lines[i] = cut(lines[i], width-2)
	}
	for len(lines) < bodyH {
		lines = append(lines, "")
	}
	if len(lines) > bodyH {
		lines = lines[:bodyH]
	}
	body := strings.Join(lines, "\n")
	return panelBorder(true, cBorderFocus).
		Width(width - 2).
		Height(bodyH).
		Render(stTitle.Render(" "+title+" ") + "\n" + body)
}

func (m model) renderDomains(h int) string {
	if len(m.rows) == 0 {
		if m.errMsg == "" {
			return stDim.Render("  no domains yet — press a to add one")
		}
	}
	var b strings.Builder
	for i, r := range m.rows {
		cursor := "  "
		nameStyle := lipgloss.NewStyle()
		if i == m.cursor {
			cursor = stCyan.Render("▸ ")
			nameStyle = stSelected
		}
		status := stGreen.Render("● up")
		if r.Paused {
			status = stYellow.Render("⏸ paused")
			if i != m.cursor {
				nameStyle = stDim
			}
		} else if !r.BackendUp {
			status = stRed.Render("✗ down")
			if i != m.cursor {
				nameStyle = stDim
			}
		}
		line := fmt.Sprintf("%s%-16s %-6s %s", cursor, nameStyle.Render(r.Name), stDim.Render(r.Kind), status)
		b.WriteString(cut(line, 200) + "\n")
		if i == m.cursor && r.BackendNote != "" && !r.BackendUp {
			b.WriteString(stDim.Render("    "+r.BackendNote) + "\n")
		}
	}
	return b.String()
}

func (m model) renderSystem(h int) string {
	daemon := stGreen.Render("● running")
	if !m.daemonUp {
		daemon = stRed.Render("○ stopped")
	}
	ca := stRed.Render("✗ untrusted")
	hint := "t fix"
	if m.caTrusted {
		ca = stGreen.Render("✓ trusted")
		hint = ""
	}
	res := stRed.Render("✗ missing")
	resHint := "u fix"
	if m.resolver {
		res = stGreen.Render("✓ installed")
		resHint = ""
	}
	suffix := ".aven"
	if m.cfg != nil {
		suffix = "." + m.cfg.Suffix
	}
	rows := []string{
		fmt.Sprintf("daemon   %s", daemon),
		fmt.Sprintf("CA       %s %s", ca, stDim.Render(hint)),
		fmt.Sprintf("resolver %s %s", res, stDim.Render(resHint)),
		fmt.Sprintf("suffix   %s", stDim.Render(suffix)),
	}
	return strings.Join(rows, "\n")
}

func (m model) renderMain(w, h int) string {
	if m.v == viewAdd {
		return m.renderAddForm(w)
	}
	if m.v == viewConfirm {
		return "\n" + stWarn.Render(fmt.Sprintf("  Delete %s?  (y/n)", m.confirmName)) + "\n\n" +
			stDim.Render("  the config entry is removed and the daemon hot-reloads")
	}

	tabs := m.renderTabs(w)
	var body string
	switch m.tab {
	case tabRequests:
		body = m.renderRequests(w, h-2)
	case tabCaddy:
		body = renderTail(caddyconf.LogPath(), h-2)
	case tabConfig:
		body = m.renderConfig(w, h-2)
	default:
		body = m.renderDetails(w)
	}
	return tabs + "\n" + body
}

func (m model) renderTabs(w int) string {
	var parts []string
	for i, name := range tabNames {
		label := fmt.Sprintf(" %d %s ", i+1, name)
		if tab(i) == m.tab {
			parts = append(parts, stCyan.Render("["+strings.TrimSpace(label)+"]"))
		} else {
			parts = append(parts, stDim.Render(label))
		}
	}
	return strings.Join(parts, "")
}

func (m model) renderDetails(w int) string {
	s, ok := m.selected()
	if !ok {
		return stDim.Render("\n  select a domain, or press a to add one")
	}
	status := stGreen.Render("● up")
	if s.Paused {
		status = stYellow.Render("⏸ paused (space to resume)")
	} else if !s.BackendUp {
		status = stRed.Render("✗ down — " + s.BackendNote)
	}
	url := "https://" + s.FQDN
	rows := []string{
		kv("url", url),
		kv("kind", s.Kind),
		kv("daemon", boolWord(m.daemonUp)),
		"",
		kv("status", status),
	}
	if s.Kind == config.KindProxy {
		rows = append(rows, kv("target", s.Spec))
	} else {
		rows = append(rows, kv("root", s.Spec))
	}
	rows = append(rows,
		"",
		stDim.Render("  c copy url · o open · space pause/resume · d delete"),
	)
	return strings.Join(rows, "\n")
}

func (m model) renderRequests(w, h int) string {
	if len(m.reqRows) == 0 {
		return stDim.Render("\n  no requests yet — hit the domain and this panel fills live (1s)")
	}
	var b strings.Builder
	b.WriteString(stDim.Render(fmt.Sprintf("  %-8s %-7s %-6s %-42s %-4s %s",
		"TIME", "METHOD", "STATUS", "URI", "SIZE", "MS")) + "\n")
	for _, r := range m.reqRows {
		status := fmt.Sprintf("%d", r.Status)
		switch {
		case r.Status >= 500:
			status = stRed.Render(status)
		case r.Status >= 400:
			status = stYellow.Render(status)
		default:
			status = stGreen.Render(status)
		}
		b.WriteString(cut(fmt.Sprintf("  %-8s %-7s %-6s %-42s %4d %6.1f",
			r.Time.Format("15:04:05"), r.Method, status, r.URI, r.Size, r.DurationMS), 300) + "\n")
	}
	lines := strings.Split(b.String(), "\n")
	if len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	return strings.Join(lines, "\n")
}

func (m model) renderConfig(w, h int) string {
	if m.cfg == nil {
		return ""
	}
	data, err := os.ReadFile(config.Path())
	if err != nil {
		return stErr.Render("  " + err.Error())
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}

func (m model) renderAddForm(w int) string {
	kind := stGreen.Render("proxy (host:port)")
	if m.kindStatic {
		kind = stGreen.Render("static (root dir)")
	}
	if m.kindStatic {
		m.valueInput.Placeholder = "~/sites/myapp"
	} else {
		m.valueInput.Placeholder = "localhost:3000"
	}
	return strings.Join([]string{
		"",
		"  name   " + m.nameInput.View(),
		"  kind   " + kind + stDim.Render("   ←/→ to switch"),
		"  value  " + m.valueInput.View(),
		"",
		stDim.Render("  enter next/submit · tab next field · esc cancel"),
	}, "\n")
}

func (m model) renderLegend(w int) string {
	list := "↑/↓ select · a add · d delete · space pause · s daemon · t trust · u setup · o open · c copy · q quit"
	main := "tab/1-4 panel · r refresh"
	if m.tab == tabRequests {
		main = "r replay last request · tab/1-4 panel"
	}
	if m.v == viewAdd {
		list = "add form: enter next/submit · tab next field · ←/→ switch kind · esc cancel"
		main = ""
	}
	line := stDim.Render(cut(list, w))
	ctx := ""
	if m.errMsg != "" {
		ctx = stErr.Render("error: " + cut(m.errMsg, w-8))
	} else if m.status != "" {
		ctx = stDim.Render(cut(m.status, w))
	} else if m.busy {
		ctx = stDim.Render("working…")
	}
	return line + "\n" + stDim.Render(cut(main, w)) + "\n" + ctx
}

// helpers

func kv(k, v string) string {
	return "  " + stDim.Render(fmt.Sprintf("%-8s", k)) + " " + v
}

func boolWord(b bool) string {
	if b {
		return stGreen.Render("● running")
	}
	return stRed.Render("○ stopped")
}

// cut truncates a single styled line to display width n, preserving ANSI
// sequences via lipgloss-aware truncation.
func cut(s string, n int) string {
	if n <= 0 {
		return ""
	}
	return lipgloss.NewStyle().MaxWidth(n).Render(s)
}

func renderTail(path string, h int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return stDim.Render("\n  no log yet")
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	for i, l := range lines {
		lines[i] = "  " + stDim.Render(l)
	}
	return strings.Join(lines, "\n")
}
