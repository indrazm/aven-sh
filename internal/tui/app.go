// Package tui implements the aven bubbletea dashboard in a
// lazydocker-style layout: a left column with the domain list and system
// checks, a right main panel with tabs (Details, Requests, Caddy log,
// Config), and a keybinding legend. All domain mutations delegate to
// internal/domain; the TUI holds no logic of its own.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"aven/internal/caddyconf"
	"aven/internal/config"
	"aven/internal/daemon"
	"aven/internal/domain"
	"aven/internal/requests"
	"aven/internal/setup"
	"aven/internal/trust"
)

type view int

const (
	viewList view = iota
	viewAdd
	viewConfirm
)

type tab int

const (
	tabDetails tab = iota
	tabRequests
	tabCaddy
	tabConfig
	tabCount
)

var tabNames = [tabCount]string{"Details", "Requests", "Caddy", "Config"}

// palette
var (
	cBorder      = lipgloss.AdaptiveColor{Light: "8", Dark: "8"}
	cBorderFocus = lipgloss.AdaptiveColor{Light: "4", Dark: "6"}
	cGreen       = lipgloss.Color("10")
	cRed         = lipgloss.Color("9")
	cYellow      = lipgloss.Color("11")
	cDim         = lipgloss.Color("8")
	cCyan        = lipgloss.Color("6")

	stTitle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "7", Dark: "7"})
	stDim      = lipgloss.NewStyle().Foreground(cDim)
	stGreen    = lipgloss.NewStyle().Foreground(cGreen)
	stRed      = lipgloss.NewStyle().Foreground(cRed)
	stYellow   = lipgloss.NewStyle().Foreground(cYellow)
	stCyan     = lipgloss.NewStyle().Foreground(cCyan)
	stSelected = lipgloss.NewStyle().Bold(true)
	stErr      = lipgloss.NewStyle().Foreground(cRed)
	stWarn     = lipgloss.NewStyle().Foreground(cYellow)
)

func panelBorder(focus bool, color lipgloss.TerminalColor) lipgloss.Style {
	c := color
	if !focus {
		c = cBorder
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(c)
}

type refreshMsg struct {
	daemonUp  bool
	caTrusted bool
	resolver  bool
	rows      []domain.Status
	cfg       *config.Config
	err       error
}

type resultMsg struct {
	action   string
	warnings []string
	err      error
}

type tickMsg time.Time

type model struct {
	cfg       *config.Config
	daemonUp  bool
	caTrusted bool
	resolver  bool
	rows      []domain.Status
	cursor    int
	v         view
	tab       tab
	focused   bool // main panel focus (tab cycling with tab key)
	width     int
	height    int
	status    string
	errMsg    string
	busy      bool

	confirmName string // domain captured when `d` was pressed

	// add form
	nameInput  textinput.Model
	valueInput textinput.Model
	kindStatic bool
	focus      int // 0 name, 1 kind, 2 value

	reqRows []requests.Request
}

// Run starts the TUI.
func Run() error {
	name := textinput.New()
	name.Placeholder = "myapp"
	name.CharLimit = 64
	value := textinput.New()
	value.CharLimit = 256
	m := model{nameInput: name, valueInput: value}
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func refreshCmd() tea.Msg {
	cfg, err := config.Load()
	if err != nil {
		return refreshMsg{err: err}
	}
	daemonUp, rows, err := domain.List()
	if err != nil {
		return refreshMsg{err: err}
	}
	return refreshMsg{
		daemonUp:  daemonUp,
		caTrusted: trust.Trusted(),
		resolver:  resolverUp(cfg),
		rows:      rows,
		cfg:       cfg,
	}
}

func resolverUp(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	data, err := os.ReadFile("/etc/resolver/" + cfg.Suffix)
	return err == nil && len(data) > 0
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Init() tea.Cmd {
	return tea.Batch(refreshCmd, tickCmd())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case refreshMsg:
		m.busy = false
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			return m, nil
		}
		m.errMsg = ""
		m.cfg = msg.cfg
		m.daemonUp = msg.daemonUp
		m.caTrusted = msg.caTrusted
		m.resolver = msg.resolver
		m.rows = msg.rows
		if m.cursor >= len(m.rows) {
			m.cursor = max(0, len(m.rows)-1)
		}
		m.loadRequests()
		return m, nil

	case tickMsg:
		m.loadRequests()
		return m, tickCmd()

	case resultMsg:
		m.busy = false
		parts := []string{msg.action}
		if msg.err != nil {
			parts = append(parts, "error: "+msg.err.Error())
			m.errMsg = strings.Join(parts, ": ")
			return m, refreshCmd
		}
		m.errMsg = ""
		for _, w := range msg.warnings {
			parts = append(parts, "warning: "+w)
		}
		m.status = strings.Join(parts, " — ")
		return m, refreshCmd

	case tea.KeyMsg:
		if m.busy {
			return m, nil
		}
		return m.handleKey(msg)
	}
	if m.v == viewAdd {
		return m.updateInputs(msg)
	}
	return m, nil
}

func (m *model) loadRequests() {
	if m.tab != tabRequests || m.cfg == nil || len(m.rows) == 0 || m.cursor >= len(m.rows) {
		return
	}
	rows, err := requests.Tail(caddyconf.AccessLogPath(), m.rows[m.cursor].FQDN, 100, 1<<20)
	if err == nil {
		m.reqRows = rows
	}
}

func (m model) selected() (domain.Status, bool) {
	if len(m.rows) == 0 || m.cursor >= len(m.rows) {
		return domain.Status{}, false
	}
	return m.rows[m.cursor], true
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.v {
	case viewAdd:
		return m.handleAddKey(msg)
	case viewConfirm:
		switch msg.String() {
		case "y":
			name := m.confirmName
			m.v = viewList
			m.busy = true
			m.status = "removing " + name + "…"
			return m, removeCmd(name)
		case "n", "esc", "q":
			m.v = viewList
			return m, nil
		}
		return m, nil
	}

	switch key := msg.String(); key {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		m.loadRequests()
	case "down", "j":
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}
		m.loadRequests()
	case "tab", "right":
		m.tab = (m.tab + 1) % tabCount
		m.loadRequests()
	case "left":
		m.tab = (m.tab + tabCount - 1) % tabCount
		m.loadRequests()
	case "1", "2", "3", "4":
		m.tab = tab(int(key[0] - '1'))
		m.loadRequests()
	case "r":
		if m.tab == tabRequests {
			if _, ok := m.selected(); ok && len(m.reqRows) > 0 {
				last := m.reqRows[len(m.reqRows)-1]
				m.busy = true
				m.status = fmt.Sprintf("replaying %s %s…", last.Method, last.URI)
				return m, replayCmd(last)
			}
			return m, nil
		}
		m.busy = true
		m.status = "refreshing…"
		return m, refreshCmd
	case "a":
		m.v = viewAdd
		m.focus = 0
		m.kindStatic = false
		m.nameInput.Reset()
		m.valueInput.Reset()
		m.nameInput.Focus()
		m.valueInput.Blur()
		m.errMsg, m.status = "", ""
		return m, textinput.Blink
	case "d":
		if s, ok := m.selected(); ok {
			m.confirmName = s.Name // capture target now, not at y-press
			m.v = viewConfirm
		}
	case "space":
		if s, ok := m.selected(); ok {
			m.busy = true
			action := "pausing"
			target := true
			if s.Paused {
				action, target = "resuming", false
			}
			m.status = action + " " + s.Name + "…"
			return m, pauseCmd(s.Name, target)
		}
	case "s":
		m.busy = true
		if m.daemonUp {
			m.status = "stopping daemon…"
			return m, stopDaemonCmd(m.cfg.AdminPort)
		}
		m.status = "starting daemon…"
		return m, startDaemonCmd()
	case "t":
		m.busy = true
		m.status = "trust setup (allow the password dialog if shown)…"
		return m, trustCmd()
	case "u":
		m.busy = true
		m.status = "setup (allow the password dialog if shown)…"
		return m, setupCmd()
	case "o":
		if s, ok := m.selected(); ok {
			url := "https://" + s.FQDN
			if err := exec.Command("open", url).Start(); err != nil {
				m.errMsg = err.Error()
			} else {
				m.status = "opened " + url
			}
		}
	case "c":
		if s, ok := m.selected(); ok {
			if err := copyClipboard("https://" + s.FQDN); err != nil {
				m.errMsg = "copy failed: " + err.Error()
			} else {
				m.status = "copied https://" + s.FQDN
			}
		}
	}
	return m, nil
}

func copyClipboard(s string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(s)
	return cmd.Run()
}

func (m model) handleAddKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key := msg.String(); key {
	case "esc":
		m.v = viewList
		return m, nil
	case "tab", "down":
		m.focus = (m.focus + 1) % 3
		m.syncFocus()
		return m, textinput.Blink
	case "up":
		m.focus = (m.focus + 2) % 3
		m.syncFocus()
		return m, textinput.Blink
	case "left", "right":
		if m.focus == 1 {
			m.kindStatic = !m.kindStatic
		}
		return m, nil
	case "enter":
		if m.focus < 2 {
			m.focus++
			m.syncFocus()
			return m, textinput.Blink
		}
		name := strings.TrimSpace(m.nameInput.Value())
		value := strings.TrimSpace(m.valueInput.Value())
		kind, want := config.KindProxy, "target"
		if m.kindStatic {
			kind, want = config.KindStatic, "root directory"
		}
		if name == "" {
			m.errMsg = "name is required"
			return m, nil
		}
		if value == "" {
			m.errMsg = want + " is required"
			return m, nil
		}
		m.v = viewList
		m.busy = true
		m.status = "adding " + name + "…"
		return m, addCmd(name, kind, value)
	}
	return m.updateInputs(msg)
}

func (m model) updateInputs(msg tea.Msg) (tea.Model, tea.Cmd) {
	ni, c1 := m.nameInput.Update(msg)
	m.nameInput = ni
	vi, c2 := m.valueInput.Update(msg)
	m.valueInput = vi
	return m, tea.Batch(c1, c2)
}

func (m *model) syncFocus() {
	switch m.focus {
	case 0:
		m.nameInput.Focus()
		m.valueInput.Blur()
	case 1:
		m.nameInput.Blur()
		m.valueInput.Blur()
	case 2:
		m.nameInput.Blur()
		m.valueInput.Focus()
	}
}

// commands

func addCmd(name, kind, value string) tea.Cmd {
	return func() tea.Msg {
		target, root := value, ""
		if kind == config.KindStatic {
			target, root = "", value
		}
		warnings, err := domain.Add(name, kind, target, root)
		return resultMsg{action: "added " + name, warnings: warnings, err: err}
	}
}

func removeCmd(name string) tea.Cmd {
	return func() tea.Msg {
		warnings, err := domain.Remove(name)
		return resultMsg{action: "removed " + name, warnings: warnings, err: err}
	}
}

func pauseCmd(name string, paused bool) tea.Cmd {
	return func() tea.Msg {
		warnings, err := domain.SetPaused(name, paused)
		action := "resumed " + name
		if paused {
			action = "paused " + name
		}
		return resultMsg{action: action, warnings: warnings, err: err}
	}
}

func startDaemonCmd() tea.Cmd {
	return func() tea.Msg {
		cfg, err := config.Load()
		if err != nil {
			return resultMsg{action: "start daemon", err: err}
		}
		err = daemon.StartInBackground(cfg)
		return resultMsg{action: "daemon started", err: err}
	}
}

func stopDaemonCmd(port int) tea.Cmd {
	return func() tea.Msg {
		err := daemon.NewClient(port).Stop()
		return resultMsg{action: "daemon stopped", err: err}
	}
}

func trustCmd() tea.Cmd {
	return func() tea.Msg {
		cfg, err := config.Load()
		if err != nil {
			return resultMsg{action: "trust", err: err}
		}
		err = trust.Ensure(cfg)
		return resultMsg{action: "trust", err: err}
	}
}

func setupCmd() tea.Cmd {
	return func() tea.Msg {
		cfg, err := config.Load()
		if err != nil {
			return resultMsg{action: "setup", err: err}
		}
		err = setup.Run(cfg)
		return resultMsg{action: "setup", err: err}
	}
}

func replayCmd(r requests.Request) tea.Cmd {
	return func() tea.Msg {
		code, err := requests.Replay(r)
		if err != nil {
			return resultMsg{action: fmt.Sprintf("replay %s %s", r.Method, r.URI), err: err}
		}
		return resultMsg{action: fmt.Sprintf("replayed %s %s → %d", r.Method, r.URI, code)}
	}
}
