package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	screenExportProgress
	screenImportFile
	screenImportFileList
	screenImportVolumeName
	screenResults
	screenError
)

// action tracks what the user intends to do with selected items.
type action int

const (
	actionNone action = iota
	actionExportImages
	actionExportRunningContainerImages
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
	images   []dockerclient.Image
	volumes  []dockerclient.Volume
	tarFiles []tarFile

	// sub-models
	multiSelect MultiSelectModel
	filePicker  FilePicker
	inputBuffer string

	// operation state
	selectedImageIndices   []int
	selectedVolumeIndices  []int
	selectedTarFileIndices []int
	importFilePath         string
	results                []string
	errMsg                 string
	loadingMsg             string
	exportProgress         exportProgressState
	exportProgressCh       <-chan operations.ExportProgress

	windowWidth  int
	windowHeight int
}

type imageListLoadedMsg struct {
	images       []dockerclient.Image
	err          error
	sourceAction action
	title        string
	selectAll    bool
}

type volumeListLoadedMsg struct {
	volumes []dockerclient.Volume
	err     error
}

type exportProgressState struct {
	current   string
	completed int
	total     int
	lines     []string
	spinner   int
}

type exportProgressMsg struct {
	progress operations.ExportProgress
}

type exportSpinnerTickMsg struct{}

var menuItems = []string{
	"Export Images",
	"Export Running Container Images",
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

	case exportProgressMsg:
		return a.handleExportProgress(msg)

	case exportSpinnerTickMsg:
		return a.handleExportSpinnerTick()

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
	case screenExportProgress:
		if msg.String() == "ctrl+c" {
			return a, tea.Quit
		}
	case screenImportFile:
		return a.handleImportFile(msg)
	case screenImportFileList:
		return a.handleImportFileList(msg)
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

	case 1: // Export Running Container Images
		a.action = actionExportRunningContainerImages
		a.loadingMsg = "Loading images from running containers..."
		a.screen = screenLoading
		return a, loadRunningContainerImagesCmd(a.dc)

	case 2: // Export Volumes
		a.action = actionExportVolumes
		a.loadingMsg = "Loading Docker volumes..."
		a.screen = screenLoading
		return a, loadVolumesCmd(a.dc)

	case 3: // Import Images
		a.filePicker = NewFilePicker(defaultFilePickerDir(), ".tar", listHeight(a.windowHeight), pickerModeDirectory)
		a.action = actionImportImages
		a.screen = screenImportFile

	case 4: // Import Volumes
		a.filePicker = NewFilePicker(defaultFilePickerDir(), ".tar", listHeight(a.windowHeight), pickerModeDirectory)
		a.action = actionImportVolumes
		a.screen = screenImportFile

	case 5: // Quit
		return a, tea.Quit
	}
	return a, nil
}

func loadImagesCmd(dc *dockerclient.Client) tea.Cmd {
	return func() tea.Msg {
		imgs, err := dc.ListImages(context.Background())
		return imageListLoadedMsg{
			images:       imgs,
			err:          err,
			sourceAction: actionExportImages,
			title:        "Select Images to Export",
		}
	}
}

func loadRunningContainerImagesCmd(dc *dockerclient.Client) tea.Cmd {
	return func() tea.Msg {
		imgs, err := dc.ListRunningContainerImages(context.Background())
		return imageListLoadedMsg{
			images:       imgs,
			err:          err,
			sourceAction: actionExportRunningContainerImages,
			title:        "Select Running Container Images to Export",
			selectAll:    true,
		}
	}
}

func loadVolumesCmd(dc *dockerclient.Client) tea.Cmd {
	return func() tea.Msg {
		vols, err := dc.ListVolumes(context.Background())
		return volumeListLoadedMsg{volumes: vols, err: err}
	}
}

func (a *App) handleImageListLoaded(msg imageListLoadedMsg) (tea.Model, tea.Cmd) {
	if a.screen != screenLoading || a.action != msg.sourceAction {
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
	a.multiSelect = NewMultiSelect(msg.title, items, listHeight(a.windowHeight))
	if msg.selectAll {
		a.multiSelect = a.multiSelect.SelectAll()
	}
	a.action = msg.sourceAction
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

		if a.action == actionExportImages || a.action == actionExportRunningContainerImages {
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
		destDir, err := resolvePathInput(a.inputBuffer)
		if err != nil {
			a.errMsg = "Cannot resolve destination directory: " + err.Error()
			a.screen = screenError
			return a, nil
		}
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			a.errMsg = "Cannot create destination directory: " + err.Error()
			a.screen = screenError
			return a, nil
		}
		return a.startExport(destDir)
	case "esc":
		a.screen = screenMenu
	case "backspace", "ctrl+h":
		runes := []rune(a.inputBuffer)
		if len(runes) > 0 {
			a.inputBuffer = string(runes[:len(runes)-1])
		}
	case "ctrl+u":
		a.inputBuffer = ""
	default:
		if msg.Type == tea.KeySpace {
			a.inputBuffer += " "
		} else if msg.Type == tea.KeyRunes {
			a.inputBuffer += string(msg.Runes)
		}
	}
	return a, nil
}

func (a *App) startExport(destDir string) (tea.Model, tea.Cmd) {
	progressCh := make(chan operations.ExportProgress)
	a.exportProgressCh = progressCh
	a.exportProgress = exportProgressState{}
	a.screen = screenExportProgress

	if a.action == actionExportImages || a.action == actionExportRunningContainerImages {
		ids := make([]string, len(a.selectedImageIndices))
		names := make([]string, len(a.selectedImageIndices))
		for i, idx := range a.selectedImageIndices {
			ids[i] = a.images[idx].ID
			names[i] = a.images[idx].DisplayName()
		}
		go func() {
			defer close(progressCh)
			operations.ExportImagesWithProgress(context.Background(), a.dc, ids, names, destDir, func(progress operations.ExportProgress) {
				progressCh <- progress
			})
		}()
	} else {
		names := make([]string, len(a.selectedVolumeIndices))
		for i, idx := range a.selectedVolumeIndices {
			names[i] = a.volumes[idx].Name
		}
		go func() {
			defer close(progressCh)
			operations.ExportVolumesWithProgress(context.Background(), a.dc, names, destDir, func(progress operations.ExportProgress) {
				progressCh <- progress
			})
		}()
	}

	return a, tea.Batch(waitExportProgressCmd(progressCh), exportSpinnerTickCmd())
}

func waitExportProgressCmd(progressCh <-chan operations.ExportProgress) tea.Cmd {
	return func() tea.Msg {
		progress, ok := <-progressCh
		if !ok {
			return exportProgressMsg{progress: operations.ExportProgress{Done: true}}
		}
		return exportProgressMsg{progress: progress}
	}
}

func (a *App) handleExportProgress(msg exportProgressMsg) (tea.Model, tea.Cmd) {
	if a.screen != screenExportProgress {
		return a, nil
	}

	progress := msg.progress
	if progress.Total > 0 {
		a.exportProgress.total = progress.Total
	}
	if progress.Name != "" {
		a.exportProgress.current = progress.Name
	}
	if progress.Index >= 0 {
		a.exportProgress.completed = progress.Index
	}
	if progress.HasResult {
		if progress.Result.Err != nil {
			a.exportProgress.lines = append(a.exportProgress.lines, styleError.Render("✗ "+progress.Result.Name+": "+progress.Result.Err.Error()))
		} else {
			a.exportProgress.lines = append(a.exportProgress.lines, styleSuccess.Render("✔ "+progress.Result.Name+" → "+progress.Result.FilePath))
		}
	}
	if progress.Done {
		a.results = a.exportProgress.lines
		a.screen = screenResults
		return a, nil
	}

	return a, waitExportProgressCmd(a.exportProgressCh)
}

func exportSpinnerTickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		return exportSpinnerTickMsg{}
	})
}

func (a *App) handleExportSpinnerTick() (tea.Model, tea.Cmd) {
	if a.screen != screenExportProgress {
		return a, nil
	}
	a.exportProgress.spinner++
	return a, exportSpinnerTickCmd()
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
		dir := a.filePicker.Chosen()
		files, err := listTarFiles(dir)
		if err != nil {
			a.errMsg = "Cannot list import files: " + err.Error()
			a.screen = screenError
			return a, nil
		}
		a.tarFiles = files
		items := make([]SelectableItem, len(files))
		for i, file := range files {
			items[i] = file
		}
		a.multiSelect = NewMultiSelect("Select Tar Files to Import", items, listHeight(a.windowHeight)).SelectAll()
		a.screen = screenImportFileList
	}

	return a, cmd
}

func (a *App) handleImportFileList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		a.selectedTarFileIndices = indices
		a.runImport()
	}

	return a, cmd
}

func (a *App) runImport() {
	filePaths := make([]string, len(a.selectedTarFileIndices))
	volumeNames := make([]string, len(a.selectedTarFileIndices))
	for i, idx := range a.selectedTarFileIndices {
		file := a.tarFiles[idx]
		filePaths[i] = file.Path
		volumeNames[i] = volumeNameFromTar(file.Name)
	}

	ctx := context.Background()
	var results []operations.ImportResult
	if a.action == actionImportImages {
		results = operations.ImportImages(ctx, a.dc, filePaths)
	} else {
		results = operations.ImportVolumes(ctx, a.dc, filePaths, volumeNames)
	}

	lines := make([]string, 0, len(results))
	for _, r := range results {
		if r.Err != nil {
			lines = append(lines, styleError.Render("✗ "+r.Name+": "+r.Err.Error()))
		} else if a.action == actionImportImages {
			lines = append(lines, styleSuccess.Render("✔ Loaded: "+r.Name))
		} else {
			lines = append(lines, styleSuccess.Render("✔ Imported into volume: "+r.Name))
		}
	}
	a.results = lines
	a.screen = screenResults
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
		sb.WriteString(styleMuted.Render("  Current: "+defaultFilePickerDir()) + "\n")
		sb.WriteString(styleSelected.Render("  > "+a.inputBuffer+"█") + "\n")
		sb.WriteString(styleHelp.Render("\n  enter confirm  •  backspace delete  •  ctrl+u clear  •  esc cancel"))

	case screenExportProgress:
		sb.WriteString(styleTitle.Render("Exporting") + "\n\n")
		total := a.exportProgress.total
		completed := a.exportProgress.completed
		if total == 0 {
			total = 1
		}
		sb.WriteString(styleNormal.Render(fmt.Sprintf("  Working %s  %d/%d complete", spinnerFrame(a.exportProgress.spinner), completed, total)) + "\n")
		sb.WriteString("  " + renderProgressBar(completed, total, 30) + "\n")
		if a.exportProgress.current != "" && completed < total {
			sb.WriteString(styleMuted.Render("  Current: "+a.exportProgress.current) + "\n")
		}
		if len(a.exportProgress.lines) > 0 {
			sb.WriteString("\n")
			start := len(a.exportProgress.lines) - 6
			if start < 0 {
				start = 0
			}
			for _, line := range a.exportProgress.lines[start:] {
				sb.WriteString("  " + line + "\n")
			}
		}
		sb.WriteString(styleHelp.Render("\n  Please wait  •  ctrl+c quit"))

	case screenImportFile:
		sb.WriteString(a.filePicker.View())

	case screenImportFileList:
		sb.WriteString(a.multiSelect.View())

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
	size := "unknown"
	if v.vol.Size >= 0 {
		size = units.HumanSize(float64(v.vol.Size))
	}
	refCount := "unknown"
	if v.vol.RefCount >= 0 {
		refCount = fmt.Sprintf("%d", v.vol.RefCount)
	}
	return fmt.Sprintf("Driver: %s  Size: %s  Ref: %s  Mount: %s", v.vol.Driver, size, refCount, v.vol.Mountpoint)
}

type tarFile struct {
	Name string
	Path string
	Size int64
}

func (t tarFile) DisplayName() string { return t.Name }
func (t tarFile) SubText() string {
	return fmt.Sprintf("Size: %s  Path: %s", units.HumanSize(float64(t.Size)), t.Path)
}

func listHeight(windowHeight int) int {
	if windowHeight > 20 {
		return windowHeight - 10
	}
	return 10
}

func defaultFilePickerDir() string {
	wd, err := os.Getwd()
	home, homeErr := os.UserHomeDir()
	if err == nil && wd != "" && (homeErr != nil || filepath.Clean(wd) != filepath.Clean(home)) {
		return wd
	}

	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		if exeDir != "." && exeDir != "" {
			return exeDir
		}
	}

	if err == nil && wd != "" {
		return wd
	}
	if homeErr == nil && home != "" {
		return home
	}
	return "."
}

func listTarFiles(dir string) ([]tarFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	files := make([]tarFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".tar") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		files = append(files, tarFile{
			Name: entry.Name(),
			Path: filepath.Join(dir, entry.Name()),
			Size: info.Size(),
		})
	}

	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
	})

	return files, nil
}

func volumeNameFromTar(name string) string {
	name = strings.TrimSuffix(name, filepath.Ext(name))
	name = strings.TrimSpace(name)
	if name == "" {
		return "imported-volume"
	}
	return name
}

func resolvePathInput(input string) (string, error) {
	path := strings.TrimSpace(input)
	if path == "" {
		path = "."
	}
	path = os.ExpandEnv(path)
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			path = home
		} else if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
			path = filepath.Join(home, path[2:])
		}
	}
	if !filepath.IsAbs(path) {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		path = filepath.Join(wd, path)
	}
	return filepath.Clean(path), nil
}

func spinnerFrame(index int) string {
	frames := []string{"|", "/", "-", "\\"}
	return frames[index%len(frames)]
}

func renderProgressBar(completed, total, width int) string {
	if width < 1 {
		width = 1
	}
	if total < 1 {
		total = 1
	}
	if completed < 0 {
		completed = 0
	}
	if completed > total {
		completed = total
	}

	filled := completed * width / total
	return styleSelected.Render("["+strings.Repeat("=", filled)+strings.Repeat(" ", width-filled)+"]") +
		styleMuted.Render(fmt.Sprintf(" %d%%", completed*100/total))
}
