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
	return MultiSelectModel{
		title:    title,
		items:    items,
		selected: make(map[int]struct{}),
		height:   visibleHeight,
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
		case " ":
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
			m.done = true
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
	sb.WriteString(styleMuted.Render(fmt.Sprintf("  %d items  •  %d selected", len(m.items), len(m.selected))))
	sb.WriteString("\n\n")

	end := m.offset + m.height
	if end > len(m.items) {
		end = len(m.items)
	}

	for i := m.offset; i < end; i++ {
		item := m.items[i]
		_, isSelected := m.selected[i]

		cursor := "  "
		if i == m.cursor {
			cursor = styleCursor.Render("▶ ")
		}

		checkbox := styleNormal.Render("☐")
		if isSelected {
			checkbox = styleSelected.Render("☑")
		}

		name := styleNormal.Render(item.DisplayName())
		if i == m.cursor {
			name = styleMenuItemActive.Render(item.DisplayName())
		}

		sub := styleMuted.Render("  " + item.SubText())
		sb.WriteString(fmt.Sprintf("%s%s %s\n%s\n", cursor, checkbox, name, sub))
	}

	if len(m.items) > m.height {
		sb.WriteString(styleMuted.Render(fmt.Sprintf("\n  Showing %d–%d of %d", m.offset+1, end, len(m.items))))
	}

	sb.WriteString(styleHelp.Render("\n  ↑/↓ navigate  •  space select  •  a select all  •  enter confirm  •  esc cancel"))
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
