package ui

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/docker/go-units"
	dockerclient "github.com/jim/dockertool/internal/docker"
	"github.com/jim/dockertool/internal/operations"
)

// screen represents the current view state.
type screen int

const (
	screenMenu screen = iota
	screenLoading
	screenImageList
	screenVolumeList
	screenExportDest
	screenImportFile
	screenImportVolumeName
	screenResults
	screenError
)

// action tracks what the user intends to do with selected items.
type action int

const (
	actionNone action = iota
	actionExportImages
	actionExportVolumes
	actionImportImages
	actionImportVolumes
)

// App is the root Bubbletea model.
type App struct {
	dc      *dockerclient.Client
	screen  screen
	action  action
	menuIdx int

	// data
	images  []dockerclient.Image
	volumes []dockerclient.Volume

	// sub-models
	multiSelect MultiSelectModel
	filePicker  FilePicker
	inputBuffer string

	// operation state
	selectedImageIndices  []int
	selectedVolumeIndices []int
	importFilePath        string
	results               []string
	errMsg                string
	loadingMsg            string

	windowWidth  int
	windowHeight int
}

type imageListLoadedMsg struct {
	images []dockerclient.Image
	err    error
}

type volumeListLoadedMsg struct {
	volumes []dockerclient.Volume
	err     error
}

var menuItems = []string{
	"Export Images",
	"Export Volumes",
	"Import Images",
	"Import Volumes",
	"Quit",
}

// NewApp creates and initialises the App model.
func NewApp(dc *dockerclient.Client) *App {
	return &App{dc: dc, screen: screenMenu}
}

func (a *App) Init() tea.Cmd {
	return nil
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.windowWidth = msg.Width
		a.windowHeight = msg.Height
		return a, nil

	case imageListLoadedMsg:
		return a.handleImageListLoaded(msg)

	case volumeListLoadedMsg:
		return a.handleVolumeListLoaded(msg)

	case tea.KeyMsg:
		return a.handleKey(msg)
	}
	return a, nil
}

func (a *App) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch a.screen {
	case screenMenu:
		return a.handleMenu(msg)
	case screenLoading:
		if msg.String() == "esc" || msg.String() == "q" || msg.String() == "ctrl+c" {
			a.screen = screenMenu
		}
		if msg.String() == "ctrl+c" {
			return a, tea.Quit
		}
	case screenImageList, screenVolumeList:
		return a.handleList(msg)
	case screenExportDest:
		return a.handleExportDest(msg)
	case screenImportFile:
		return a.handleImportFile(msg)
	case screenImportVolumeName:
		return a.handleImportVolumeName(msg)
	case screenResults, screenError:
		a.screen = screenMenu
	}
	return a, nil
}

// ---- Menu ----

func (a *App) handleMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if a.menuIdx > 0 {
			a.menuIdx--
		}
	case "down", "j":
		if a.menuIdx < len(menuItems)-1 {
			a.menuIdx++
		}
	case "enter":
		return a.activateMenuItem()
	case "ctrl+c", "q":
		return a, tea.Quit
	}
	return a, nil
}

func (a *App) activateMenuItem() (tea.Model, tea.Cmd) {
	switch a.menuIdx {
	case 0: // Export Images
		a.action = actionExportImages
		a.loadingMsg = "Loading Docker images..."
		a.screen = screenLoading
		return a, loadImagesCmd(a.dc)

	case 1: // Export Volumes
		a.action = actionExportVolumes
		a.loadingMsg = "Loading Docker volumes..."
		a.screen = screenLoading
		return a, loadVolumesCmd(a.dc)

	case 2: // Import Images
		home, _ := os.UserHomeDir()
		a.filePicker = NewFilePicker(home, ".tar", listHeight(a.windowHeight))
		a.action = actionImportImages
		a.screen = screenImportFile

	case 3: // Import Volumes
		home, _ := os.UserHomeDir()
		a.filePicker = NewFilePicker(home, ".tar", listHeight(a.windowHeight))
		a.action = actionImportVolumes
		a.screen = screenImportFile

	case 4: // Quit
		return a, tea.Quit
	}
	return a, nil
}

func loadImagesCmd(dc *dockerclient.Client) tea.Cmd {
	return func() tea.Msg {
		imgs, err := dc.ListImages(context.Background())
		return imageListLoadedMsg{images: imgs, err: err}
	}
}

func loadVolumesCmd(dc *dockerclient.Client) tea.Cmd {
	return func() tea.Msg {
		vols, err := dc.ListVolumes(context.Background())
		return volumeListLoadedMsg{volumes: vols, err: err}
	}
}

func (a *App) handleImageListLoaded(msg imageListLoadedMsg) (tea.Model, tea.Cmd) {
	if a.screen != screenLoading || a.action != actionExportImages {
		return a, nil
	}
	if msg.err != nil {
		a.errMsg = msg.err.Error()
		a.screen = screenError
		return a, nil
	}
	a.images = msg.images
	items := make([]SelectableItem, len(msg.images))
	for i, img := range msg.images {
		items[i] = imageItem{img}
	}
	a.multiSelect = NewMultiSelect("Select Images to Export", items, listHeight(a.windowHeight))
	a.action = actionExportImages
	a.screen = screenImageList
	return a, nil
}

func (a *App) handleVolumeListLoaded(msg volumeListLoadedMsg) (tea.Model, tea.Cmd) {
	if a.screen != screenLoading || a.action != actionExportVolumes {
		return a, nil
	}
	if msg.err != nil {
		a.errMsg = msg.err.Error()
		a.screen = screenError
		return a, nil
	}
	a.volumes = msg.volumes
	items := make([]SelectableItem, len(msg.volumes))
	for i, v := range msg.volumes {
		items[i] = volumeItem{v}
	}
	a.multiSelect = NewMultiSelect("Select Volumes to Export", items, listHeight(a.windowHeight))
	a.action = actionExportVolumes
	a.screen = screenVolumeList
	return a, nil
}

// ---- Multi-select list ----

func (a *App) handleList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	updated, cmd := a.multiSelect.Update(msg)
	a.multiSelect = updated

	if a.multiSelect.IsCanceled() {
		a.screen = screenMenu
		return a, nil
	}

	if a.multiSelect.IsDone() {
		indices := a.multiSelect.SelectedIndices()
		if len(indices) == 0 {
			a.screen = screenMenu
			return a, nil
		}
		sort.Ints(indices)

		if a.action == actionExportImages {
			a.selectedImageIndices = indices
		} else {
			a.selectedVolumeIndices = indices
		}
		a.inputBuffer = ""
		a.screen = screenExportDest
	}

	return a, cmd
}

// ---- Export destination input ----

func (a *App) handleExportDest(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		destDir := strings.TrimSpace(a.inputBuffer)
		if destDir == "" {
			destDir = "."
		}
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			a.errMsg = "Cannot create destination directory: " + err.Error()
			a.screen = screenError
			return a, nil
		}
		a.runExport(destDir)
	case "esc":
		a.screen = screenMenu
	case "backspace":
		if len(a.inputBuffer) > 0 {
			a.inputBuffer = a.inputBuffer[:len(a.inputBuffer)-1]
		}
	default:
		if len(msg.String()) == 1 {
			a.inputBuffer += msg.String()
		}
	}
	return a, nil
}

func (a *App) runExport(destDir string) {
	ctx := context.Background()
	var lines []string

	if a.action == actionExportImages {
		ids := make([]string, len(a.selectedImageIndices))
		names := make([]string, len(a.selectedImageIndices))
		for i, idx := range a.selectedImageIndices {
			ids[i] = a.images[idx].ID
			names[i] = a.images[idx].DisplayName()
		}
		results := operations.ExportImages(ctx, a.dc, ids, names, destDir)
		for _, r := range results {
			if r.Err != nil {
				lines = append(lines, styleError.Render("✗ "+r.Name+": "+r.Err.Error()))
			} else {
				lines = append(lines, styleSuccess.Render("✔ "+r.Name+" → "+r.FilePath))
			}
		}
	} else {
		names := make([]string, len(a.selectedVolumeIndices))
		for i, idx := range a.selectedVolumeIndices {
			names[i] = a.volumes[idx].Name
		}
		results := operations.ExportVolumes(ctx, a.dc, names, destDir)
		for _, r := range results {
			if r.Err != nil {
				lines = append(lines, styleError.Render("✗ "+r.Name+": "+r.Err.Error()))
			} else {
				lines = append(lines, styleSuccess.Render("✔ "+r.Name+" → "+r.FilePath))
			}
		}
	}

	a.results = lines
	a.screen = screenResults
}

// ---- Import file picker ----

func (a *App) handleImportFile(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	updated, cmd := a.filePicker.Update(msg)
	a.filePicker = updated

	if a.filePicker.IsCanceled() {
		a.screen = screenMenu
		return a, nil
	}

	if a.filePicker.IsChosen() {
		a.importFilePath = a.filePicker.Chosen()
		if a.action == actionImportImages {
			ctx := context.Background()
			results := operations.ImportImages(ctx, a.dc, []string{a.importFilePath})
			var lines []string
			for _, r := range results {
				if r.Err != nil {
					lines = append(lines, styleError.Render("✗ "+r.Name+": "+r.Err.Error()))
				} else {
					lines = append(lines, styleSuccess.Render("✔ Loaded: "+r.Name))
				}
			}
			a.results = lines
			a.screen = screenResults
		} else {
			// Need a volume name for import.
			a.inputBuffer = ""
			a.screen = screenImportVolumeName
		}
	}

	return a, cmd
}

// ---- Volume name input for import ----

func (a *App) handleImportVolumeName(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		volName := strings.TrimSpace(a.inputBuffer)
		if volName == "" {
			break
		}
		ctx := context.Background()
		results := operations.ImportVolumes(ctx, a.dc, []string{a.importFilePath}, []string{volName})
		var lines []string
		for _, r := range results {
			if r.Err != nil {
				lines = append(lines, styleError.Render("✗ "+r.Name+": "+r.Err.Error()))
			} else {
				lines = append(lines, styleSuccess.Render("✔ Imported into volume: "+r.Name))
			}
		}
		a.results = lines
		a.screen = screenResults
	case "esc":
		a.screen = screenMenu
	case "backspace":
		if len(a.inputBuffer) > 0 {
			a.inputBuffer = a.inputBuffer[:len(a.inputBuffer)-1]
		}
	default:
		if len(msg.String()) == 1 {
			a.inputBuffer += msg.String()
		}
	}
	return a, nil
}

// ---- View ----

func (a *App) View() string {
	var sb strings.Builder

	sb.WriteString(styleHeader.Render(" Docker Tool ") + "\n\n")

	switch a.screen {
	case screenMenu:
		sb.WriteString(styleTitle.Render("Main Menu") + "\n\n")
		for i, item := range menuItems {
			cursor := "  "
			if i == a.menuIdx {
				cursor = styleMenuCursor.Render("▶ ")
				sb.WriteString(cursor + styleMenuItemActive.Render(item) + "\n")
			} else {
				sb.WriteString(cursor + styleMenuItem.Render(item) + "\n")
			}
		}
		sb.WriteString(styleHelp.Render("\n  ↑/↓ navigate  •  enter select  •  q quit"))

	case screenLoading:
		sb.WriteString(styleTitle.Render("Please Wait") + "\n\n")
		sb.WriteString(styleNormal.Render("  "+a.loadingMsg) + "\n")
		sb.WriteString(styleHelp.Render("\n  esc return to menu"))

	case screenImageList, screenVolumeList:
		sb.WriteString(a.multiSelect.View())

	case screenExportDest:
		sb.WriteString(styleTitle.Render("Export Destination") + "\n\n")
		sb.WriteString(styleNormal.Render("  Directory path (leave blank for current directory):") + "\n")
		sb.WriteString(styleSelected.Render("  > "+a.inputBuffer+"█") + "\n")
		sb.WriteString(styleHelp.Render("\n  enter confirm  •  esc cancel"))

	case screenImportFile:
		sb.WriteString(a.filePicker.View())

	case screenImportVolumeName:
		sb.WriteString(styleTitle.Render("Volume Name for Import") + "\n\n")
		sb.WriteString(styleMuted.Render("  File: "+a.importFilePath) + "\n\n")
		sb.WriteString(styleNormal.Render("  Target volume name:") + "\n")
		sb.WriteString(styleSelected.Render("  > "+a.inputBuffer+"█") + "\n")
		sb.WriteString(styleHelp.Render("\n  enter confirm  •  esc cancel"))

	case screenResults:
		sb.WriteString(styleTitle.Render("Results") + "\n\n")
		for _, line := range a.results {
			sb.WriteString("  " + line + "\n")
		}
		sb.WriteString(styleHelp.Render("\n  Press any key to return to the menu"))

	case screenError:
		sb.WriteString(styleError.Render("Error") + "\n\n")
		sb.WriteString(styleNormal.Render("  "+a.errMsg) + "\n")
		sb.WriteString(styleHelp.Render("\n  Press any key to return to the menu"))
	}

	return sb.String()
}

// ---- helpers for SelectableItem adapters ----

type imageItem struct{ img dockerclient.Image }

func (i imageItem) DisplayName() string { return i.img.DisplayName() }
func (i imageItem) SubText() string {
	return fmt.Sprintf("ID: %s  Size: %s", i.img.ShortID, units.HumanSize(float64(i.img.Size)))
}

type volumeItem struct{ vol dockerclient.Volume }

func (v volumeItem) DisplayName() string { return v.vol.Name }
func (v volumeItem) SubText() string {
	return fmt.Sprintf("Driver: %s  Mount: %s", v.vol.Driver, v.vol.Mountpoint)
}

func listHeight(windowHeight int) int {
	if windowHeight > 20 {
		return windowHeight - 10
	}
	return 10
}
