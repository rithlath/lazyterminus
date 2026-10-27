// Package theme holds lazyterminus's colors, styles, and status symbols.
//
// Colors are given in truecolor; Bubble Tea downsamples them to the
// terminal's profile (256 colors, 16 colors, or none when NO_COLOR is set).
// Every status also carries a symbol or word so nothing relies on color.
package theme

import (
	"charm.land/lipgloss/v2"
)

// Palette colors.
var (
	Accent = lipgloss.Color("#FF5FAF") // focus, key hints
	Purple = lipgloss.Color("#7D56F4") // app badge, selection
	Green  = lipgloss.Color("#04B575") // healthy, in sync, success
	Amber  = lipgloss.Color("#F5B942") // needs attention, running
	Red    = lipgloss.Color("#FF6B6B") // failed
	Muted  = lipgloss.Color("#8A8A8A") // metadata
	Violet = lipgloss.Color("#C792EA") // SFTP mode
	White  = lipgloss.Color("#FFFFFF")
)

// Status symbols. Each is meaningful without color.
const (
	SymOK      = "●"
	SymUnknown = "○"
	SymFailed  = "✗"
	SymSuccess = "✓"
	SymAhead   = "↑"
	SymCursor  = "›"
	SymRunning = "◐"
)

// Styles used across panels.
var (
	Base       = lipgloss.NewStyle()
	MutedText  = lipgloss.NewStyle().Foreground(Muted)
	OK         = lipgloss.NewStyle().Foreground(Green)
	Warn       = lipgloss.NewStyle().Foreground(Amber)
	Fail       = lipgloss.NewStyle().Foreground(Red)
	SFTP       = lipgloss.NewStyle().Foreground(Violet)
	Key        = lipgloss.NewStyle().Foreground(Accent).Bold(true)
	Bold       = lipgloss.NewStyle().Bold(true)
	Title      = lipgloss.NewStyle().Bold(true)
	FocusTitle = lipgloss.NewStyle().Foreground(Accent).Bold(true)
	Border     = lipgloss.NewStyle().Foreground(Muted)
	FocusBord  = lipgloss.NewStyle().Foreground(Accent)
	Selected   = lipgloss.NewStyle().Background(Purple).Foreground(White).Bold(true)
	SelectedBl = lipgloss.NewStyle().Bold(true)
	AppBadge   = lipgloss.NewStyle().Background(Purple).Foreground(White).Bold(true).Padding(0, 1)
	ModeBadge  = lipgloss.NewStyle().Background(Amber).Foreground(lipgloss.Color("#000000")).Bold(true).Padding(0, 1)
	Label      = lipgloss.NewStyle().Foreground(Muted).Width(14)
	Divider    = lipgloss.NewStyle().Foreground(Muted)
)
