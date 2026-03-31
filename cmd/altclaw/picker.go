package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"altclaw.ai/internal/config"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// pickerModel is a Bubble Tea model for selecting a workspace folder.
type pickerModel struct {
	store    *config.Store
	current  string   // current directory path
	entries  []string // directory names in current
	drives   []string // available drives (Windows only)
	recentWS []*config.Workspace // recent workspaces from store
	cursor   int      // selected index
	width    int
	height   int
	typing   bool   // manual path input mode
	tuiMode  bool   // terminal UI mode toggle
	input    string // typed path
	err      string // error message
	selected string // final result (set on confirm)
	quit     bool
}

func newPickerModel(store *config.Store) *pickerModel {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/"
		if runtime.GOOS == "windows" {
			home = "C:\\"
		}
	}
	m := &pickerModel{store: store, current: home}
	m.loadEntries()
	m.loadRecentWorkspaces()
	return m
}

func (m *pickerModel) loadRecentWorkspaces() {
	m.recentWS = nil
	if m.store != nil {
		if wsList, err := m.store.ListWorkspaces(context.Background()); err == nil {
			for _, ws := range wsList {
				if ws != nil && ws.Path != "" {
					m.recentWS = append(m.recentWS, ws)
				}
			}
		}
	}
}

func (m *pickerModel) loadEntries() {
	m.entries = nil
	m.err = ""
	dirEntries, err := os.ReadDir(m.current)
	if err != nil {
		m.err = err.Error()
		return
	}
	for _, e := range dirEntries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			m.entries = append(m.entries, e.Name())
		}
	}
	sort.Strings(m.entries)
}

func (m *pickerModel) Init() tea.Cmd {
	return func() tea.Msg { return tea.RequestWindowSize() }
}

// specialCount returns the number of special items at the top of the list.
// 0: Use this folder, 1: TUI mode toggle, 2: parent dir, 3: type path
const specialItems = 4

func (m *pickerModel) totalItems() int {
	return specialItems + len(m.recentWS) + len(m.entries)
}

func (m *pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		key := msg.Key()

		// Manual path input mode
		if m.typing {
			switch msg.String() {
			case "enter":
				path := strings.TrimSpace(m.input)
				if path == "" {
					m.typing = false
					return m, nil
				}
				// Expand ~ to home dir
				if strings.HasPrefix(path, "~") {
					if home, err := os.UserHomeDir(); err == nil {
						path = filepath.Join(home, path[1:])
					}
				}
				if !filepath.IsAbs(path) {
					path = filepath.Join(m.current, path)
				}
				info, err := os.Stat(path)
				if err != nil || !info.IsDir() {
					m.err = fmt.Sprintf("Not a valid directory: %s", path)
					return m, nil
				}
				m.selected = path
				return m, tea.Quit
			case "esc":
				m.typing = false
				m.input = ""
				return m, nil
			case "backspace":
				if len(m.input) > 0 {
					m.input = m.input[:len(m.input)-1]
				}
			default:
				if key.Text != "" {
					m.input += key.Text
				}
			}
			return m, nil
		}

		// Browse mode
		switch msg.String() {
		case "ctrl+c", "q":
			m.quit = true
			return m, tea.Quit

		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			var maxIdx int
			if len(m.drives) > 0 {
				maxIdx = len(m.drives) - 1
			} else {
				maxIdx = m.totalItems() - 1
			}
			if m.cursor < maxIdx {
				m.cursor++
			}

		case " ":
			// Space toggles TUI mode when on the checkbox item
			if len(m.drives) == 0 && m.cursor == 1 {
				m.tuiMode = !m.tuiMode
			}

		case "delete", "x":
			// Delete recent workspace
			if len(m.drives) == 0 {
				idx := m.cursor - specialItems
				if idx >= 0 && idx < len(m.recentWS) {
					ws := m.recentWS[idx]
					if m.store != nil {
						_ = m.store.DeleteWorkspace(context.Background(), ws.ID)
					}
					m.loadRecentWorkspaces()
					// Adjust cursor if needed
					if m.cursor >= m.totalItems() && m.cursor > 0 {
						m.cursor--
					}
				}
			}

		case "enter":
			// Drive selection mode
			if len(m.drives) > 0 {
				if m.cursor >= 0 && m.cursor < len(m.drives) {
					m.current = m.drives[m.cursor]
					m.drives = nil
					m.cursor = 0
					m.loadEntries()
				}
				return m, nil
			}
			switch {
			case m.cursor == 0:
				// "Use this folder"
				m.selected = m.current
				return m, tea.Quit
			case m.cursor == 1:
				// Toggle TUI mode
				m.tuiMode = !m.tuiMode
			case m.cursor == 2:
				// Go to parent
				m.navigateUp()
			case m.cursor == 3:
				// Type path manually
				m.typing = true
				m.input = m.current
				m.err = ""
			default:
				// Recent workspace or directory entry
				idx := m.cursor - specialItems
				if idx < len(m.recentWS) {
					// Select recent workspace directly
					m.selected = m.recentWS[idx].Path
					return m, tea.Quit
				}
				// Directory entry
				dirIdx := idx - len(m.recentWS)
				if dirIdx >= 0 && dirIdx < len(m.entries) {
					m.current = filepath.Join(m.current, m.entries[dirIdx])
					m.cursor = 0
					m.loadEntries()
				}
			}

		case "backspace", "left", "h":
			if len(m.drives) > 0 {
				// Already at drive list, nothing to go up to
				return m, nil
			}
			m.navigateUp()

		case "right", "l":
			// Navigate into selected directory or drive
			if len(m.drives) > 0 {
				if m.cursor >= 0 && m.cursor < len(m.drives) {
					m.current = m.drives[m.cursor]
					m.drives = nil
					m.cursor = 0
					m.loadEntries()
				}
				return m, nil
			}
			idx := m.cursor - specialItems
			if idx < len(m.recentWS) {
				// Recent workspace: select directly
				m.selected = m.recentWS[idx].Path
				return m, tea.Quit
			}
			dirIdx := idx - len(m.recentWS)
			if dirIdx >= 0 && dirIdx < len(m.entries) {
				m.current = filepath.Join(m.current, m.entries[dirIdx])
				m.cursor = 0
				m.loadEntries()
			}
		}
		return m, nil
	}
	return m, nil
}

var (
	pickerHeader = lipgloss.NewStyle().
			Background(lipgloss.Color("#7C3AED")).
			Foreground(lipgloss.Color("#FFFFFF")).
			Bold(true).
			Padding(0, 2)

	pickerPath = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#06B6D4")).
			Bold(true).
			PaddingLeft(1)

	pickerSelected = lipgloss.NewStyle().
			Background(lipgloss.Color("#7C3AED")).
			Foreground(lipgloss.Color("#FFFFFF")).
			Bold(true)

	pickerItem = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E2E8F0")).
			PaddingLeft(2)

	pickerAction = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#10B981")).
			Bold(true).
			PaddingLeft(2)

	pickerDim = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#64748B")).
			Italic(true)

	pickerErr = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#EF4444")).
			PaddingLeft(2)

	pickerInputStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#4C3A8E")).
				Padding(0, 1)
)

// navigateUp goes to the parent directory, or shows the drive list on Windows at a root.
func (m *pickerModel) navigateUp() {
	parent := filepath.Dir(m.current)
	if parent != m.current {
		m.current = parent
		m.cursor = 0
		m.loadEntries()
		return
	}
	// At filesystem root — show drive list if available (Windows)
	drives := listDrives()
	if len(drives) > 0 {
		m.drives = drives
		m.cursor = 0
	}
}

func (m *pickerModel) View() tea.View {
	if m.width == 0 {
		return tea.NewView("Loading...")
	}

	var b strings.Builder

	// Header
	b.WriteString(pickerHeader.Width(m.width).Render("  🐾 Select Altclaw Workspace"))
	b.WriteString("\n\n")

	// Manual input mode
	if m.typing {
		b.WriteString(pickerDim.Render("  Type the full path to your workspace:"))
		b.WriteString("\n\n")

		cursor := lipgloss.NewStyle().
			Background(lipgloss.Color("#06B6D4")).
			Foreground(lipgloss.Color("#000000")).
			Render(" ")
		inputLine := "  ❯ " + m.input + cursor
		b.WriteString(pickerInputStyle.Width(m.width - 4).Render(inputLine))
		b.WriteString("\n\n")

		if m.err != "" {
			b.WriteString(pickerErr.Render("  ✗ " + m.err))
			b.WriteString("\n\n")
		}

		b.WriteString(pickerDim.Render("  enter: confirm • esc: back"))
		b.WriteString("\n")

		v := tea.NewView(b.String())
		v.AltScreen = true
		return v
	}

	// Drive selection mode (Windows)
	if len(m.drives) > 0 {
		b.WriteString(pickerPath.Render("💾 Select a drive"))
		b.WriteString("\n\n")

		for i, d := range m.drives {
			line := "💾 " + d
			if m.cursor == i {
				line = pickerSelected.Render(" ▸ " + line + " ")
			} else {
				line = pickerItem.Render(line)
			}
			b.WriteString(line)
			b.WriteString("\n")
		}

		b.WriteString("\n")
		b.WriteString(pickerDim.Render("  ↑↓/jk: navigate • enter/→: select drive • q: quit"))
		b.WriteString("\n")

		v := tea.NewView(b.String())
		v.AltScreen = true
		return v
	}

	// Current path
	b.WriteString(pickerPath.Render("📂 " + m.current))
	b.WriteString("\n\n")

	if m.err != "" {
		b.WriteString(pickerErr.Render("  ✗ " + m.err))
		b.WriteString("\n")
	}

	// Calculate visible window
	itemCount := m.totalItems()
	listHeight := m.height - 10
	if listHeight < 5 {
		listHeight = 5
	}

	start := 0
	if m.cursor >= listHeight {
		start = m.cursor - listHeight + 1
	}
	end := start + listHeight
	if end > itemCount {
		end = itemCount
	}

	// Render items
	for i := start; i < end; i++ {
		var line string
		switch {
		case i == 0:
			line = "✓ Use this folder"
			if m.cursor == i {
				line = pickerSelected.Render(" ▸ " + line + " ")
			} else {
				line = pickerAction.Render("  " + line)
			}
		case i == 1:
			// TUI mode checkbox
			check := "[ ]"
			if m.tuiMode {
				check = "[✓]"
			}
			line = check + " Terminal UI mode (--tui)"
			if m.cursor == i {
				line = pickerSelected.Render(" ▸ " + line + " ")
			} else {
				line = pickerDim.Render("  " + line)
			}
		case i == 2:
			line = "⬆ .. (parent directory)"
			if m.cursor == i {
				line = pickerSelected.Render(" ▸ " + line + " ")
			} else {
				line = pickerDim.Render("  " + line)
			}
		case i == 3:
			line = "⌨ Type a path..."
			if m.cursor == i {
				line = pickerSelected.Render(" ▸ " + line + " ")
			} else {
				line = pickerDim.Render("  " + line)
			}
		default:
			idx := i - specialItems
			if idx < len(m.recentWS) {
				// Recent workspace
				ws := m.recentWS[idx]
				label := ws.Path
				if ws.Name != "" && ws.Name != filepath.Base(ws.Path) {
					label = ws.Name + " — " + ws.Path
				}
				line = "🕐 " + label
				if m.cursor == i {
					line = pickerSelected.Render(" ▸ " + line + " ")
				} else {
					line = pickerAction.Render("  " + line)
				}
			} else {
				// Directory entry
				dirIdx := idx - len(m.recentWS)
				name := m.entries[dirIdx]
				line = "📁 " + name
				if m.cursor == i {
					line = pickerSelected.Render(" ▸ " + line + " ")
				} else {
					line = pickerItem.Render(line)
				}
			}
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	// Scroll indicator
	if itemCount > listHeight {
		b.WriteString(pickerDim.Render(fmt.Sprintf("\n  %d/%d items", m.cursor+1, itemCount)))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(pickerDim.Render("  ↑↓/jk: navigate • enter/→: open • space: toggle TUI • del/x: remove • q: quit"))
	b.WriteString("\n")

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

// pickWorkspaceFolder runs the TUI folder picker and returns the selected path and TUI mode choice.
func pickWorkspaceFolder(store *config.Store) (string, bool, error) {
	m := newPickerModel(store)
	p := tea.NewProgram(m)
	finalModel, err := p.Run()
	if err != nil {
		return "", false, fmt.Errorf("folder picker error: %w", err)
	}
	result := finalModel.(*pickerModel)
	if result.quit || result.selected == "" {
		return "", false, fmt.Errorf("no folder selected")
	}
	return result.selected, result.tuiMode, nil
}
