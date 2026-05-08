package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// SelectableItem is any item that can appear in a multi-select list.
type SelectableItem interface {
	DisplayName() string
	SubText() string
}

// MultiSelectModel is a reusable multi-select list component.
type MultiSelectModel struct {
	title    string
	items    []SelectableItem
	cursor   int
	selected map[int]struct{}
	done     bool
	canceled bool
	offset   int
	height   int
}

func NewMultiSelect(title string, items []SelectableItem, visibleHeight int) MultiSelectModel {
	visibleItems := visibleHeight / 2
	if visibleItems < 1 {
		visibleItems = 1
	}
	return MultiSelectModel{
		title:    title,
		items:    items,
		selected: make(map[int]struct{}),
		height:   visibleItems,
	}
}

func (m MultiSelectModel) Init() tea.Cmd { return nil }

func (m MultiSelectModel) SelectAll() MultiSelectModel {
	m.selected = make(map[int]struct{}, len(m.items))
	for i := range m.items {
		m.selected[i] = struct{}{}
	}
	return m
}

func (m MultiSelectModel) Update(msg tea.Msg) (MultiSelectModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				if m.cursor < m.offset {
					m.offset--
				}
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
				if m.cursor >= m.offset+m.height {
					m.offset++
				}
			}
		case "pgup":
			if len(m.items) == 0 {
				break
			}
			m.cursor -= m.height
			if m.cursor < 0 {
				m.cursor = 0
			}
			if m.offset > m.cursor {
				m.offset = m.cursor
			}
		case "pgdown":
			if len(m.items) == 0 {
				break
			}
			m.cursor += m.height
			if m.cursor >= len(m.items) {
				m.cursor = len(m.items) - 1
			}
			if m.cursor >= m.offset+m.height {
				m.offset = m.cursor - m.height + 1
			}
		case "home":
			m.cursor = 0
			m.offset = 0
		case "end":
			if len(m.items) == 0 {
				m.cursor = 0
				m.offset = 0
				break
			}
			m.cursor = len(m.items) - 1
			if len(m.items) > m.height {
				m.offset = len(m.items) - m.height
			}
		case " ":
			if len(m.items) == 0 {
				break
			}
			if _, ok := m.selected[m.cursor]; ok {
				delete(m.selected, m.cursor)
			} else {
				m.selected[m.cursor] = struct{}{}
			}
		case "a":
			if len(m.selected) == len(m.items) {
				m.selected = make(map[int]struct{})
			} else {
				for i := range m.items {
					m.selected[i] = struct{}{}
				}
			}
		case "enter":
			if len(m.items) > 0 {
				m.done = true
			}
		case "esc", "q":
			m.canceled = true
		}
	}
	return m, nil
}

func (m MultiSelectModel) View() string {
	var sb strings.Builder

	// Title
	sb.WriteString(styleTitle.Render(m.title) + "\n")

	// Stats row
	selCount := len(m.selected)
	total := len(m.items)
	pos := "─"
	if total > 0 {
		pos = fmt.Sprintf("%d/%d", m.cursor+1, total)
	}
	stats := fmt.Sprintf("  %d items", total)
	if selCount > 0 {
		stats += fmt.Sprintf("  ·  " + styleSelected.Render(fmt.Sprintf("%d selected", selCount)))
	}
	stats += "  ·  " + styleMuted.Render("pos "+pos)
	sb.WriteString(stats + "\n\n")

	end := m.offset + m.height
	if end > total {
		end = total
	}

	// Scroll-up hint
	if m.offset > 0 {
		sb.WriteString(styleMuted.Render(fmt.Sprintf("  ▲ %d more above\n", m.offset)))
	}

	if total == 0 {
		sb.WriteString(styleMuted.Render("  (no items found)") + "\n")
	}

	for i := m.offset; i < end; i++ {
		item := m.items[i]
		_, isSelected := m.selected[i]
		isActive := i == m.cursor

		// Checkbox glyph
		check := "○"
		if isSelected {
			check = styleSelected.Render("◉")
		}

		// Cursor glyph
		cur := "  "
		if isActive {
			cur = styleCursor.Render("▸ ")
		}

		mainLine := fmt.Sprintf("%s%s  %s", cur, check, item.DisplayName())
		subLine := "      " + item.SubText()

		if isActive {
			sb.WriteString(styleListItemActive.Render(mainLine) + "\n")
			sb.WriteString(styleListSubtextActive.Render(subLine) + "\n")
		} else if isSelected {
			sb.WriteString(styleSelected.Render(mainLine) + "\n")
			sb.WriteString(styleSubtext.Render(subLine) + "\n")
		} else {
			sb.WriteString(styleNormal.Render(mainLine) + "\n")
			sb.WriteString(styleMuted.Render(subLine) + "\n")
		}
	}

	// Scroll-down hint
	below := total - end
	if below > 0 {
		sb.WriteString(styleMuted.Render(fmt.Sprintf("  ▼ %d more below\n", below)))
	}

	sb.WriteString(renderHelpBar("↑↓", "navigate", "space", "toggle", "a", "all/none", "enter", "confirm", "esc", "cancel"))
	return sb.String()
}

// SelectedIndices returns the indices of all selected items.
func (m MultiSelectModel) SelectedIndices() []int {
	indices := make([]int, 0, len(m.selected))
	for i := range m.selected {
		indices = append(indices, i)
	}
	return indices
}

func (m MultiSelectModel) IsDone() bool     { return m.done }
func (m MultiSelectModel) IsCanceled() bool { return m.canceled }
