// Package tui implements the Bubble Tea terminal UI for Altclaw.
package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Color palette — matches website brand (amber + blue dark theme)
var (
	primaryColor = lipgloss.Color("#F59E0B") // amber  (--primary-color)
	accentColor  = lipgloss.Color("#5BC0EB") // blue   (--secondary-color)
	successColor = lipgloss.Color("#22C55E") // green
	warningColor = lipgloss.Color("#F59E0B") // amber
	errorColor   = lipgloss.Color("#EF4444") // red
	surfaceColor = lipgloss.Color("#0A0A0F") // near-black (--bg-color)
	textColor    = lipgloss.Color("#FFFFFF") // white  (--text-color)
	dimColor     = lipgloss.Color("#A1A1AA") // zinc   (--text-muted)
	borderColor  = lipgloss.Color("#3F3F46") // subtle border
)

// Style definitions
var (
	HeaderStyle = lipgloss.NewStyle().
		Background(primaryColor).
		Foreground(lipgloss.Color("#000000")).
		Bold(true).
		Padding(0, 2)

	StatusBarStyle = lipgloss.NewStyle().
		Background(lipgloss.Color("#050505")).
		Foreground(dimColor).
		Padding(0, 1)

	UserMsgStyle = lipgloss.NewStyle().
		Foreground(accentColor).
		Bold(true).
		PaddingLeft(2)

	UserBodyStyle = lipgloss.NewStyle().
		Foreground(textColor).
		PaddingLeft(4)

	AssistantMsgStyle = lipgloss.NewStyle().
		Foreground(primaryColor).
		Bold(true).
		PaddingLeft(2)

	AssistantBodyStyle = lipgloss.NewStyle().
		Foreground(textColor).
		PaddingLeft(4)

	SystemMsgStyle = lipgloss.NewStyle().
		Foreground(dimColor).
		Italic(true).
		PaddingLeft(2)

	ErrorMsgStyle = lipgloss.NewStyle().
		Foreground(errorColor).
		Bold(true).
		PaddingLeft(2)

	InputBorderStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(primaryColor).
		Padding(0, 1)

	SpinnerStyle = lipgloss.NewStyle().
		Foreground(primaryColor)

	HelpStyle = lipgloss.NewStyle().
		Foreground(dimColor).
		Italic(true)

	BadgeStyle = func(bg color.Color, fg color.Color) lipgloss.Style {
		return lipgloss.NewStyle().
			Background(bg).
			Foreground(fg).
			Padding(0, 1).
			Bold(true)
	}

	UserBadge      = BadgeStyle(accentColor, lipgloss.Color("#000000"))
	AssistantBadge = BadgeStyle(primaryColor, lipgloss.Color("#000000"))

	LogStyle = lipgloss.NewStyle().
		Foreground(successColor).
		PaddingLeft(4)

	SeparatorStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#3F3F46")).
		PaddingLeft(2)
)
