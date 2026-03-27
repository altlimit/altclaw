package tui

import (
	"fmt"

	"charm.land/lipgloss/v2"
)

// StatusBar renders the bottom status bar with model info and spinner state.
type StatusBar struct {
	ProviderLabel string // configured name (e.g. "default", "coding")
	ProviderName  string // provider type (e.g. "gemini", "anthropic")
	ModelName     string
	Workspace     string
	ChatTitle     string
	Thinking      bool
	Prompting     bool   // true when waiting for user answer
	SpinnerFrame  string
}

// Render draws the status bar to fit the given width.
func (s StatusBar) Render(width int) string {
	left := ""
	if s.Prompting {
		left = lipgloss.NewStyle().Foreground(warningColor).Render("◆ awaiting input")
	} else if s.Thinking {
		left = SpinnerStyle.Render(s.SpinnerFrame+" thinking...")
	} else {
		left = lipgloss.NewStyle().Foreground(successColor).Render("● ready")
	}

	label := s.ProviderName + "/" + s.ModelName
	if s.ProviderLabel != "" {
		label = s.ProviderLabel + " (" + s.ProviderName + "/" + s.ModelName + ")"
	}
	provider := lipgloss.NewStyle().
		Foreground(accentColor).
		Bold(true).
		Render(" " + label + " ")

	chatInfo := ""
	if s.ChatTitle != "" {
		title := s.ChatTitle
		if len(title) > 20 {
			title = title[:17] + "..."
		}
		chatInfo = lipgloss.NewStyle().
			Foreground(warningColor).
			Render(fmt.Sprintf(" 💬 %s ", title))
	}

	wsShort := s.Workspace
	if len(wsShort) > 25 {
		wsShort = "..." + wsShort[len(wsShort)-22:]
	}
	ws := lipgloss.NewStyle().
		Foreground(dimColor).
		Render(fmt.Sprintf(" 📁 %s ", wsShort))

	right := provider + chatInfo + ws

	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}
	padding := lipgloss.NewStyle().Width(gap).Render("")

	bar := StatusBarStyle.Width(width).Render(left + padding + right)
	return bar
}
