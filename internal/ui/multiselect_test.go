package ui

import (
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

type testItem struct{ name string }

func (t testItem) DisplayName() string { return t.name }
func (t testItem) SubText() string     { return "" }

func newTestMultiSelect(names ...string) MultiSelectModel {
	items := make([]SelectableItem, len(names))
	for i, n := range names {
		items[i] = testItem{n}
	}
	return NewMultiSelect("test", items, 10)
}

func key(s string) tea.KeyMsg {
	if len(s) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	switch s {
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestMultiSelectSearchFilter(t *testing.T) {
	m := newTestMultiSelect("nginx:1.27", "postgres:16", "redis:7")

	// Enter search mode and type "redis".
	m, _ = m.Update(key("/"))
	for _, r := range "redis" {
		m, _ = m.Update(key(string(r)))
	}

	if got := m.visible(); !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("visible = %v, want [2]", got)
	}

	// Enter applies the filter and leaves search mode.
	m, _ = m.Update(key("enter"))
	if m.searching {
		t.Fatal("expected search mode to end after enter")
	}
	if m.done {
		t.Fatal("enter in search mode must not confirm selection")
	}

	// Toggling selects the original item index.
	m, _ = m.Update(key(" "))
	if got := m.SelectedIndices(); !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("selected = %v, want [2]", got)
	}

	// Selection survives clearing the filter.
	m, _ = m.Update(key("esc"))
	if m.filter != "" {
		t.Fatalf("filter = %q, want empty", m.filter)
	}
	if got := m.SelectedIndices(); !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("selected = %v, want [2]", got)
	}
	if got := len(m.visible()); got != 3 {
		t.Fatalf("visible count = %d, want 3", got)
	}
}

func TestMultiSelectSelectAllFiltered(t *testing.T) {
	m := newTestMultiSelect("nginx:1.27", "nginx-alpine:1", "redis:7")
	m = m.setFilter("nginx")

	m, _ = m.Update(key("a"))
	got := m.SelectedIndices()
	if len(got) != 2 {
		t.Fatalf("selected = %v, want 2 items", got)
	}

	// Toggling again deselects only the filtered items.
	m, _ = m.Update(key("a"))
	if got := m.SelectedIndices(); len(got) != 0 {
		t.Fatalf("selected = %v, want none", got)
	}
}

func TestMultiSelectSelect(t *testing.T) {
	m := newTestMultiSelect("a", "b", "c").Select(0, 2)
	got := m.SelectedIndices()
	if len(got) != 2 {
		t.Fatalf("selected = %v, want 2 items", got)
	}
}
