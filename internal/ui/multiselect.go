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
	title     string
	items     []SelectableItem
	cursor    int
	selected  map[int]struct{}
	done      bool
	canceled  bool
	offset    int
	height    int
	filter    string
	searching bool
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

// Select marks the given item indices as selected (keeping prior selections).
func (m MultiSelectModel) Select(indices ...int) MultiSelectModel {
	for _, i := range indices {
		if i >= 0 && i < len(m.items) {
			m.selected[i] = struct{}{}
		}
	}
	return m
}

// visible returns the indices of items matching the current filter.
func (m MultiSelectModel) visible() []int {
	if m.filter == "" {
		indices := make([]int, len(m.items))
		for i := range m.items {
			indices[i] = i
		}
		return indices
	}
	needle := strings.ToLower(m.filter)
	var indices []int
	for i, item := range m.items {
		if strings.Contains(strings.ToLower(item.DisplayName()), needle) {
			indices = append(indices, i)
		}
	}
	return indices
}

func (m MultiSelectModel) setFilter(filter string) MultiSelectModel {
	m.filter = filter
	m.cursor = 0
	m.offset = 0
	return m
}

// updateSearch handles keys while the search input is active.
func (m MultiSelectModel) updateSearch(key tea.KeyMsg) MultiSelectModel {
	switch key.Type {
	case tea.KeyEnter:
		m.searching = false
	case tea.KeyEsc:
		m.searching = false
		m = m.setFilter("")
	case tea.KeyBackspace:
		runes := []rune(m.filter)
		if len(runes) > 0 {
			m = m.setFilter(string(runes[:len(runes)-1]))
		}
	case tea.KeyCtrlU:
		m = m.setFilter("")
	case tea.KeySpace:
		m = m.setFilter(m.filter + " ")
	case tea.KeyRunes:
		m = m.setFilter(m.filter + string(key.Runes))
	}
	return m
}

func (m MultiSelectModel) Update(msg tea.Msg) (MultiSelectModel, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if m.searching {
		return m.updateSearch(key), nil
	}

	vis := m.visible()
	switch key.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			if m.cursor < m.offset {
				m.offset--
			}
		}
	case "down", "j":
		if m.cursor < len(vis)-1 {
			m.cursor++
			if m.cursor >= m.offset+m.height {
				m.offset++
			}
		}
	case "pgup":
		if len(vis) == 0 {
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
		if len(vis) == 0 {
			break
		}
		m.cursor += m.height
		if m.cursor >= len(vis) {
			m.cursor = len(vis) - 1
		}
		if m.cursor >= m.offset+m.height {
			m.offset = m.cursor - m.height + 1
		}
	case "home":
		m.cursor = 0
		m.offset = 0
	case "end":
		if len(vis) == 0 {
			m.cursor = 0
			m.offset = 0
			break
		}
		m.cursor = len(vis) - 1
		if len(vis) > m.height {
			m.offset = len(vis) - m.height
		}
	case " ":
		if len(vis) == 0 {
			break
		}
		idx := vis[m.cursor]
		if _, ok := m.selected[idx]; ok {
			delete(m.selected, idx)
		} else {
			m.selected[idx] = struct{}{}
		}
	case "a":
		// Toggle all currently visible (filtered) items.
		allSelected := len(vis) > 0
		for _, idx := range vis {
			if _, ok := m.selected[idx]; !ok {
				allSelected = false
				break
			}
		}
		if allSelected {
			for _, idx := range vis {
				delete(m.selected, idx)
			}
		} else {
			for _, idx := range vis {
				m.selected[idx] = struct{}{}
			}
		}
	case "/":
		m.searching = true
	case "enter":
		if len(m.items) > 0 {
			m.done = true
		}
	case "esc", "q":
		if m.filter != "" {
			m = m.setFilter("")
		} else {
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
	vis := m.visible()
	total := len(vis)
	pos := "─"
	if total > 0 {
		pos = fmt.Sprintf("%d/%d", m.cursor+1, total)
	}
	stats := fmt.Sprintf("  %d items", total)
	if m.filter != "" {
		stats = fmt.Sprintf("  %d of %d items", total, len(m.items))
	}
	if selCount > 0 {
		stats += "  ·  " + styleSelected.Render(fmt.Sprintf("%d selected", selCount))
	}
	stats += "  ·  " + styleMuted.Render("pos "+pos)
	sb.WriteString(stats + "\n")

	// Filter row
	if m.searching || m.filter != "" {
		filterLine := styleMuted.Render("  Filter: ") + styleInput.Render(m.filter)
		if m.searching {
			filterLine += styleMenuCursor.Render("▌")
		}
		sb.WriteString(filterLine + "\n")
	}
	sb.WriteString("\n")

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

	for row := m.offset; row < end; row++ {
		i := vis[row]
		item := m.items[i]
		_, isSelected := m.selected[i]
		isActive := row == m.cursor

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

	if m.searching {
		sb.WriteString(renderHelpBar("enter", "apply filter", "esc", "clear filter", "ctrl+u", "clear input"))
	} else {
		sb.WriteString(renderHelpBar("↑↓", "navigate", "space", "toggle", "a", "all/none", "/", "search", "enter", "confirm", "esc", "cancel"))
	}
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
