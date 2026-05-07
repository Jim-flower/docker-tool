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

	sb.WriteString(styleTitle.Render(m.title))
	sb.WriteString("\n")
	position := "0/0"
	if len(m.items) > 0 {
		position = fmt.Sprintf("%d/%d", m.cursor+1, len(m.items))
	}
	sb.WriteString(styleMuted.Render(fmt.Sprintf("  %d items  •  %d selected  •  current %s", len(m.items), len(m.selected), position)))
	sb.WriteString("\n\n")

	end := m.offset + m.height
	if end > len(m.items) {
		end = len(m.items)
	}

	if len(m.items) == 0 {
		sb.WriteString(styleMuted.Render("  (no items found)"))
		sb.WriteString("\n")
	}

	for i := m.offset; i < end; i++ {
		item := m.items[i]
		_, isSelected := m.selected[i]
		isActive := i == m.cursor

		cursor := "  "
		if isActive {
			cursor = "> "
		}

		checkbox := "[ ]"
		if isSelected {
			checkbox = "[x]"
		}

		line := fmt.Sprintf("%s%s %s", cursor, checkbox, item.DisplayName())
		sub := "    " + item.SubText()
		if isActive {
			sb.WriteString(styleListItemActive.Render(line) + "\n")
			sb.WriteString(styleListSubtextActive.Render(sub) + "\n")
			continue
		}

		if isSelected {
			sb.WriteString(styleSelected.Render(line) + "\n")
		} else {
			sb.WriteString(styleNormal.Render(line) + "\n")
		}
		sb.WriteString(styleMuted.Render(sub) + "\n")
	}

	if len(m.items) > m.height {
		sb.WriteString(styleMuted.Render(fmt.Sprintf("\n  Showing %d–%d of %d", m.offset+1, end, len(m.items))))
	}

	sb.WriteString(styleHelp.Render("\n  ↑/↓ navigate  •  pgup/pgdown page  •  space select  •  a select all  •  enter confirm  •  esc cancel"))
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
