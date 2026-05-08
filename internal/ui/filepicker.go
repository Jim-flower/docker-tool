package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type pickerMode int

const (
	pickerModeFile pickerMode = iota
	pickerModeDirectory
)

// FilePicker lets the user navigate and pick .tar files or directories.
type FilePicker struct {
	currentDir string
	entries    []os.DirEntry
	cursor     int
	offset     int
	height     int
	chosen     string
	canceled   bool
	filter     string
	err        error
	mode       pickerMode
}

func NewFilePicker(startDir, filter string, visibleHeight int, mode ...pickerMode) FilePicker {
	pickMode := pickerModeFile
	if len(mode) > 0 {
		pickMode = mode[0]
	}
	fp := FilePicker{
		currentDir: startDir,
		height:     visibleHeight,
		filter:     filter,
		mode:       pickMode,
	}
	fp.entries, fp.err = readDir(startDir, filter)
	return fp
}

func (fp FilePicker) Init() tea.Cmd { return nil }

func (fp FilePicker) Update(msg tea.Msg) (FilePicker, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if fp.cursor > 0 {
				fp.cursor--
				if fp.cursor < fp.offset {
					fp.offset--
				}
			}
		case "down", "j":
			if fp.cursor < len(fp.entries)-1 {
				fp.cursor++
				if fp.cursor >= fp.offset+fp.height {
					fp.offset++
				}
			}
		case "enter":
			if fp.mode == pickerModeDirectory {
				fp.chosen = fp.selectedDir()
				break
			}
			if len(fp.entries) == 0 {
				break
			}
			entry := fp.entries[fp.cursor]
			full := filepath.Join(fp.currentDir, entry.Name())
			if entry.IsDir() {
				fp.currentDir = full
				fp.cursor = 0
				fp.offset = 0
				fp.entries, fp.err = readDir(full, fp.filter)
			} else {
				fp.chosen = full
			}
		case "right", "l":
			if len(fp.entries) == 0 {
				break
			}
			entry := fp.entries[fp.cursor]
			if !entry.IsDir() {
				break
			}
			full := filepath.Join(fp.currentDir, entry.Name())
			fp.currentDir = full
			fp.cursor = 0
			fp.offset = 0
			fp.entries, fp.err = readDir(full, fp.filter)
		case "backspace", "left", "h":
			parent := filepath.Dir(fp.currentDir)
			if parent != fp.currentDir {
				fp.currentDir = parent
				fp.cursor = 0
				fp.offset = 0
				fp.entries, fp.err = readDir(parent, fp.filter)
			}
		case "esc", "q":
			fp.canceled = true
		}
	}
	return fp, nil
}

func (fp FilePicker) View() string {
	var sb strings.Builder

	// Title
	title := "Select File"
	if fp.mode == pickerModeDirectory {
		title = "Select Export Folder"
	}
	sb.WriteString(styleTitle.Render(title) + "\n")

	// Current path (truncated to reasonable length)
	path := fp.currentDir
	const maxPathLen = 60
	if len(path) > maxPathLen {
		path = "…" + path[len(path)-maxPathLen+1:]
	}
	sb.WriteString(styleMuted.Render("  ") + styleInfo.Render(path) + "\n\n")

	if fp.err != nil {
		sb.WriteString(styleError.Render("  Error: "+fp.err.Error()) + "\n")
	} else if len(fp.entries) == 0 {
		sb.WriteString(styleMuted.Render("  (empty directory)") + "\n")
	} else {
		end := fp.offset + fp.height
		if end > len(fp.entries) {
			end = len(fp.entries)
		}

		// Scroll-up hint
		if fp.offset > 0 {
			sb.WriteString(styleMuted.Render(fmt.Sprintf("  ▲ %d more above\n", fp.offset)))
		}

		for i := fp.offset; i < end; i++ {
			entry := fp.entries[i]
			isActive := i == fp.cursor

			cur := "  "
			if isActive {
				cur = styleCursor.Render("▸ ")
			}

			var icon, name string
			if entry.IsDir() {
				icon = styleMuted.Render("▶ ")
				if isActive {
					name = styleMenuItemActive.Render(entry.Name() + "/")
				} else {
					name = styleNormal.Render(entry.Name() + "/")
				}
			} else {
				icon = styleMuted.Render("  ")
				if isActive {
					name = styleMenuItemActive.Render(entry.Name())
				} else {
					name = styleMuted.Render(entry.Name())
				}
			}

			sb.WriteString(fmt.Sprintf("%s%s%s\n", cur, icon, name))
		}

		// Scroll-down hint
		below := len(fp.entries) - end
		if below > 0 {
			sb.WriteString(styleMuted.Render(fmt.Sprintf("  ▼ %d more below\n", below)))
		}
	}

	if fp.mode == pickerModeDirectory {
		sb.WriteString(renderHelpBar("↑↓", "navigate", "→", "open", "←", "parent", "enter", "select here", "esc", "cancel"))
	} else {
		sb.WriteString(renderHelpBar("↑↓", "navigate", "→/enter", "open", "←", "parent", "esc", "cancel"))
	}
	return sb.String()
}

func (fp FilePicker) IsChosen() bool   { return fp.chosen != "" }
func (fp FilePicker) IsCanceled() bool { return fp.canceled }
func (fp FilePicker) Chosen() string   { return fp.chosen }

func (fp FilePicker) selectedDir() string {
	if len(fp.entries) == 0 || fp.cursor < 0 || fp.cursor >= len(fp.entries) {
		return fp.currentDir
	}
	entry := fp.entries[fp.cursor]
	if !entry.IsDir() {
		return fp.currentDir
	}
	return filepath.Join(fp.currentDir, entry.Name())
}

func readDir(dir, filter string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	filtered := entries[:0]
	for _, e := range entries {
		if e.IsDir() || filter == "" || strings.HasSuffix(strings.ToLower(e.Name()), strings.ToLower(filter)) {
			filtered = append(filtered, e)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].IsDir() != filtered[j].IsDir() {
			return filtered[i].IsDir()
		}
		return strings.ToLower(filtered[i].Name()) < strings.ToLower(filtered[j].Name())
	})
	return filtered, nil
}
