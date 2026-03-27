package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"altclaw.ai/internal/agent"
	"altclaw.ai/internal/bridge"
	"altclaw.ai/internal/config"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ── Msg types ────────────────────────────────────────────────────────

// responseMsg signals the AI has finished responding.
type responseMsg struct {
	content string
	err     error
}

// streamChunkMsg carries a streaming chunk from the AI.
type streamChunkMsg struct {
	chunk string
}

// StreamChunk creates a streamChunkMsg for sending from outside the package.
func StreamChunk(chunk string) streamChunkMsg {
	return streamChunkMsg{chunk: chunk}
}

// logMsg carries a log message from bridge execution.
type logMsg struct {
	msg string
}

// tickMsg drives the spinner animation.
type tickMsg time.Time

// promptMsg asks the TUI to display an interactive prompt.
type promptMsg struct {
	question string
	respCh   chan string
}

// Spinner frames (Braille animation)
var spinnerFrames = []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}

// Model is the main Bubble Tea model for the Altclaw TUI.
type Model struct {
	agent  *agent.Agent
	width  int
	height int

	messages     []ChatMessage
	input        string
	cursorPos    int
	scrollOffset int
	thinking     bool
	streamBuf    string
	spinnerIdx   int    // current spinner frame index
	ephemeral    string // transient status text (not persisted in messages)

	// Interactive prompt state (for ui.ask / ui.confirm)
	promptActive   bool      // true when waiting for user input on a prompt
	promptQuestion string    // the question being asked
	promptRespCh   chan string // channel to send the answer back

	// Input history (up/down arrow)
	inputHistory []string
	historyIdx   int    // -1 = not browsing, 0..len-1 = browsing
	historySaved string // saved current input when entering history

	status StatusBar

	// Database access
	store *config.Store
	ws    *config.Workspace

	// Chat state
	chatID    int64
	chatTitle string

	// RebuildAgent rebuilds the agent with a different provider.
	// Returns the new agent or an error.
	RebuildAgent func(providerName string) (*agent.Agent, error)

	// For streaming
	program *tea.Program

	// Mid-execution message queue
	pendingMu   sync.Mutex
	pendingMsgs []string
}

// Ensure Model implements bridge.UIHandler
var _ bridge.UIHandler = (*Model)(nil)

// NewModel creates the TUI model.
func NewModel(ag *agent.Agent, providerLabel, providerName, modelName, workspace string, store *config.Store, ws *config.Workspace) *Model {
	m := &Model{
		agent: ag,
		store: store,
		ws:    ws,
		messages: []ChatMessage{
			{Role: "system", Content: "Welcome to Altclaw! Type a message or /help for commands."},
		},
		historyIdx: -1,
		status: StatusBar{
			ProviderLabel: providerLabel,
			ProviderName:  providerName,
			ModelName:     modelName,
			Workspace:     shortenPath(workspace),
		},
	}
	return m
}

// SetProgram sets the tea.Program reference for sending messages from goroutines.
func (m *Model) SetProgram(p *tea.Program) {
	m.program = p
}

// SetAgent sets the agent after construction (needed for circular dependency resolution).
func (m *Model) SetAgent(ag *agent.Agent) {
	m.agent = ag
}

// ResumeLatestChat loads the most recently modified chat from the store.
// Called during startup so the TUI picks up where the user left off.
func (m *Model) ResumeLatestChat() {
	if m.store == nil || m.ws == nil {
		return
	}
	chats, err := m.store.ListChats(context.Background(), m.ws.ID)
	if err != nil || len(chats) == 0 {
		return
	}
	latest := chats[0] // ListChats returns newest first (-modified)

	m.chatID = latest.ID
	m.chatTitle = latest.Title
	m.status.ChatTitle = latest.Title

	if m.agent != nil {
		m.agent.ChatID = latest.ID
		_ = m.agent.LoadMessages(context.Background())
	}

	// Rebuild display from agent's loaded messages
	m.messages = m.messages[:0]
	m.messages = append(m.messages, ChatMessage{Role: "system", Content: fmt.Sprintf("Resumed chat: %s", latest.Title)})
	if m.agent != nil {
		for _, msg := range m.agent.Messages {
			if msg.Role == "user" && !strings.HasPrefix(msg.Content, "[Execution Results]") {
				m.messages = append(m.messages, ChatMessage{Role: "user", Content: msg.Content})
			} else if msg.Role == "assistant" {
				m.messages = append(m.messages, ChatMessage{Role: "assistant", Content: msg.Content})
			}
		}
	}
	m.scrollOffset = 0
}

// DrainPendingMsgs returns and clears any messages queued during execution.
// This is wired to agent.PendingMsgs for mid-execution message injection.
func (m *Model) DrainPendingMsgs() []string {
	m.pendingMu.Lock()
	msgs := m.pendingMsgs
	m.pendingMsgs = nil
	m.pendingMu.Unlock()
	return msgs
}

// Log implements bridge.UIHandler.
func (m *Model) Log(msg string) {
	if m.program != nil {
		m.program.Send(logMsg{msg: msg})
	}
}

// Ask implements bridge.UIHandler.
// Blocks the calling goroutine until the user types an answer in the TUI.
func (m *Model) Ask(question string) string {
	if m.program == nil {
		return "yes"
	}
	respCh := make(chan string, 1)
	m.program.Send(promptMsg{question: "❓ " + question, respCh: respCh})
	return <-respCh
}

// Confirm implements bridge.UIHandler.
// Blocks the calling goroutine until the user approves or rejects.
func (m *Model) Confirm(action, label, summary string, params map[string]any) string {
	if m.program == nil {
		return "yes"
	}
	respCh := make(chan string, 1)
	prompt := fmt.Sprintf("🔐 %s: %s (yes/no)", label, summary)
	m.program.Send(promptMsg{question: prompt, respCh: respCh})
	return <-respCh
}

func (m *Model) Init() tea.Cmd {
	return func() tea.Msg {
		return tea.RequestWindowSize()
	}
}

// spinnerTick returns a tea.Cmd that fires a tickMsg after a short interval.
func spinnerTick() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		key := msg.Key()

		// If in prompt mode, intercept keys for the prompt
		if m.promptActive {
			switch msg.String() {
			case "ctrl+c":
				// Cancel prompt with "no"
				if m.promptRespCh != nil {
					m.promptRespCh <- "no"
				}
				m.promptActive = false
				m.status.Prompting = false
				m.promptQuestion = ""
				m.promptRespCh = nil
				m.input = ""
				m.cursorPos = 0
				return m, nil
			case "enter":
				answer := strings.TrimSpace(m.input)
				if answer == "" {
					answer = "yes" // default
				}
				m.messages = append(m.messages, ChatMessage{Role: "log", Content: m.promptQuestion + " → " + answer})
				if m.promptRespCh != nil {
					m.promptRespCh <- answer
				}
				m.promptActive = false
				m.status.Prompting = false
				m.promptQuestion = ""
				m.promptRespCh = nil
				m.input = ""
				m.cursorPos = 0
				m.scrollOffset = 0
				return m, nil
			case "backspace":
				if m.cursorPos > 0 {
					m.input = m.input[:m.cursorPos-1] + m.input[m.cursorPos:]
					m.cursorPos--
				}
			default:
				if key.Text != "" {
					m.input = m.input[:m.cursorPos] + key.Text + m.input[m.cursorPos:]
					m.cursorPos += len(key.Text)
				}
			}
			return m, nil
		}

		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit

		case "enter":
			if strings.TrimSpace(m.input) == "" {
				return m, nil
			}
			// If thinking, queue the message for mid-execution injection
			if m.thinking {
				queuedMsg := strings.TrimSpace(m.input)
				m.pendingMu.Lock()
				m.pendingMsgs = append(m.pendingMsgs, queuedMsg)
				m.pendingMu.Unlock()
				m.messages = append(m.messages, ChatMessage{Role: "user", Content: "(queued) " + queuedMsg})
				m.input = ""
				m.cursorPos = 0
				m.scrollOffset = 0
				return m, nil
			}
			return m, m.handleSubmit()

		case "backspace":
			if m.cursorPos > 0 {
				m.input = m.input[:m.cursorPos-1] + m.input[m.cursorPos:]
				m.cursorPos--
			}

		case "left":
			if m.cursorPos > 0 {
				m.cursorPos--
			}

		case "right":
			if m.cursorPos < len(m.input) {
				m.cursorPos++
			}

		case "home", "ctrl+a":
			m.cursorPos = 0

		case "end", "ctrl+e":
			m.cursorPos = len(m.input)

		case "ctrl+u":
			m.input = ""
			m.cursorPos = 0

		case "ctrl+l":
			m.messages = m.messages[:0]
			m.messages = append(m.messages, ChatMessage{Role: "system", Content: "Chat cleared."})
			if m.agent != nil {
				m.agent.Reset()
			}
			m.scrollOffset = 0

		case "pgup":
			m.scrollOffset += 5
		case "pgdown":
			m.scrollOffset -= 5
			if m.scrollOffset < 0 {
				m.scrollOffset = 0
			}

		case "up":
			if len(m.inputHistory) > 0 {
				if m.historyIdx == -1 {
					// Entering history: save current input
					m.historySaved = m.input
					m.historyIdx = len(m.inputHistory) - 1
				} else if m.historyIdx > 0 {
					m.historyIdx--
				}
				m.input = m.inputHistory[m.historyIdx]
				m.cursorPos = len(m.input)
			}
		case "down":
			if m.historyIdx >= 0 {
				if m.historyIdx < len(m.inputHistory)-1 {
					m.historyIdx++
					m.input = m.inputHistory[m.historyIdx]
				} else {
					// Back to current input
					m.historyIdx = -1
					m.input = m.historySaved
				}
				m.cursorPos = len(m.input)
			}

		default:
			// Handle printable character input via Key.Text
			if key.Text != "" {
				m.input = m.input[:m.cursorPos] + key.Text + m.input[m.cursorPos:]
				m.cursorPos += len(key.Text)
			}
		}
		return m, nil

	case tea.PasteMsg:
		// Handle pasted text — insert at cursor, strip newlines
		pasted := strings.ReplaceAll(msg.String(), "\n", " ")
		pasted = strings.ReplaceAll(pasted, "\r", "")
		m.input = m.input[:m.cursorPos] + pasted + m.input[m.cursorPos:]
		m.cursorPos += len(pasted)
		return m, nil

	case tickMsg:
		if !m.thinking {
			return m, nil
		}
		m.spinnerIdx = (m.spinnerIdx + 1) % len(spinnerFrames)
		m.status.SpinnerFrame = spinnerFrames[m.spinnerIdx]
		return m, spinnerTick()

	case streamChunkMsg:
		m.streamBuf += msg.chunk
		// Auto-scroll to bottom when new streaming content arrives
		m.scrollOffset = 0
		return m, nil

	case promptMsg:
		// Enter interactive prompt mode
		m.promptActive = true
		m.status.Prompting = true
		m.promptQuestion = msg.question
		m.promptRespCh = msg.respCh
		m.input = ""
		m.cursorPos = 0
		m.messages = append(m.messages, ChatMessage{Role: "system", Content: msg.question})
		m.scrollOffset = 0
		return m, nil

	case logMsg:
		// Filter out agent iteration noise — show as ephemeral status instead of permanent entries.
		// These messages are useful context but shouldn't clutter the chat history.
		lower := strings.ToLower(msg.msg)
		if strings.Contains(lower, "waiting for ai response") ||
			strings.Contains(lower, "checking system") ||
			strings.Contains(lower, "reading") && strings.Contains(lower, "documentation") {
			m.ephemeral = msg.msg
			return m, nil
		}
		m.messages = append(m.messages, ChatMessage{Role: "log", Content: msg.msg})
		// Auto-scroll
		m.scrollOffset = 0
		return m, nil

	case responseMsg:
		m.thinking = false
		m.status.Thinking = false
		m.ephemeral = ""
		if msg.err != nil {
			m.messages = append(m.messages, ChatMessage{Role: "error", Content: msg.err.Error()})
		} else {
			m.messages = append(m.messages, ChatMessage{Role: "assistant", Content: msg.content})
		}
		m.streamBuf = ""
		m.scrollOffset = 0
		return m, nil
	}

	return m, nil
}

func (m *Model) handleSubmit() tea.Cmd {
	input := strings.TrimSpace(m.input)
	m.input = ""
	m.cursorPos = 0

	// Push to input history (cap at 20)
	m.inputHistory = append(m.inputHistory, input)
	if len(m.inputHistory) > 20 {
		m.inputHistory = m.inputHistory[len(m.inputHistory)-20:]
	}
	m.historyIdx = -1
	m.historySaved = ""

	// Handle commands
	if strings.HasPrefix(input, "/") {
		return m.handleCommand(input)
	}

	// Auto-create chat on first message if no active chat
	if m.chatID == 0 && m.store != nil {
		title := input
		if len(title) > 50 {
			title = title[:50] + "..."
		}
		chat, err := m.store.CreateChat(context.Background(), m.ws.ID, title, m.status.ProviderName)
		if err == nil {
			m.chatID = chat.ID
			m.chatTitle = chat.Title
			m.status.ChatTitle = m.chatTitle
			if m.agent != nil {
				m.agent.ChatID = chat.ID
			}
		}
	}

	// Add user message
	m.messages = append(m.messages, ChatMessage{Role: "user", Content: input})
	m.thinking = true
	m.status.Thinking = true
	m.streamBuf = ""
	m.ephemeral = ""
	m.scrollOffset = 0

	// Send to agent in background, start spinner
	return tea.Batch(
		func() tea.Msg {
			ctx := context.Background()
			resp, err := m.agent.Send(ctx, input)
			return responseMsg{content: resp, err: err}
		},
		spinnerTick(),
	)
}

func (m *Model) handleCommand(input string) tea.Cmd {
	parts := strings.Fields(input)
	cmd := parts[0]
	arg := ""
	if len(parts) > 1 {
		arg = strings.Join(parts[1:], " ")
	}

	switch cmd {
	case "/help":
		m.messages = append(m.messages, ChatMessage{
			Role:    "system",
			Content: "Commands:\n  /help               Show this help\n  /clear              Clear current chat display\n  /provider [name]    List providers or switch to named provider\n  /chats              List chat sessions\n  /new                Start a new chat\n  /open <id>          Switch to an existing chat\n  /delete <id>        Delete a chat session\n  /rename <title>     Rename current chat\n  /quit               Exit Altclaw",
		})
	case "/clear":
		if m.agent != nil {
			m.agent.Reset()
		}
		m.messages = m.messages[:0]
		m.messages = append(m.messages, ChatMessage{Role: "system", Content: "Chat cleared."})
		m.scrollOffset = 0
	case "/quit":
		return tea.Quit
	case "/provider":
		m.handleProviderCommand(arg)
	case "/chats":
		m.handleChatsCommand()
	case "/new":
		m.handleNewCommand()
	case "/open":
		m.handleOpenCommand(arg)
	case "/delete":
		m.handleDeleteCommand(arg)
	case "/rename":
		m.handleRenameCommand(arg)
	default:
		m.messages = append(m.messages, ChatMessage{
			Role:    "error",
			Content: fmt.Sprintf("Unknown command: %s. Type /help for available commands.", cmd),
		})
	}
	return nil
}

// handleProviderCommand lists or switches providers.
func (m *Model) handleProviderCommand(name string) {
	if m.store == nil {
		m.messages = append(m.messages, ChatMessage{Role: "error", Content: "Store not available"})
		return
	}

	providers, err := m.store.ListProviders()
	if err != nil {
		m.messages = append(m.messages, ChatMessage{Role: "error", Content: "Failed to list providers: " + err.Error()})
		return
	}

	// No argument: list providers
	if name == "" {
		var sb strings.Builder
		sb.WriteString("Available providers:")
		for _, p := range providers {
			marker := "  "
			if p.Name == m.status.ProviderLabel {
				marker = "▸ "
			}
			sb.WriteString(fmt.Sprintf("\n%s%s (%s/%s)", marker, p.Name, p.ProviderType, p.Model))
		}
		sb.WriteString("\n\nUse /provider <name> to switch.")
		m.messages = append(m.messages, ChatMessage{Role: "system", Content: sb.String()})
		return
	}

	// Validate provider exists
	prov, err := m.store.GetProvider(name)
	if err != nil || prov == nil {
		m.messages = append(m.messages, ChatMessage{Role: "error", Content: fmt.Sprintf("Provider %q not found. Use /provider to list available providers.", name)})
		return
	}

	// Switch provider
	if m.RebuildAgent == nil {
		m.messages = append(m.messages, ChatMessage{Role: "error", Content: "Provider switching not available"})
		return
	}

	newAg, err := m.RebuildAgent(name)
	if err != nil {
		m.messages = append(m.messages, ChatMessage{Role: "error", Content: "Failed to switch provider: " + err.Error()})
		return
	}

	m.agent = newAg
	// Restore chat ID on the new agent
	if m.chatID != 0 {
		m.agent.ChatID = m.chatID
		_ = m.agent.LoadMessages(context.Background())
	}

	// Update status bar
	m.status.ProviderLabel = prov.Name
	m.status.ProviderName = prov.ProviderType
	m.status.ModelName = prov.Model

	// Save last provider to workspace
	_ = m.store.SaveWorkspace(context.Background(), func(w *config.Workspace) error {
		w.LastProvider = name
		return nil
	})

	m.messages = append(m.messages, ChatMessage{Role: "system", Content: fmt.Sprintf("Switched to provider: %s", name)})
}

// handleChatsCommand lists all chat sessions.
func (m *Model) handleChatsCommand() {
	if m.store == nil {
		m.messages = append(m.messages, ChatMessage{Role: "error", Content: "Store not available"})
		return
	}

	chats, err := m.store.ListChats(context.Background(), m.ws.ID)
	if err != nil {
		m.messages = append(m.messages, ChatMessage{Role: "error", Content: "Failed to list chats: " + err.Error()})
		return
	}

	if len(chats) == 0 {
		m.messages = append(m.messages, ChatMessage{Role: "system", Content: "No chats yet. Start typing to create one."})
		return
	}

	var sb strings.Builder
	sb.WriteString("Chat sessions:")
	for _, c := range chats {
		marker := "  "
		if c.ID == m.chatID {
			marker = "▸ "
		}
		title := c.Title
		if title == "" {
			title = "(untitled)"
		}
		sb.WriteString(fmt.Sprintf("\n%s#%d  %s  [%s]", marker, c.ID, title, c.UpdatedAt.Format("Jan 02 15:04")))
	}
	sb.WriteString("\n\nUse /open <id> to switch, /delete <id> to remove.")
	m.messages = append(m.messages, ChatMessage{Role: "system", Content: sb.String()})
}

// handleNewCommand creates a new chat session.
func (m *Model) handleNewCommand() {
	if m.agent != nil {
		m.agent.Reset()
	}
	m.chatID = 0
	m.chatTitle = ""
	m.status.ChatTitle = ""
	m.messages = m.messages[:0]
	m.messages = append(m.messages, ChatMessage{Role: "system", Content: "New chat started. Type a message to begin."})
	m.scrollOffset = 0
	if m.agent != nil {
		m.agent.ChatID = 0
	}
}

// handleOpenCommand switches to an existing chat.
func (m *Model) handleOpenCommand(arg string) {
	if m.store == nil {
		m.messages = append(m.messages, ChatMessage{Role: "error", Content: "Store not available"})
		return
	}

	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil || id == 0 {
		m.messages = append(m.messages, ChatMessage{Role: "error", Content: "Usage: /open <chat_id>"})
		return
	}

	chat, err := m.store.GetChat(context.Background(), m.ws.ID, id)
	if err != nil {
		m.messages = append(m.messages, ChatMessage{Role: "error", Content: fmt.Sprintf("Chat #%d not found", id)})
		return
	}

	// Reset agent and load messages
	if m.agent != nil {
		m.agent.Reset()
		m.agent.ChatID = chat.ID
		_ = m.agent.LoadMessages(context.Background())
	}

	m.chatID = chat.ID
	m.chatTitle = chat.Title
	m.status.ChatTitle = chat.Title

	// Rebuild display messages from agent's loaded messages
	m.messages = m.messages[:0]
	m.messages = append(m.messages, ChatMessage{Role: "system", Content: fmt.Sprintf("Opened chat #%d: %s", chat.ID, chat.Title)})
	if m.agent != nil {
		for _, msg := range m.agent.Messages {
			if msg.Role == "user" && !strings.HasPrefix(msg.Content, "[Execution Results]") {
				m.messages = append(m.messages, ChatMessage{Role: "user", Content: msg.Content})
			} else if msg.Role == "assistant" {
				m.messages = append(m.messages, ChatMessage{Role: "assistant", Content: msg.Content})
			}
		}
	}
	m.scrollOffset = 0
}

// handleDeleteCommand deletes a chat session.
func (m *Model) handleDeleteCommand(arg string) {
	if m.store == nil {
		m.messages = append(m.messages, ChatMessage{Role: "error", Content: "Store not available"})
		return
	}

	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil || id == 0 {
		m.messages = append(m.messages, ChatMessage{Role: "error", Content: "Usage: /delete <chat_id>"})
		return
	}

	chat := &config.Chat{ID: id, Workspace: m.ws.ID}
	if err := m.store.DeleteChat(context.Background(), chat); err != nil {
		m.messages = append(m.messages, ChatMessage{Role: "error", Content: "Failed to delete chat: " + err.Error()})
		return
	}

	// If deleting the current chat, reset
	if id == m.chatID {
		m.handleNewCommand()
	}
	m.messages = append(m.messages, ChatMessage{Role: "system", Content: fmt.Sprintf("Chat #%d deleted.", id)})
}

// handleRenameCommand renames the current chat.
func (m *Model) handleRenameCommand(title string) {
	if m.store == nil {
		m.messages = append(m.messages, ChatMessage{Role: "error", Content: "Store not available"})
		return
	}
	if m.chatID == 0 {
		m.messages = append(m.messages, ChatMessage{Role: "error", Content: "No active chat to rename. Start a chat first."})
		return
	}
	if title == "" {
		m.messages = append(m.messages, ChatMessage{Role: "error", Content: "Usage: /rename <new title>"})
		return
	}

	chat, err := m.store.GetChat(context.Background(), m.ws.ID, m.chatID)
	if err != nil {
		m.messages = append(m.messages, ChatMessage{Role: "error", Content: "Failed to get chat: " + err.Error()})
		return
	}
	chat.Title = title
	if err := m.store.UpdateChat(context.Background(), chat); err != nil {
		m.messages = append(m.messages, ChatMessage{Role: "error", Content: "Failed to rename: " + err.Error()})
		return
	}

	m.chatTitle = title
	m.status.ChatTitle = title
	m.messages = append(m.messages, ChatMessage{Role: "system", Content: fmt.Sprintf("Chat renamed to: %s", title)})
}

func (m *Model) View() tea.View {
	if m.width == 0 {
		return tea.NewView("Loading...")
	}

	// Header
	header := HeaderStyle.Width(m.width).Render("  🐾 ALTCLAW  — AI Agent Orchestrator")

	// Status bar
	statusBar := m.status.Render(m.width)

	// Input area
	inputDisplay := m.input
	if m.cursorPos <= len(inputDisplay) {
		// Show cursor
		before := inputDisplay[:m.cursorPos]
		after := ""
		if m.cursorPos < len(inputDisplay) {
			after = inputDisplay[m.cursorPos:]
		}
		cursorBg := accentColor
		if m.promptActive {
			cursorBg = warningColor
		}
		cursor := lipgloss.NewStyle().
			Background(cursorBg).
			Foreground(lipgloss.Color("#000000")).
			Render(" ")
		inputDisplay = before + cursor + after
	}

	var inputBox string
	var help string
	if m.promptActive {
		// Prompt mode: distinct look
		prompt := lipgloss.NewStyle().Foreground(warningColor).Bold(true).Render("? ")
		inputLine := prompt + inputDisplay
		inputBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(warningColor).
			Padding(0, 1).
			Width(m.width - 4).
			Render(inputLine)
		help = HelpStyle.Width(m.width).Render("  enter: submit answer (empty = yes) • ctrl+c: cancel (no)")
	} else {
		prompt := lipgloss.NewStyle().Foreground(accentColor).Bold(true).Render("❯ ")
		inputLine := prompt + inputDisplay
		inputBox = InputBorderStyle.Width(m.width - 4).Render(inputLine)
		help = HelpStyle.Width(m.width).Render("  enter: send • /help: commands • /provider: switch • /chats: list • /new: new chat • ctrl+c: quit")
	}

	// Chat area — reserve space for thinking line when spinner is active
	headerH := lipgloss.Height(header)
	statusH := lipgloss.Height(statusBar)
	inputH := lipgloss.Height(inputBox)
	helpH := lipgloss.Height(help)
	thinkingH := 0
	if m.thinking && !m.promptActive && m.streamBuf == "" {
		thinkingH = 1
	}
	chatH := m.height - headerH - statusH - inputH - helpH - thinkingH - 1

	if chatH < 1 {
		chatH = 1
	}

	// Render messages
	var chatContent strings.Builder
	for _, msg := range m.messages {
		chatContent.WriteString(RenderMessage(msg, m.width))
	}
	// Show streaming buffer (replaces thinking indicator when content arrives)
	if m.streamBuf != "" {
		badge := AssistantBadge.Render(" ALTCLAW ")
		body := AssistantBodyStyle.Width(m.width - 6).Render(m.streamBuf + "▊")
		chatContent.WriteString(fmt.Sprintf("\n%s\n%s\n", badge, body))
	}

	// Simple scroll: take last N lines
	chatLines := strings.Split(chatContent.String(), "\n")
	start := len(chatLines) - chatH - m.scrollOffset
	if start < 0 {
		start = 0
	}
	end := start + chatH
	if end > len(chatLines) {
		end = len(chatLines)
	}
	visibleChat := strings.Join(chatLines[start:end], "\n")

	chatBox := lipgloss.NewStyle().
		Width(m.width).
		Height(chatH).
		Render(visibleChat)

	// Thinking indicator — rendered OUTSIDE the scrollable chat as a fixed line
	// so it doesn't pollute the scroll buffer with stale spinner frames.
	thinkingLine := ""
	if m.thinking && !m.promptActive && m.streamBuf == "" {
		frame := spinnerFrames[m.spinnerIdx]
		spinText := "thinking..."
		if m.ephemeral != "" {
			// Strip emoji prefix for cleaner display
			spinText = strings.TrimSpace(m.ephemeral)
			if idx := strings.Index(spinText, "] "); idx >= 0 {
				spinText = spinText[idx+2:]
			}
		}
		thinkingLine = SpinnerStyle.Width(m.width).Render("  " + frame + " " + spinText)
	}

	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Top,
		header,
		chatBox,
		thinkingLine,
		inputBox,
		help,
		statusBar,
	))
	v.AltScreen = true
	return v
}

func shortenPath(path string) string {
	if len(path) > 30 {
		return "..." + path[len(path)-27:]
	}
	return path
}
