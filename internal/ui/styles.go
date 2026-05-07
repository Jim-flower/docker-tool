package ui

import "github.com/charmbracelet/lipgloss"

var (
	colorPrimary   = lipgloss.Color("#7C3AED")
	colorSecondary = lipgloss.Color("#10B981")
	colorDanger    = lipgloss.Color("#EF4444")
	colorMuted     = lipgloss.Color("#6B7280")
	colorSelected  = lipgloss.Color("#F59E0B")
	colorActiveBg  = lipgloss.Color("#374151")
	colorBg        = lipgloss.Color("#1F2937")
	colorText      = lipgloss.Color("#F9FAFB")

	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			PaddingBottom(1)

	styleSubtitle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Italic(true)

	styleSelected = lipgloss.NewStyle().
			Foreground(colorSelected).
			Bold(true)

	styleNormal = lipgloss.NewStyle().
			Foreground(colorText)

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

	styleBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorPrimary).
			Padding(1, 2)

	styleHelp = lipgloss.NewStyle().
			Foreground(colorMuted).
			PaddingTop(1)

	styleMenuCursor = lipgloss.NewStyle().
			Foreground(colorSecondary).
			Bold(true)

	styleMenuItem = lipgloss.NewStyle().
			Foreground(colorText)

	styleMenuItemActive = lipgloss.NewStyle().
				Foreground(colorPrimary).
				Bold(true)

	styleListItemActive = lipgloss.NewStyle().
				Foreground(colorText).
				Background(colorActiveBg).
				Bold(true)

	styleListSubtextActive = lipgloss.NewStyle().
				Foreground(colorText).
				Background(colorActiveBg)

	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorText).
			Background(colorPrimary).
			Padding(0, 2)
)
