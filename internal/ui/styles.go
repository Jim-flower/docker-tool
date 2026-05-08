package ui

import "github.com/charmbracelet/lipgloss"

var (
	// Core palette
	colorPrimary   = lipgloss.Color("#7C3AED")
	colorSecondary = lipgloss.Color("#10B981")
	colorDanger    = lipgloss.Color("#EF4444")
	colorMuted     = lipgloss.Color("#6B7280")
	colorSelected  = lipgloss.Color("#F59E0B")
	colorActiveBg  = lipgloss.Color("#2D3748")
	colorBorder    = lipgloss.Color("#4B5563")
	colorText      = lipgloss.Color("#F9FAFB")
	colorSubtext   = lipgloss.Color("#9CA3AF")
	colorInfo      = lipgloss.Color("#60A5FA")

	// Typography
	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			MarginBottom(1)

	styleNormal = lipgloss.NewStyle().
			Foreground(colorText)

	styleSubtext = lipgloss.NewStyle().
			Foreground(colorSubtext)

	styleSelected = lipgloss.NewStyle().
			Foreground(colorSelected).
			Bold(true)

	styleCursor = lipgloss.NewStyle().
			Foreground(colorSecondary).
			Bold(true)

	styleSuccess = lipgloss.NewStyle().
			Foreground(colorSecondary).
			Bold(true)

	styleError = lipgloss.NewStyle().
			Foreground(colorDanger).
			Bold(true)

	styleMuted = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleInfo = lipgloss.NewStyle().
			Foreground(colorInfo)

	// Header banner — full-width strip
	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorText).
			Background(colorPrimary).
			Padding(0, 2)

	// Section label (EXPORT / IMPORT)
	styleSectionLabel = lipgloss.NewStyle().
				Foreground(colorMuted).
				Bold(true)

	// Thin divider line
	styleDivider = lipgloss.NewStyle().
			Foreground(colorBorder)

	// Menu
	styleMenuCursor = lipgloss.NewStyle().
			Foreground(colorSecondary).
			Bold(true)

	styleMenuItem = lipgloss.NewStyle().
			Foreground(colorText)

	styleMenuItemActive = lipgloss.NewStyle().
				Foreground(colorPrimary).
				Bold(true)

	// List rows
	styleListItemActive = lipgloss.NewStyle().
				Foreground(colorText).
				Background(colorActiveBg).
				Bold(true)

	styleListSubtextActive = lipgloss.NewStyle().
				Foreground(colorSubtext).
				Background(colorActiveBg)

	// Help bar at the bottom
	styleHelp = lipgloss.NewStyle().
			Foreground(colorMuted).
			MarginTop(1)

	// Key badge: subtle bg pill around the key name
	styleKeyBind = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E5E7EB")).
			Background(colorActiveBg).
			Padding(0, 1)

	// Description next to a key badge
	styleKeyDesc = lipgloss.NewStyle().
			Foreground(colorMuted)

	// Text input cursor line
	styleInput = lipgloss.NewStyle().
			Foreground(colorSecondary).
			Bold(true)

	styleBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorPrimary).
			Padding(1, 2)

	styleSubtitle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Italic(true)
)
