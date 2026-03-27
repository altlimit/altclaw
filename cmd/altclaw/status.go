package main

import (
	"bytes"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// tuiLogWriter adapts slog output into TUI log messages.
type tuiLogWriter struct {
	model *statusModel
}

func (w *tuiLogWriter) Write(p []byte) (n int, err error) {
	lines := bytes.Split(bytes.TrimRight(p, "\n"), []byte("\n"))
	for _, line := range lines {
		if len(line) > 0 {
			w.model.sendLog(string(line))
		}
	}
	return len(p), nil
}

// serverLog carries a log line into the TUI.
type serverLog struct {
	msg string
}

// serverReady signals the server has started with its address.
type serverReady struct {
	addr      string
	password  string
	workspace string
}

// statusModel is a Bubble Tea model that shows a nice server status screen.
type statusModel struct {
	width     int
	height    int
	addr      string
	password  string
	workspace string
	logs      []string
	ready     bool
	program   *tea.Program
	mu        sync.Mutex
}

func (m *statusModel) Init() tea.Cmd {
	return func() tea.Msg { return tea.RequestWindowSize() }
}

func (m *statusModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		}
	case serverReady:
		m.addr = msg.addr
		m.password = msg.password
		m.workspace = msg.workspace
		m.ready = true
	case serverLog:
		m.mu.Lock()
		m.logs = append(m.logs, msg.msg)
		// Keep last 100 lines
		if len(m.logs) > 100 {
			m.logs = m.logs[len(m.logs)-100:]
		}
		m.mu.Unlock()
	}
	return m, nil
}

var (
	statusHeader = lipgloss.NewStyle().
			Background(lipgloss.Color("#7C3AED")).
			Foreground(lipgloss.Color("#FFFFFF")).
			Bold(true).
			Padding(0, 2)

	statusLabel = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#64748B")).
			Bold(true)

	statusValue = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#06B6D4")).
			Bold(true)

	statusSuccess = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#10B981")).
			Bold(true)

	statusBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#4C3A8E")).
			Padding(1, 2)

	statusDim = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#64748B")).
			Italic(true)

	statusLogLine = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#94A3B8"))
)

func (m *statusModel) View() tea.View {
	if m.width == 0 {
		return tea.NewView("Starting...")
	}

	var b strings.Builder

	// Header
	b.WriteString(statusHeader.Width(m.width).Render("  🐾 ALTCLAW  — AI Agent Orchestrator"))
	b.WriteString("\n\n")

	if !m.ready {
		b.WriteString("  ⣾ Starting server...\n")
		v := tea.NewView(b.String())
		v.AltScreen = true
		return v
	}

	// Server info box
	url := "http://localhost" + m.addr
	var info strings.Builder
	info.WriteString(statusSuccess.Render("  ● Server running") + "\n\n")
	info.WriteString(statusLabel.Render("   URL:       ") + statusValue.Render(url) + "\n")
	info.WriteString(statusLabel.Render("   Password:  ") + statusValue.Render(m.password) + "\n")
	info.WriteString(statusLabel.Render("   Workspace: ") + statusValue.Render(m.workspace) + "\n")

	boxWidth := m.width - 4
	if boxWidth > 70 {
		boxWidth = 70
	}
	b.WriteString(statusBox.Width(boxWidth).Render(info.String()))
	b.WriteString("\n\n")
	b.WriteString(statusDim.Render("  Browser opened automatically. Press q to stop the server."))
	b.WriteString("\n\n")

	// Log area
	m.mu.Lock()
	logs := make([]string, len(m.logs))
	copy(logs, m.logs)
	m.mu.Unlock()

	logHeight := m.height - 14
	if logHeight < 3 {
		logHeight = 3
	}
	if len(logs) > 0 {
		b.WriteString(statusDim.Render("  ─── Recent Activity ───"))
		b.WriteString("\n")
		start := 0
		if len(logs) > logHeight {
			start = len(logs) - logHeight
		}
		for _, line := range logs[start:] {
			// Truncate long lines
			if len(line) > m.width-4 {
				line = line[:m.width-7] + "..."
			}
			b.WriteString(statusLogLine.Render("  " + line))
			b.WriteString("\n")
		}
	}

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

// sendLog pushes a log message into the TUI from any goroutine.
func (m *statusModel) sendLog(msg string) {
	if m.program != nil {
		m.program.Send(serverLog{msg: msg})
	}
}
