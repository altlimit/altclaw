package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
)

// ChatMessage represents a single message in the chat history.
type ChatMessage struct {
	Role    string // "user", "assistant", "system", "error", "log"
	Content string
}

// renderMarkdown renders markdown content using Glamour with the dark theme.
func renderMarkdown(content string, width int) string {
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return content
	}
	rendered, err := r.Render(content)
	if err != nil {
		return content
	}
	// Glamour adds trailing newlines; trim for consistent spacing
	return strings.TrimRight(rendered, "\n")
}

// RenderMessage renders a chat message with appropriate styling.
// width constrains text wrapping to prevent overflow.
func RenderMessage(msg ChatMessage, width int) string {
	bodyWidth := width - 6 // account for padding
	if bodyWidth < 20 {
		bodyWidth = 20
	}

	// Thin separator for conversation turns
	sep := SeparatorStyle.Width(width - 4).Render(strings.Repeat("─", min(width-4, 60)))

	switch msg.Role {
	case "user":
		badge := UserBadge.Render(" YOU ")
		body := UserBodyStyle.Width(bodyWidth).Render(msg.Content)
		return fmt.Sprintf("\n%s\n%s\n%s\n", sep, badge, body)

	case "assistant":
		badge := AssistantBadge.Render(" ALTCLAW ")
		body := renderMarkdown(msg.Content, bodyWidth)
		return fmt.Sprintf("\n%s\n%s\n%s\n", sep, badge, body)

	case "system":
		return SystemMsgStyle.Width(bodyWidth).Render("⚙ "+msg.Content) + "\n"

	case "error":
		return ErrorMsgStyle.Width(bodyWidth).Render("✗ "+msg.Content) + "\n"

	case "log":
		return LogStyle.Width(bodyWidth).Render("▸ "+msg.Content) + "\n"

	default:
		return msg.Content + "\n"
	}
}
