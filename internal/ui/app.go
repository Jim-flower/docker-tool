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
	screenContainerList
	screenExportDest
	screenExportMode
	screenExportProgress
	screenImportFile
	screenImportFileList
	screenImportProgress
	screenImportVolumeName
	screenComposeFile
	screenConfirm
	screenRemoveProgress
	screenResults
	screenError
)

// action tracks what the user intends to do with selected items.
type action int

const (
	actionNone action = iota
	actionExportImages
	actionExportRunningContainerImages
	actionExportComposeImages
	actionExportVolumes
	actionExportComposeVolumes
	actionImportImages
	actionImportVolumes
	actionRemoveImages
	actionRemoveVolumes
	actionRemoveContainers
)

// isImageExport reports whether the action exports images (and therefore reads
// the image selection).
func (a action) isImageExport() bool {
	return a == actionExportImages || a == actionExportRunningContainerImages || a == actionExportComposeImages
}

// isRemove reports whether the action deletes Docker objects.
func (a action) isRemove() bool {
	return a == actionRemoveImages || a == actionRemoveVolumes || a == actionRemoveContainers
}

// App is the root Bubbletea model.
type App struct {
	dc      *dockerclient.Client
	screen  screen
	action  action
	menuIdx int

	// data
	images     []dockerclient.Image
	volumes    []dockerclient.Volume
	containers []dockerclient.Container
	tarFiles   []tarFile

	// images referenced by running containers, keyed by image ID; used to warn
	// before an image is deleted out from under a live container.
	runningImageIDs map[string]struct{}

	// sub-models
	multiSelect MultiSelectModel
	filePicker  FilePicker
	inputBuffer string

	// operation state
	selectedImageIndices     []int
	selectedVolumeIndices    []int
	selectedContainerIndices []int
	selectedTarFileIndices   []int
	importFilePath           string
	results                  []string
	composeWarnings          []string
	errMsg                   string
	loadingMsg               string
	exportProgress           exportProgressState
	exportProgressCh         <-chan operations.ExportProgress
	importProgress           importProgressState
	importProgressCh         <-chan operations.ImportProgress
	removeProgress           removeProgressState
	removeProgressCh         <-chan operations.RemoveProgress

	// export bundle state
	pendingDestDir string
	exportAppend   bool
	bundleStatus   operations.BundleStatus
	exportModeIdx  int

	// confirmation state for destructive batch operations
	confirm confirmState

	windowWidth  int
	windowHeight int
}

// confirmState describes a destructive batch operation awaiting confirmation.
type confirmState struct {
	title      string
	rows       [][2]string // label / value pairs shown as an impact summary
	warnings   []string
	allowForce bool
	force      bool
	forceLabel string
}

type imageListLoadedMsg struct {
	images       []dockerclient.Image
	err          error
	sourceAction action
	title        string
	preselect    []int
	badges       map[int]string
	runningIDs   map[string]struct{}
}

type volumeListLoadedMsg struct {
	volumes      []dockerclient.Volume
	err          error
	sourceAction action
	title        string
	badges       map[int]string
}

type containerListLoadedMsg struct {
	containers []dockerclient.Container
	err        error
	preselect  []int
	badges     map[int]string
}

type exportProgressState struct {
	current      string
	completed    int
	total        int
	lines        []string
	spinner      int
	bytesWritten int64
}

type exportProgressMsg struct {
	progress operations.ExportProgress
}

type exportSpinnerTickMsg struct{}

type importProgressState struct {
	current   string
	completed int
	total     int
	lines     []string
	spinner   int
}

type importProgressMsg struct {
	progress operations.ImportProgress
}

type importSpinnerTickMsg struct{}

type removeProgressState struct {
	title     string
	current   string
	completed int
	total     int
	lines     []string
	spinner   int
}

type removeProgressMsg struct {
	progress operations.RemoveProgress
}

type removeSpinnerTickMsg struct{}

// menuItem is one row of the main menu. An item carries the section header it
// starts (empty for the items that follow it) or a divider, so the menu can
// grow without hard-coded index ranges in the view.
type menuItem struct {
	icon    string
	label   string
	section string
	divider bool
}

// menuItems order is significant: activateMenuItem switches on the index.
var menuItems = []menuItem{
	{icon: "📦", label: "Export Images", section: "EXPORT"},
	{icon: "▶", label: "Export Running Container Images"},
	{icon: "🧩", label: "Export Images from Compose File"},
	{icon: "🗄", label: "Export Volumes"},
	{icon: "🗃", label: "Export Volumes from Compose File"},
	{icon: "📥", label: "Import Images", section: "IMPORT"},
	{icon: "📥", label: "Import Volumes"},
	{icon: "🗑", label: "Remove Images", section: "CLEANUP"},
	{icon: "🗄", label: "Remove Volumes"},
	{icon: "🧹", label: "Remove Containers"},
	{icon: "🚪", label: "Quit", divider: true},
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

	case containerListLoadedMsg:
		return a.handleContainerListLoaded(msg)

	case removeProgressMsg:
		return a.handleRemoveProgress(msg)

	case removeSpinnerTickMsg:
		return a.handleRemoveSpinnerTick()

	case exportProgressMsg:
		return a.handleExportProgress(msg)

	case exportSpinnerTickMsg:
		return a.handleExportSpinnerTick()

	case importProgressMsg:
		return a.handleImportProgress(msg)

	case importSpinnerTickMsg:
		return a.handleImportSpinnerTick()

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
	case screenImageList, screenVolumeList, screenContainerList:
		return a.handleList(msg)
	case screenExportDest:
		return a.handleExportDest(msg)
	case screenExportMode:
		return a.handleExportMode(msg)
	case screenExportProgress:
		if msg.String() == "ctrl+c" {
			return a, tea.Quit
		}
	case screenConfirm:
		return a.handleConfirm(msg)
	case screenRemoveProgress:
		if msg.String() == "ctrl+c" {
			return a, tea.Quit
		}
	case screenImportFile:
		return a.handleImportFile(msg)
	case screenImportFileList:
		return a.handleImportFileList(msg)
	case screenImportProgress:
		if msg.String() == "ctrl+c" {
			return a, tea.Quit
		}
	case screenImportVolumeName:
		return a.handleImportVolumeName(msg)
	case screenComposeFile:
		return a.handleComposeFile(msg)
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

	case 2: // Export Images from Compose File
		a.filePicker = NewFilePicker(defaultFilePickerDir(), ".yml,.yaml", listHeight(a.windowHeight), pickerModeFile)
		a.filePicker.Title = "Select Compose File"
		a.action = actionExportComposeImages
		a.composeWarnings = nil
		a.screen = screenComposeFile

	case 3: // Export Volumes
		a.action = actionExportVolumes
		a.loadingMsg = "Loading Docker volumes..."
		a.screen = screenLoading
		return a, loadVolumesCmd(a.dc)

	case 4: // Export Volumes from Compose File
		a.filePicker = NewFilePicker(defaultFilePickerDir(), ".yml,.yaml", listHeight(a.windowHeight), pickerModeFile)
		a.filePicker.Title = "Select Compose File"
		a.action = actionExportComposeVolumes
		a.composeWarnings = nil
		a.screen = screenComposeFile

	case 5: // Import Images
		a.filePicker = NewFilePicker(defaultFilePickerDir(), ".tar", listHeight(a.windowHeight), pickerModeDirectory)
		a.action = actionImportImages
		a.screen = screenImportFile

	case 6: // Import Volumes
		a.filePicker = NewFilePicker(defaultFilePickerDir(), ".tar", listHeight(a.windowHeight), pickerModeDirectory)
		a.action = actionImportVolumes
		a.screen = screenImportFile

	case 7: // Remove Images
		a.action = actionRemoveImages
		a.loadingMsg = "Loading Docker images..."
		a.screen = screenLoading
		return a, loadImagesForRemovalCmd(a.dc)

	case 8: // Remove Volumes
		a.action = actionRemoveVolumes
		a.loadingMsg = "Loading Docker volumes..."
		a.screen = screenLoading
		return a, loadVolumesForRemovalCmd(a.dc)

	case 9: // Remove Containers
		a.action = actionRemoveContainers
		a.loadingMsg = "Loading Docker containers..."
		a.screen = screenLoading
		return a, loadContainersForRemovalCmd(a.dc)

	case 10: // Quit
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
		fail := func(err error) imageListLoadedMsg {
			return imageListLoadedMsg{err: err, sourceAction: actionExportRunningContainerImages}
		}
		running, err := dc.ListRunningContainerImages(context.Background())
		if err != nil {
			return fail(err)
		}
		all, err := dc.ListImages(context.Background())
		if err != nil {
			return fail(err)
		}

		runningIDs := make(map[string]struct{}, len(running))
		for _, img := range running {
			runningIDs[img.ID] = struct{}{}
		}
		rest := make([]dockerclient.Image, 0, len(all)-len(running))
		for _, img := range all {
			if _, ok := runningIDs[img.ID]; !ok {
				rest = append(rest, img)
			}
		}
		sort.Slice(rest, func(i, j int) bool {
			return rest[i].DisplayName() < rest[j].DisplayName()
		})

		// Running container images come first and are pre-selected; the user
		// can additionally pick any other local image from the same list.
		images := append(append([]dockerclient.Image{}, running...), rest...)
		preselect := make([]int, len(running))
		for i := range running {
			preselect[i] = i
		}
		return imageListLoadedMsg{
			images:       images,
			sourceAction: actionExportRunningContainerImages,
			title:        "Select Images to Export (running containers pre-selected)",
			preselect:    preselect,
		}
	}
}

func loadVolumesCmd(dc *dockerclient.Client) tea.Cmd {
	return func() tea.Msg {
		vols, err := dc.ListVolumes(context.Background())
		return volumeListLoadedMsg{
			volumes:      vols,
			err:          err,
			sourceAction: actionExportVolumes,
			title:        "Select Volumes to Export",
		}
	}
}

// loadImagesForRemovalCmd lists every image and pre-selects the dangling
// (untagged) ones, the usual reclaim targets, while flagging images that a
// running container still holds.
func loadImagesForRemovalCmd(dc *dockerclient.Client) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		imgs, err := dc.ListImages(ctx)
		if err != nil {
			return imageListLoadedMsg{err: err, sourceAction: actionRemoveImages}
		}

		// Losing this only costs the "in use" hints, so a failure is not fatal.
		runningIDs := map[string]struct{}{}
		if running, err := dc.ListRunningContainerImages(ctx); err == nil {
			for _, img := range running {
				runningIDs[img.ID] = struct{}{}
			}
		}

		badges := make(map[int]string, len(imgs))
		var preselect []int
		for i, img := range imgs {
			var marks []string
			if img.Dangling() {
				marks = append(marks, "⚠ dangling")
				preselect = append(preselect, i)
			}
			if _, ok := runningIDs[img.ID]; ok {
				marks = append(marks, "● in use")
			}
			badges[i] = strings.Join(marks, "  ")
		}

		return imageListLoadedMsg{
			images:       imgs,
			sourceAction: actionRemoveImages,
			title:        "Select Images to Remove (dangling pre-selected)",
			preselect:    preselect,
			badges:       badges,
			runningIDs:   runningIDs,
		}
	}
}

// loadVolumesForRemovalCmd lists volumes and flags the ones a container still
// references, which Docker refuses to delete.
func loadVolumesForRemovalCmd(dc *dockerclient.Client) tea.Cmd {
	return func() tea.Msg {
		vols, err := dc.ListVolumes(context.Background())
		if err != nil {
			return volumeListLoadedMsg{err: err, sourceAction: actionRemoveVolumes}
		}
		badges := make(map[int]string, len(vols))
		for i, vol := range vols {
			if vol.RefCount > 0 {
				badges[i] = fmt.Sprintf("● in use by %d container(s)", vol.RefCount)
			}
		}
		return volumeListLoadedMsg{
			volumes:      vols,
			sourceAction: actionRemoveVolumes,
			title:        "Select Volumes to Remove",
			badges:       badges,
		}
	}
}

// loadContainersForRemovalCmd lists containers (stopped ones first) and
// pre-selects everything that is not running.
func loadContainersForRemovalCmd(dc *dockerclient.Client) tea.Cmd {
	return func() tea.Msg {
		containers, err := dc.ListContainers(context.Background(), true)
		if err != nil {
			return containerListLoadedMsg{err: err}
		}
		badges := make(map[int]string, len(containers))
		var preselect []int
		for i, ctr := range containers {
			if ctr.IsRunning() {
				badges[i] = "● running"
				continue
			}
			badges[i] = "○ stopped"
			preselect = append(preselect, i)
		}
		return containerListLoadedMsg{containers: containers, preselect: preselect, badges: badges}
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
	a.runningImageIDs = msg.runningIDs
	preselected := make(map[int]struct{}, len(msg.preselect))
	for _, idx := range msg.preselect {
		preselected[idx] = struct{}{}
	}
	items := make([]SelectableItem, len(msg.images))
	for i, img := range msg.images {
		item := imageItem{img: img}
		if msg.badges != nil {
			item.badge = msg.badges[i]
		} else if _, ok := preselected[i]; ok {
			item.badge = "● running"
		}
		items[i] = item
	}
	a.multiSelect = NewMultiSelect(msg.title, items, listHeight(a.windowHeight))
	if len(msg.preselect) > 0 {
		a.multiSelect = a.multiSelect.Select(msg.preselect...)
	}
	a.action = msg.sourceAction
	a.screen = screenImageList
	return a, nil
}

func (a *App) handleVolumeListLoaded(msg volumeListLoadedMsg) (tea.Model, tea.Cmd) {
	if a.screen != screenLoading || a.action != msg.sourceAction {
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
		item := volumeItem{vol: v}
		if msg.badges != nil {
			item.badge = msg.badges[i]
		}
		items[i] = item
	}
	a.multiSelect = NewMultiSelect(msg.title, items, listHeight(a.windowHeight))
	a.action = msg.sourceAction
	a.screen = screenVolumeList
	return a, nil
}

func (a *App) handleContainerListLoaded(msg containerListLoadedMsg) (tea.Model, tea.Cmd) {
	if a.screen != screenLoading || a.action != actionRemoveContainers {
		return a, nil
	}
	if msg.err != nil {
		a.errMsg = msg.err.Error()
		a.screen = screenError
		return a, nil
	}
	a.containers = msg.containers
	items := make([]SelectableItem, len(msg.containers))
	for i, ctr := range msg.containers {
		item := containerItem{ctr: ctr}
		if msg.badges != nil {
			item.badge = msg.badges[i]
		}
		items[i] = item
	}
	a.multiSelect = NewMultiSelect("Select Containers to Remove (stopped pre-selected)", items, listHeight(a.windowHeight))
	if len(msg.preselect) > 0 {
		a.multiSelect = a.multiSelect.Select(msg.preselect...)
	}
	a.screen = screenContainerList
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

		switch {
		case a.action.isImageExport():
			a.selectedImageIndices = indices
		case a.action == actionRemoveImages:
			a.selectedImageIndices = indices
			return a.confirmRemoveImages()
		case a.action == actionRemoveVolumes:
			a.selectedVolumeIndices = indices
			return a.confirmRemoveVolumes()
		case a.action == actionRemoveContainers:
			a.selectedContainerIndices = indices
			return a.confirmRemoveContainers()
		default:
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
		a.pendingDestDir = destDir
		// An existing bundle is grown rather than silently overwritten, but the
		// choice is always explicit so a clean re-export stays possible.
		if status := operations.InspectBundle(destDir); status.Exists() {
			a.bundleStatus = status
			a.exportModeIdx = 0
			a.screen = screenExportMode
			return a, nil
		}
		a.exportAppend = false
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

// handleExportMode picks between growing an existing export bundle and
// rebuilding its import scripts from this selection alone.
func (a *App) handleExportMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if a.exportModeIdx > 0 {
			a.exportModeIdx--
		}
	case "down", "j":
		if a.exportModeIdx < 1 {
			a.exportModeIdx++
		}
	case "enter":
		a.exportAppend = a.exportModeIdx == 0
		return a.startExport(a.pendingDestDir)
	case "esc":
		a.screen = screenExportDest
	}
	return a, nil
}

func (a *App) startExport(destDir string) (tea.Model, tea.Cmd) {
	// Buffered so byte-progress bursts from concurrent workers don't stall exports.
	progressCh := make(chan operations.ExportProgress, 8)
	a.exportProgressCh = progressCh
	a.exportProgress = exportProgressState{}
	a.screen = screenExportProgress

	// IsProgress messages are best-effort; drop rather than block the exporting goroutine.
	callback := func(progress operations.ExportProgress) {
		if progress.IsProgress {
			select {
			case progressCh <- progress:
			default:
			}
		} else {
			progressCh <- progress
		}
	}

	appendMode := a.exportAppend
	if a.action.isImageExport() {
		ids := make([]string, len(a.selectedImageIndices))
		names := make([]string, len(a.selectedImageIndices))
		for i, idx := range a.selectedImageIndices {
			ids[i] = a.images[idx].ID
			names[i] = a.images[idx].DisplayName()
		}
		go func() {
			defer close(progressCh)
			if appendMode {
				operations.ExportImagesAppendWithProgress(context.Background(), a.dc, ids, names, destDir, callback)
				return
			}
			operations.ExportImagesWithProgress(context.Background(), a.dc, ids, names, destDir, callback)
		}()
	} else {
		names := make([]string, len(a.selectedVolumeIndices))
		for i, idx := range a.selectedVolumeIndices {
			names[i] = a.volumes[idx].Name
		}
		go func() {
			defer close(progressCh)
			if appendMode {
				operations.ExportVolumesAppendWithProgress(context.Background(), a.dc, names, destDir, callback)
				return
			}
			operations.ExportVolumesWithProgress(context.Background(), a.dc, names, destDir, callback)
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

	if progress.IsProgress {
		a.exportProgress.bytesWritten = progress.BytesWritten
		return a, waitExportProgressCmd(a.exportProgressCh)
	}

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
		a.exportProgress.bytesWritten = 0
		if progress.Result.Err != nil {
			a.exportProgress.lines = append(a.exportProgress.lines, styleError.Render("✗ "+progress.Result.Name+": "+progress.Result.Err.Error()))
		} else {
			a.exportProgress.lines = append(a.exportProgress.lines, styleSuccess.Render("✔ "+progress.Result.Name+" → "+progress.Result.FilePath))
		}
	}
	if progress.Done {
		if progress.ScriptErr != nil {
			a.exportProgress.lines = append(a.exportProgress.lines, styleError.Render("✗ import script: "+progress.ScriptErr.Error()))
		} else if progress.ScriptPath != "" {
			a.exportProgress.lines = append(a.exportProgress.lines, styleSuccess.Render("✔ Import script → "+progress.ScriptPath))
		}
		a.results = a.exportProgress.lines
		isComposeExport := a.action == actionExportComposeImages || a.action == actionExportComposeVolumes
		if isComposeExport && len(a.composeWarnings) > 0 {
			a.results = append(append([]string{}, a.composeWarnings...), a.results...)
		}
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

// ---- Compose file picker ----

func (a *App) handleComposeFile(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	updated, cmd := a.filePicker.Update(msg)
	a.filePicker = updated

	if a.filePicker.IsCanceled() {
		a.screen = screenMenu
		return a, nil
	}

	if !a.filePicker.IsChosen() {
		return a, cmd
	}

	path := a.filePicker.Chosen()
	if a.action == actionExportComposeVolumes {
		return a.loadComposeVolumes(path)
	}
	return a.loadComposeImages(path)
}

func (a *App) loadComposeImages(path string) (tea.Model, tea.Cmd) {
	refs, err := dockerclient.ParseComposeImages(path)
	if err != nil {
		a.errMsg = err.Error()
		a.screen = screenError
		return a, nil
	}
	matched, missing, err := a.dc.MatchImagesByRef(context.Background(), refs)
	if err != nil {
		a.errMsg = "Cannot list local images: " + err.Error()
		a.screen = screenError
		return a, nil
	}
	if len(matched) == 0 {
		a.errMsg = "None of the compose file's images exist locally. Pull them first (docker compose pull)."
		a.screen = screenError
		return a, nil
	}

	a.composeWarnings = nil
	for _, ref := range missing {
		a.composeWarnings = append(a.composeWarnings, styleMuted.Render("⚠ "+ref+" not found locally, skipped"))
	}

	a.images = matched
	items := make([]SelectableItem, len(matched))
	for i, img := range matched {
		items[i] = imageItem{img: img}
	}
	a.multiSelect = NewMultiSelect("Select Compose Images to Export", items, listHeight(a.windowHeight)).SelectAll()
	a.screen = screenImageList
	return a, nil
}

func (a *App) loadComposeVolumes(path string) (tea.Model, tea.Cmd) {
	names, err := dockerclient.ParseComposeVolumes(path)
	if err != nil {
		a.errMsg = err.Error()
		a.screen = screenError
		return a, nil
	}
	matched, missing, err := a.dc.MatchVolumesByName(context.Background(), names)
	if err != nil {
		a.errMsg = "Cannot list local volumes: " + err.Error()
		a.screen = screenError
		return a, nil
	}
	if len(matched) == 0 {
		a.errMsg = "None of the compose file's named volumes exist locally."
		a.screen = screenError
		return a, nil
	}

	a.composeWarnings = nil
	for _, name := range missing {
		a.composeWarnings = append(a.composeWarnings, styleMuted.Render("⚠ volume "+name+" not found locally, skipped"))
	}

	a.volumes = matched
	items := make([]SelectableItem, len(matched))
	for i, v := range matched {
		items[i] = volumeItem{vol: v}
	}
	a.multiSelect = NewMultiSelect("Select Compose Volumes to Export", items, listHeight(a.windowHeight)).SelectAll()
	a.screen = screenVolumeList
	return a, nil
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
		return a.startImport()
	}

	return a, cmd
}

func (a *App) startImport() (tea.Model, tea.Cmd) {
	filePaths := make([]string, len(a.selectedTarFileIndices))
	volumeNames := make([]string, len(a.selectedTarFileIndices))
	for i, idx := range a.selectedTarFileIndices {
		file := a.tarFiles[idx]
		filePaths[i] = file.Path
		volumeNames[i] = volumeNameFromTar(file.DisplayName())
	}

	progressCh := make(chan operations.ImportProgress)
	a.importProgressCh = progressCh
	a.importProgress = importProgressState{}
	a.screen = screenImportProgress

	if a.action == actionImportImages {
		go func() {
			defer close(progressCh)
			operations.ImportImagesWithProgress(context.Background(), a.dc, filePaths, func(progress operations.ImportProgress) {
				progressCh <- progress
			})
		}()
	} else {
		go func() {
			defer close(progressCh)
			operations.ImportVolumesWithProgress(context.Background(), a.dc, filePaths, volumeNames, func(progress operations.ImportProgress) {
				progressCh <- progress
			})
		}()
	}

	return a, tea.Batch(waitImportProgressCmd(progressCh), importSpinnerTickCmd())
}

func waitImportProgressCmd(progressCh <-chan operations.ImportProgress) tea.Cmd {
	return func() tea.Msg {
		progress, ok := <-progressCh
		if !ok {
			return importProgressMsg{progress: operations.ImportProgress{Done: true}}
		}
		return importProgressMsg{progress: progress}
	}
}

func (a *App) handleImportProgress(msg importProgressMsg) (tea.Model, tea.Cmd) {
	if a.screen != screenImportProgress {
		return a, nil
	}

	progress := msg.progress
	if progress.Total > 0 {
		a.importProgress.total = progress.Total
	}
	if progress.Name != "" {
		a.importProgress.current = progress.Name
	}
	if progress.Index >= 0 {
		a.importProgress.completed = progress.Index
	}
	if progress.HasResult {
		if progress.Result.Err != nil {
			a.importProgress.lines = append(a.importProgress.lines, styleError.Render("✗ "+progress.Result.Name+": "+progress.Result.Err.Error()))
		} else if a.action == actionImportImages {
			a.importProgress.lines = append(a.importProgress.lines, styleSuccess.Render("✔ Loaded: "+progress.Result.Name))
		} else {
			a.importProgress.lines = append(a.importProgress.lines, styleSuccess.Render("✔ Imported into volume: "+progress.Result.Name))
		}
	}
	if progress.Done {
		a.results = a.importProgress.lines
		a.screen = screenResults
		return a, nil
	}

	return a, waitImportProgressCmd(a.importProgressCh)
}

func importSpinnerTickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		return importSpinnerTickMsg{}
	})
}

func (a *App) handleImportSpinnerTick() (tea.Model, tea.Cmd) {
	if a.screen != screenImportProgress {
		return a, nil
	}
	a.importProgress.spinner++
	return a, importSpinnerTickCmd()
}

// ---- Destructive batch operations ----

func (a *App) confirmRemoveImages() (tea.Model, tea.Cmd) {
	var totalSize int64
	dangling, running, multiTag := 0, 0, 0
	for _, idx := range a.selectedImageIndices {
		img := a.images[idx]
		totalSize += img.Size
		if img.Dangling() {
			dangling++
		}
		if img.TagCount() > 1 {
			multiTag++
		}
		if _, ok := a.runningImageIDs[img.ID]; ok {
			running++
		}
	}

	rows := [][2]string{
		{"Items", fmt.Sprintf("%d image(s)", len(a.selectedImageIndices))},
		{"Reclaimable", units.HumanSize(float64(totalSize))},
	}
	if dangling > 0 {
		rows = append(rows, [2]string{"Dangling", fmt.Sprintf("%d", dangling)})
	}
	if running > 0 {
		rows = append(rows, [2]string{"In use", fmt.Sprintf("%d", running)})
	}
	if multiTag > 0 {
		rows = append(rows, [2]string{"Multi-tag", fmt.Sprintf("%d", multiTag)})
	}

	var warnings []string
	if running > 0 {
		warnings = append(warnings, fmt.Sprintf("%d image(s) are held by running containers; remove or stop those containers first, or they will fail.", running))
	}
	if multiTag > 0 {
		warnings = append(warnings, fmt.Sprintf("%d image(s) carry several tags; deleting them drops every tag.", multiTag))
	}
	if dangling > 0 {
		warnings = append(warnings, fmt.Sprintf("%d dangling image(s) are untagged build cache; removing them only costs rebuild time.", dangling))
	}

	a.confirm = confirmState{
		title:      "Delete Docker Images",
		rows:       rows,
		warnings:   warnings,
		allowForce: true,
		forceLabel: "also remove images referenced by stopped containers",
	}
	a.screen = screenConfirm
	return a, nil
}

func (a *App) confirmRemoveVolumes() (tea.Model, tea.Cmd) {
	var totalSize int64
	unknownSize, inUse := 0, 0
	for _, idx := range a.selectedVolumeIndices {
		vol := a.volumes[idx]
		if vol.Size >= 0 {
			totalSize += vol.Size
		} else {
			unknownSize++
		}
		if vol.RefCount > 0 {
			inUse++
		}
	}

	size := units.HumanSize(float64(totalSize))
	if unknownSize > 0 {
		size += fmt.Sprintf(" (+%d unknown)", unknownSize)
	}
	rows := [][2]string{
		{"Items", fmt.Sprintf("%d volume(s)", len(a.selectedVolumeIndices))},
		{"Size", size},
	}
	if inUse > 0 {
		rows = append(rows, [2]string{"In use", fmt.Sprintf("%d", inUse)})
	}

	warnings := []string{
		"Volume data is deleted permanently. Docker refuses to remove a volume still referenced by a container.",
	}
	if inUse > 0 {
		warnings = append(warnings, fmt.Sprintf("%d selected volume(s) are referenced by containers and will fail.", inUse))
	}

	a.confirm = confirmState{title: "Delete Docker Volumes", rows: rows, warnings: warnings}
	a.screen = screenConfirm
	return a, nil
}

func (a *App) confirmRemoveContainers() (tea.Model, tea.Cmd) {
	running := 0
	for _, idx := range a.selectedContainerIndices {
		if a.containers[idx].IsRunning() {
			running++
		}
	}

	rows := [][2]string{
		{"Items", fmt.Sprintf("%d container(s)", len(a.selectedContainerIndices))},
		{"Running", fmt.Sprintf("%d", running)},
	}
	warnings := []string{
		"Anonymous volumes attached to these containers are left behind, not deleted.",
	}
	if running > 0 {
		warnings = append(warnings, fmt.Sprintf("%d running container(s) must be stopped first, unless force is enabled.", running))
	}

	a.confirm = confirmState{
		title:      "Remove Docker Containers",
		rows:       rows,
		warnings:   warnings,
		allowForce: true,
		forceLabel: "stop running containers first, then remove them",
	}
	a.screen = screenConfirm
	return a, nil
}

func (a *App) handleConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "f":
		if a.confirm.allowForce {
			a.confirm.force = !a.confirm.force
		}
	case "enter":
		return a.startRemove()
	case "esc":
		a.screen = screenMenu
	}
	return a, nil
}

func (a *App) startRemove() (tea.Model, tea.Cmd) {
	progressCh := make(chan operations.RemoveProgress)
	a.removeProgressCh = progressCh
	a.removeProgress = removeProgressState{title: removeProgressTitle(a.action)}
	a.screen = screenRemoveProgress
	force := a.confirm.force

	emit := func(progress operations.RemoveProgress) { progressCh <- progress }

	switch a.action {
	case actionRemoveImages:
		ids := make([]string, len(a.selectedImageIndices))
		names := make([]string, len(a.selectedImageIndices))
		for i, idx := range a.selectedImageIndices {
			ids[i] = a.images[idx].ID
			names[i] = a.images[idx].DisplayName()
		}
		go func() {
			defer close(progressCh)
			operations.RemoveImagesWithProgress(context.Background(), a.dc, ids, names, force, emit)
		}()

	case actionRemoveVolumes:
		names := make([]string, len(a.selectedVolumeIndices))
		for i, idx := range a.selectedVolumeIndices {
			names[i] = a.volumes[idx].Name
		}
		go func() {
			defer close(progressCh)
			operations.RemoveVolumesWithProgress(context.Background(), a.dc, names, emit)
		}()

	case actionRemoveContainers:
		ids := make([]string, len(a.selectedContainerIndices))
		names := make([]string, len(a.selectedContainerIndices))
		for i, idx := range a.selectedContainerIndices {
			ids[i] = a.containers[idx].ID
			names[i] = a.containers[idx].Name
		}
		go func() {
			defer close(progressCh)
			operations.RemoveContainersWithProgress(context.Background(), a.dc, ids, names, force, emit)
		}()
	}

	return a, tea.Batch(waitRemoveProgressCmd(progressCh), removeSpinnerTickCmd())
}

func removeProgressTitle(a action) string {
	switch a {
	case actionRemoveImages:
		return "Removing Images"
	case actionRemoveVolumes:
		return "Removing Volumes"
	default:
		return "Removing Containers"
	}
}

func waitRemoveProgressCmd(progressCh <-chan operations.RemoveProgress) tea.Cmd {
	return func() tea.Msg {
		progress, ok := <-progressCh
		if !ok {
			return removeProgressMsg{progress: operations.RemoveProgress{Done: true}}
		}
		return removeProgressMsg{progress: progress}
	}
}

func (a *App) handleRemoveProgress(msg removeProgressMsg) (tea.Model, tea.Cmd) {
	if a.screen != screenRemoveProgress {
		return a, nil
	}

	progress := msg.progress
	if progress.Total > 0 {
		a.removeProgress.total = progress.Total
	}
	if progress.Name != "" {
		a.removeProgress.current = progress.Name
	}
	if progress.Index >= 0 {
		a.removeProgress.completed = progress.Index
	}
	if progress.HasResult {
		if progress.Result.Err != nil {
			a.removeProgress.lines = append(a.removeProgress.lines, styleError.Render("✗ "+progress.Result.Name+": "+progress.Result.Err.Error()))
		} else {
			a.removeProgress.lines = append(a.removeProgress.lines, styleSuccess.Render("✔ Removed: "+progress.Result.Name))
		}
	}
	if progress.Done {
		a.results = a.removeProgress.lines
		if len(a.results) == 0 {
			a.results = []string{styleMuted.Render("Nothing matched the selection.")}
		}
		a.screen = screenResults
		return a, nil
	}

	return a, waitRemoveProgressCmd(a.removeProgressCh)
}

func removeSpinnerTickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		return removeSpinnerTickMsg{}
	})
}

func (a *App) handleRemoveSpinnerTick() (tea.Model, tea.Cmd) {
	if a.screen != screenRemoveProgress {
		return a, nil
	}
	a.removeProgress.spinner++
	return a, removeSpinnerTickCmd()
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
	case "backspace", "ctrl+h":
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

	// Full-width header banner
	bannerWidth := max(a.windowWidth, 40)
	sb.WriteString(styleHeader.Width(bannerWidth).Render("  Docker Tool") + "\n\n")

	switch a.screen {
	case screenMenu:
		for i, item := range menuItems {
			switch {
			case item.section != "":
				if i > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(styleSectionLabel.Render("  "+item.section) + "\n")
			case item.divider:
				sb.WriteString("\n" + styleDivider.Render("  "+strings.Repeat("─", 32)) + "\n")
			}
			renderMenuItem(&sb, i, item.icon+"  "+item.label, a.menuIdx)
		}
		sb.WriteString(renderHelpBar("↑↓", "navigate", "enter", "select", "q", "quit"))

	case screenLoading:
		sb.WriteString(styleTitle.Render("Please Wait") + "\n\n")
		sb.WriteString("  " + styleMuted.Render("⣷") + "  " + styleNormal.Render(a.loadingMsg) + "\n")
		sb.WriteString(renderHelpBar("esc", "cancel"))

	case screenImageList, screenVolumeList, screenContainerList:
		sb.WriteString(a.multiSelect.View())

	case screenExportDest:
		sb.WriteString(styleTitle.Render("Export Destination") + "\n\n")
		sb.WriteString(styleNormal.Render("  Destination directory") + "\n")
		sb.WriteString(styleMuted.Render("  leave blank to use: "+defaultFilePickerDir()) + "\n\n")
		sb.WriteString(styleMuted.Render("  ▸ ") + styleInput.Render(a.inputBuffer) + styleMenuCursor.Render("▌") + "\n")
		sb.WriteString(renderHelpBar("enter", "confirm", "ctrl+u", "clear", "esc", "cancel"))

	case screenExportMode:
		sb.WriteString(styleTitle.Render("Existing Export Found") + "\n\n")
		sb.WriteString(styleNormal.Render("  "+a.pendingDestDir) + "\n")
		sb.WriteString(styleMuted.Render(fmt.Sprintf("  already holds %d image(s) and %d volume(s)",
			a.bundleStatus.Images, a.bundleStatus.Volumes)) + "\n\n")

		options := []struct {
			label string
			desc  string
		}{
			{"＋  Append", "keep the existing bundle and add this selection to its import scripts"},
			{"⟳  Overwrite", "rebuild the import scripts from this selection only (old tar files stay on disk)"},
		}
		for i, option := range options {
			if i == a.exportModeIdx {
				sb.WriteString(styleMenuCursor.Render("  ▸ ") + styleMenuItemActive.Render(option.label) + "\n")
			} else {
				sb.WriteString("    " + styleMenuItem.Render(option.label) + "\n")
			}
			sb.WriteString(styleMuted.Render("      "+option.desc) + "\n")
		}
		sb.WriteString(renderHelpBar("↑↓", "choose", "enter", "confirm", "esc", "back"))

	case screenConfirm:
		sb.WriteString(styleError.Render("  ⚠  "+a.confirm.title) + "\n\n")
		for _, row := range a.confirm.rows {
			sb.WriteString("  " + styleMuted.Render(padRight(row[0], 14)) + styleNormal.Render(row[1]) + "\n")
		}
		if len(a.confirm.warnings) > 0 {
			sb.WriteString("\n")
			for _, warning := range a.confirm.warnings {
				sb.WriteString("  " + styleInfo.Render("▲ "+warning) + "\n")
			}
		}
		if a.confirm.allowForce {
			sb.WriteString("\n  ")
			if a.confirm.force {
				sb.WriteString(styleError.Render("Force [ on ]"))
			} else {
				sb.WriteString(styleMuted.Render("Force [ off ]"))
			}
			sb.WriteString(styleMuted.Render("  "+a.confirm.forceLabel) + "\n")
		}
		help := []string{"enter", "confirm"}
		if a.confirm.allowForce {
			help = append(help, "f", "toggle force")
		}
		help = append(help, "esc", "cancel")
		sb.WriteString(renderHelpBar(help...))

	case screenExportProgress:
		sb.WriteString(styleTitle.Render("Exporting") + "\n\n")
		total := a.exportProgress.total
		completed := a.exportProgress.completed
		if total == 0 {
			total = 1
		}
		sb.WriteString(styleNormal.Render(fmt.Sprintf("  %s  %d / %d complete",
			styleCursor.Render(spinnerFrame(a.exportProgress.spinner)), completed, total)) + "\n")
		sb.WriteString("  " + renderProgressBar(completed, total, 32) + "\n")
		if completed < total {
			if a.exportProgress.bytesWritten > 0 {
				label := "  Transferring"
				if a.exportProgress.current != "" {
					label += "  " + styleNormal.Render(a.exportProgress.current)
				}
				label += "  " + styleInfo.Render(units.HumanSize(float64(a.exportProgress.bytesWritten)))
				sb.WriteString(label + "\n")
			} else if a.exportProgress.current != "" {
				sb.WriteString(styleMuted.Render("  Exporting  ") + styleNormal.Render(a.exportProgress.current) + "\n")
			}
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
		sb.WriteString(renderHelpBar("ctrl+c", "quit"))

	case screenImportFile:
		sb.WriteString(a.filePicker.View())

	case screenImportFileList:
		sb.WriteString(a.multiSelect.View())

	case screenImportProgress:
		sb.WriteString(styleTitle.Render("Importing") + "\n\n")
		total := a.importProgress.total
		completed := a.importProgress.completed
		if total == 0 {
			total = 1
		}
		sb.WriteString(styleNormal.Render(fmt.Sprintf("  %s  %d / %d complete",
			styleCursor.Render(spinnerFrame(a.importProgress.spinner)), completed, total)) + "\n")
		sb.WriteString("  " + renderProgressBar(completed, total, 32) + "\n")
		if a.importProgress.current != "" && completed < total {
			sb.WriteString(styleMuted.Render("  Importing  ") + styleNormal.Render(a.importProgress.current) + "\n")
		}
		if len(a.importProgress.lines) > 0 {
			sb.WriteString("\n")
			start := len(a.importProgress.lines) - 6
			if start < 0 {
				start = 0
			}
			for _, line := range a.importProgress.lines[start:] {
				sb.WriteString("  " + line + "\n")
			}
		}
		sb.WriteString(renderHelpBar("ctrl+c", "quit"))

	case screenImportVolumeName:
		sb.WriteString(styleTitle.Render("Volume Name for Import") + "\n\n")
		sb.WriteString(styleMuted.Render("  File: ") + styleInfo.Render(a.importFilePath) + "\n\n")
		sb.WriteString(styleNormal.Render("  Target volume name:") + "\n")
		sb.WriteString(styleMuted.Render("  ▸ ") + styleInput.Render(a.inputBuffer) + styleMenuCursor.Render("▌") + "\n")
		sb.WriteString(renderHelpBar("enter", "confirm", "esc", "cancel"))

	case screenComposeFile:
		sb.WriteString(a.filePicker.View())

	case screenRemoveProgress:
		sb.WriteString(styleTitle.Render(a.removeProgress.title) + "\n\n")
		total := a.removeProgress.total
		completed := a.removeProgress.completed
		if total == 0 {
			total = 1
		}
		sb.WriteString(styleNormal.Render(fmt.Sprintf("  %s  %d / %d complete",
			styleCursor.Render(spinnerFrame(a.removeProgress.spinner)), completed, total)) + "\n")
		sb.WriteString("  " + renderProgressBar(completed, total, 32) + "\n")
		if a.removeProgress.current != "" && completed < total {
			sb.WriteString(styleMuted.Render("  Working  ") + styleNormal.Render(a.removeProgress.current) + "\n")
		}
		if len(a.removeProgress.lines) > 0 {
			sb.WriteString("\n")
			start := len(a.removeProgress.lines) - 6
			if start < 0 {
				start = 0
			}
			for _, line := range a.removeProgress.lines[start:] {
				sb.WriteString("  " + line + "\n")
			}
		}
		sb.WriteString(renderHelpBar("ctrl+c", "quit"))

	case screenResults:
		sb.WriteString(styleTitle.Render("Results") + "\n\n")
		// Summary counts
		ok, fail := 0, 0
		for _, line := range a.results {
			if strings.Contains(line, "✔") {
				ok++
			} else if strings.Contains(line, "✗") {
				fail++
			}
		}
		if ok > 0 || fail > 0 {
			summary := "  "
			if ok > 0 {
				summary += styleSuccess.Render(fmt.Sprintf("✔ %d succeeded", ok))
			}
			if fail > 0 {
				if ok > 0 {
					summary += "   "
				}
				summary += styleError.Render(fmt.Sprintf("✗ %d failed", fail))
			}
			sb.WriteString(summary + "\n\n")
		}
		for _, line := range a.results {
			sb.WriteString("  " + line + "\n")
		}
		sb.WriteString(renderHelpBar("any key", "return to menu"))

	case screenError:
		sb.WriteString(styleError.Render("  ✗  Error") + "\n\n")
		sb.WriteString(styleNormal.Render("  "+a.errMsg) + "\n")
		sb.WriteString(renderHelpBar("any key", "return to menu"))
	}

	return sb.String()
}

// renderMenuItem writes a single menu row.
func renderMenuItem(sb *strings.Builder, idx int, item string, activeIdx int) {
	if idx == activeIdx {
		sb.WriteString(styleMenuCursor.Render("  ▸ ") + styleMenuItemActive.Render(item) + "\n")
	} else {
		sb.WriteString("    " + styleMenuItem.Render(item) + "\n")
	}
}

// padRight pads s with spaces so values line up, guaranteeing one separator
// space even when the label is already the target width or wider.
func padRight(s string, width int) string {
	if n := len([]rune(s)); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s + " "
}

// renderHelpBar renders a key-description help footer.
// Pairs are alternating: key, description, key, description, ...
func renderHelpBar(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		key := styleKeyBind.Render(pairs[i])
		desc := styleKeyDesc.Render(" " + pairs[i+1])
		parts = append(parts, key+desc)
	}
	return styleHelp.Render("\n  " + strings.Join(parts, "   "))
}

// ---- helpers for SelectableItem adapters ----

type imageItem struct {
	img   dockerclient.Image
	badge string
}

func (i imageItem) DisplayName() string { return i.img.DisplayName() }
func (i imageItem) SubText() string {
	s := fmt.Sprintf("ID: %s  Size: %s", i.img.ShortID, units.HumanSize(float64(i.img.Size)))
	if i.badge != "" {
		return i.badge + "  " + s
	}
	return s
}

type volumeItem struct {
	vol   dockerclient.Volume
	badge string
}

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
	line := fmt.Sprintf("Driver: %s  Size: %s  Ref: %s  Mount: %s", v.vol.Driver, size, refCount, v.vol.Mountpoint)
	if v.badge != "" {
		return v.badge + "  " + line
	}
	return line
}

type containerItem struct {
	ctr   dockerclient.Container
	badge string
}

func (c containerItem) DisplayName() string { return c.ctr.Name }
func (c containerItem) SubText() string {
	line := fmt.Sprintf("ID: %s  Image: %s  %s", c.ctr.ShortID, c.ctr.Image, c.ctr.Status)
	if c.badge != "" {
		return c.badge + "  " + line
	}
	return line
}

type tarFile struct {
	Name    string
	Path    string
	RelPath string
	Size    int64
}

func (t tarFile) DisplayName() string {
	if t.RelPath != "" {
		return t.RelPath
	}
	return t.Name
}
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
	files := []tarFile{}
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".tar") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			relPath = entry.Name()
		}
		files = append(files, tarFile{
			Name:    entry.Name(),
			Path:    path,
			RelPath: relPath,
			Size:    info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(files[i].DisplayName()) < strings.ToLower(files[j].DisplayName())
	})

	return files, nil
}

func volumeNameFromTar(name string) string {
	name = strings.TrimSuffix(name, filepath.Ext(name))
	name = strings.ReplaceAll(name, string(filepath.Separator), "_")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
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
	frames := []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}
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
	bar := styleSelected.Render(strings.Repeat("█", filled)) +
		styleMuted.Render(strings.Repeat("░", width-filled))
	pct := completed * 100 / total
	return bar + styleMuted.Render(fmt.Sprintf("  %3d%%", pct))
}
