package operations

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	dockerclient "github.com/jim/dockertool/internal/docker"
)

const (
	maxConcurrentExports = 3

	// imageImportScriptName and volumeImportScriptName are the scripts written
	// into an export directory; they are also how an existing bundle is detected.
	imageImportScriptName  = "import-images.sh"
	volumeImportScriptName = "import-volumes.sh"
	bundleManifestName     = "dockertool-export.json"

	// Markers used to count the entries already present in an import script.
	imageEntryMarker  = `docker load -i "$SCRIPT_DIR"/`
	volumeEntryMarker = `echo "Restoring volume: `

	// Placeholders emitted when a bundle turned out to be empty. They are
	// dropped again as soon as the bundle receives its first item.
	noImagePlaceholder  = `echo "No image archives found in this export."`
	noVolumePlaceholder = `echo "No volume archives found in this export."`

	bundleManifestVersion = 1
)

// ExportResult holds the outcome of a single export operation.
type ExportResult struct {
	Name     string
	FilePath string
	Err      error
}

// ExportProgress reports item-level export progress.
type ExportProgress struct {
	Index        int
	Total        int
	Name         string
	Result       ExportResult
	HasResult    bool
	ScriptPath   string
	ScriptErr    error
	Done         bool
	BytesWritten int64 // non-zero during in-progress byte updates
	IsProgress   bool  // true = byte-transfer update only, not a completion event
}

// progressReader wraps a reader and calls onProgress at most every 250 ms.
type progressReader struct {
	r          io.Reader
	written    int64
	lastReport time.Time
	onProgress func(int64)
}

func (pr *progressReader) Read(p []byte) (n int, err error) {
	n, err = pr.r.Read(p)
	if n > 0 && pr.onProgress != nil {
		pr.written += int64(n)
		if time.Since(pr.lastReport) >= 250*time.Millisecond {
			pr.lastReport = time.Now()
			pr.onProgress(pr.written)
		}
	}
	return
}

// ExportImages saves the selected images as .tar files into destDir.
func ExportImages(ctx context.Context, dc *dockerclient.Client, imageIDs []string, imageNames []string, destDir string) []ExportResult {
	return ExportImagesWithProgress(ctx, dc, imageIDs, imageNames, destDir, nil)
}

// ExportImagesWithProgress exports images concurrently (up to maxConcurrentExports)
// and reports progress. IsProgress byte-update messages are best-effort.
func ExportImagesWithProgress(ctx context.Context, dc *dockerclient.Client, imageIDs []string, imageNames []string, destDir string, onProgress func(ExportProgress)) []ExportResult {
	return exportImages(ctx, dc, imageIDs, imageNames, destDir, false, onProgress)
}

// ExportImagesAppendWithProgress behaves like ExportImagesWithProgress but
// merges the new images into the import scripts already present in destDir
// instead of replacing them, so a bundle can be grown in several passes.
func ExportImagesAppendWithProgress(ctx context.Context, dc *dockerclient.Client, imageIDs []string, imageNames []string, destDir string, onProgress func(ExportProgress)) []ExportResult {
	return exportImages(ctx, dc, imageIDs, imageNames, destDir, true, onProgress)
}

func exportImages(ctx context.Context, dc *dockerclient.Client, imageIDs []string, imageNames []string, destDir string, appendMode bool, onProgress func(ExportProgress)) []ExportResult {
	total := len(imageIDs)
	results := make([]ExportResult, total)
	cli := dc.Raw()

	type indexedResult struct {
		index  int
		result ExportResult
	}

	resultCh := make(chan indexedResult, total)
	sem := make(chan struct{}, maxConcurrentExports)

	var wg sync.WaitGroup
	for i, id := range imageIDs {
		wg.Add(1)
		i, id, name := i, id, imageNames[i]
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			var byteProgress func(int64)
			if onProgress != nil {
				byteProgress = func(written int64) {
					onProgress(ExportProgress{
						Total: total, Name: name,
						BytesWritten: written, IsProgress: true,
					})
				}
			}
			result := exportSingleImage(ctx, cli, id, name, destDir, byteProgress)
			resultCh <- indexedResult{i, result}
		}()
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	completed := 0
	for ir := range resultCh {
		results[ir.index] = ir.result
		completed++
		if onProgress != nil {
			onProgress(ExportProgress{
				Index: completed, Total: total,
				Name: ir.result.Name, Result: ir.result, HasResult: true,
			})
		}
	}

	var scriptPath string
	var scriptErr error
	if appendMode {
		scriptPath, scriptErr = AppendImageImportScript(destDir, results)
	} else {
		scriptPath, scriptErr = WriteImageImportScript(destDir, results)
	}
	if onProgress != nil {
		onProgress(ExportProgress{Index: total, Total: total, ScriptPath: scriptPath, ScriptErr: scriptErr, Done: true})
	}
	return results
}

func exportSingleImage(ctx context.Context, cli *client.Client, id, name, destDir string, onByteProgress func(int64)) ExportResult {
	safe := sanitizeFilename(name)
	outPath := filepath.Join(destDir, safe+".tar")

	out, err := os.Create(outPath)
	if err != nil {
		return ExportResult{Name: name, Err: fmt.Errorf("create file: %w", err)}
	}
	defer out.Close()

	// Save by the repo:tag reference when available so tag metadata
	// (RepoTags in manifest.json) is preserved in the archive.
	// Saving by image ID alone produces tars with no tags, so the
	// image imports as <none>:<none>.
	ref := id
	if strings.Contains(name, ":") {
		ref = name
	}
	rc, err := cli.ImageSave(ctx, []string{ref})
	if err != nil && ref != id {
		// The tag may have been removed after listing; fall back to ID.
		rc, err = cli.ImageSave(ctx, []string{id})
	}
	if err != nil {
		os.Remove(outPath)
		return ExportResult{Name: name, Err: fmt.Errorf("docker image save: %w", err)}
	}
	defer rc.Close()

	var reader io.Reader = rc
	if onByteProgress != nil {
		reader = &progressReader{r: rc, onProgress: onByteProgress}
	}

	if _, err := io.Copy(out, reader); err != nil {
		os.Remove(outPath)
		return ExportResult{Name: name, Err: fmt.Errorf("write tar: %w", err)}
	}

	return ExportResult{Name: name, FilePath: outPath}
}

// ExportVolumes archives the selected volume data as .tar files into destDir.
func ExportVolumes(ctx context.Context, dc *dockerclient.Client, volumeNames []string, destDir string) []ExportResult {
	return ExportVolumesWithProgress(ctx, dc, volumeNames, destDir, nil)
}

// ExportVolumesWithProgress archives volumes and reports progress.
// It tries the Docker Alpine container method first; on failure it falls back to
// reading the volume mountpoint directly (needed when Linux containers are
// unavailable, e.g. Windows containers mode).
func ExportVolumesWithProgress(ctx context.Context, dc *dockerclient.Client, volumeNames []string, destDir string, onProgress func(ExportProgress)) []ExportResult {
	return exportVolumes(ctx, dc, volumeNames, destDir, false, onProgress)
}

// ExportVolumesAppendWithProgress behaves like ExportVolumesWithProgress but
// merges the new volumes into the import scripts already present in destDir.
func ExportVolumesAppendWithProgress(ctx context.Context, dc *dockerclient.Client, volumeNames []string, destDir string, onProgress func(ExportProgress)) []ExportResult {
	return exportVolumes(ctx, dc, volumeNames, destDir, true, onProgress)
}

func exportVolumes(ctx context.Context, dc *dockerclient.Client, volumeNames []string, destDir string, appendMode bool, onProgress func(ExportProgress)) []ExportResult {
	results := make([]ExportResult, 0, len(volumeNames))
	cli := dc.Raw()
	total := len(volumeNames)

	for i, volName := range volumeNames {
		if onProgress != nil {
			onProgress(ExportProgress{Index: i, Total: total, Name: volName})
		}
		result := exportSingleVolumeWithFallback(ctx, cli, volName, destDir)
		results = append(results, result)
		if onProgress != nil {
			onProgress(ExportProgress{Index: i + 1, Total: total, Name: volName, Result: result, HasResult: true})
		}
	}
	var scriptPath string
	var scriptErr error
	if appendMode {
		scriptPath, scriptErr = AppendVolumeImportScript(destDir, results)
	} else {
		scriptPath, scriptErr = WriteVolumeImportScript(destDir, results)
	}
	if onProgress != nil {
		onProgress(ExportProgress{Index: total, Total: total, ScriptPath: scriptPath, ScriptErr: scriptErr, Done: true})
	}
	return results
}

// exportSingleVolumeWithFallback tries the container method first, then direct FS.
func exportSingleVolumeWithFallback(ctx context.Context, cli *client.Client, volName, destDir string) ExportResult {
	result := exportSingleVolume(ctx, cli, volName, destDir)
	if result.Err == nil {
		return result
	}
	direct := exportSingleVolumeDirect(ctx, cli, volName, destDir)
	if direct.Err == nil {
		return direct
	}
	return result // return original container error
}

func exportSingleVolume(ctx context.Context, cli *client.Client, volName, destDir string) ExportResult {
	outPath := filepath.Join(destDir, sanitizeFilename(volName)+".tar")

	out, err := os.Create(outPath)
	if err != nil {
		return ExportResult{Name: volName, Err: fmt.Errorf("create file: %w", err)}
	}
	defer out.Close()

	resp, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image: "alpine:latest",
			Cmd:   []string{"tar", "-cf", "-", "-C", "/data", "."},
		},
		&container.HostConfig{
			Binds: []string{volName + ":/data:ro"},
		},
		nil, nil,
		"dockertool-export-"+volName+"-"+timestamp(),
	)
	if err != nil {
		os.Remove(outPath)
		return ExportResult{Name: volName, Err: fmt.Errorf("create container: %w", err)}
	}

	containerID := resp.ID
	defer cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})

	// Attach BEFORE starting so we get a direct byte stream that bypasses
	// Docker's log storage (json-file driver encodes binary through JSON and
	// can corrupt tar data for large or binary-heavy volumes).
	hijack, err := cli.ContainerAttach(ctx, containerID, container.AttachOptions{
		Stdout: true,
		Stream: true,
	})
	if err != nil {
		os.Remove(outPath)
		return ExportResult{Name: volName, Err: fmt.Errorf("attach container: %w", err)}
	}
	defer hijack.Close()

	if err := cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		os.Remove(outPath)
		return ExportResult{Name: volName, Err: fmt.Errorf("start container: %w", err)}
	}

	// Read until the container exits and Docker closes the attached stream.
	// The attach stream uses the same Docker mux framing as ContainerLogs.
	if err := stripDockerMux(out, hijack.Reader); err != nil {
		os.Remove(outPath)
		return ExportResult{Name: volName, Err: fmt.Errorf("write archive: %w", err)}
	}

	// ContainerWait is still needed to surface a non-zero exit code from tar.
	statusCh, errCh := cli.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return ExportResult{Name: volName, Err: fmt.Errorf("wait container: %w", err)}
		}
	case status := <-statusCh:
		if status.StatusCode != 0 {
			return ExportResult{Name: volName, Err: fmt.Errorf("tar exited with code %d", status.StatusCode)}
		}
	}

	return ExportResult{Name: volName, FilePath: outPath}
}

// exportSingleVolumeDirect reads the volume mountpoint directly without Docker.
func exportSingleVolumeDirect(ctx context.Context, cli *client.Client, volName, destDir string) ExportResult {
	vol, err := cli.VolumeInspect(ctx, volName)
	if err != nil {
		return ExportResult{Name: volName, Err: fmt.Errorf("inspect volume: %w", err)}
	}

	outPath := filepath.Join(destDir, sanitizeFilename(volName)+".tar")
	out, err := os.Create(outPath)
	if err != nil {
		return ExportResult{Name: volName, Err: fmt.Errorf("create file: %w", err)}
	}

	tw := tar.NewWriter(out)
	if err := addDirToTar(tw, vol.Mountpoint, ""); err != nil {
		tw.Close()
		out.Close()
		os.Remove(outPath)
		return ExportResult{Name: volName, Err: fmt.Errorf("archive volume: %w", err)}
	}
	tw.Close()
	out.Close()
	return ExportResult{Name: volName, FilePath: outPath}
}

// scriptEntry is a single item destined for an import script, kept separate
// from its shell lines so entries can be deduplicated while appending.
type scriptEntry struct {
	name  string
	file  string
	lines []string
}

func importScriptPrelude(echo string) []string {
	return []string{
		"#!/usr/bin/env sh",
		`SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)`,
		`command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 1; }`,
		`failed=0`,
		fmt.Sprintf(`echo "%s..."`, echo),
	}
}

// imageScriptEntries builds the load lines for successfully exported images.
func imageScriptEntries(destDir string, results []ExportResult) []scriptEntry {
	entries := make([]scriptEntry, 0, len(results))
	for _, result := range results {
		if result.Err != nil || result.FilePath == "" {
			continue
		}
		entries = append(entries, scriptEntry{
			name: result.Name,
			file: filepath.Base(result.FilePath),
			lines: []string{
				fmt.Sprintf(`echo "Loading: %s"`, escapeDoubleQuoted(result.Name)),
				fmt.Sprintf(`docker load -i "$SCRIPT_DIR"/%s || { echo "  ERROR: failed to load %s" >&2; failed=$((failed+1)); }`,
					shellQuote(relSlashPath(destDir, result.FilePath)), escapeDoubleQuoted(result.Name)),
			},
		})
	}
	return entries
}

// volumeScriptEntries builds the restore lines for successfully exported volumes.
func volumeScriptEntries(destDir string, results []ExportResult) []scriptEntry {
	entries := make([]scriptEntry, 0, len(results))
	for _, result := range results {
		if result.Err != nil || result.FilePath == "" {
			continue
		}
		entries = append(entries, scriptEntry{
			name: result.Name,
			file: filepath.Base(result.FilePath),
			lines: []string{
				fmt.Sprintf(`echo "Restoring volume: %s"`, escapeDoubleQuoted(result.Name)),
				fmt.Sprintf(`docker volume create %s >/dev/null || { echo "  ERROR: failed to create volume %s" >&2; failed=$((failed+1)); }`,
					shellQuote(result.Name), escapeDoubleQuoted(result.Name)),
				fmt.Sprintf(`docker run --rm -i -v %s alpine:latest sh -c 'tar -xf - -C /data' < "$SCRIPT_DIR"/%s || { echo "  ERROR: failed to restore volume %s" >&2; failed=$((failed+1)); }`,
					shellQuote(result.Name+":/data"), shellQuote(relSlashPath(destDir, result.FilePath)), escapeDoubleQuoted(result.Name)),
			},
		})
	}
	return entries
}

func relSlashPath(destDir, path string) string {
	relPath, err := filepath.Rel(destDir, path)
	if err != nil {
		relPath = filepath.Base(path)
	}
	return filepath.ToSlash(relPath)
}

// WriteImageImportScript writes import-images.sh with per-item error reporting
// and a counter that prevents silent failures.
func WriteImageImportScript(destDir string, results []ExportResult) (string, error) {
	entries := imageScriptEntries(destDir, results)
	lines := importScriptPrelude("Importing Docker images")
	for _, entry := range entries {
		lines = append(lines, entry.lines...)
	}
	if len(entries) == 0 {
		lines = append(lines, noImagePlaceholder)
	}
	lines = append(lines,
		`[ "$failed" -eq 0 ] || echo "WARNING: $failed image(s) failed to load" >&2`,
		`echo "Image import complete."`,
		"",
	)

	scriptPath := filepath.Join(destDir, imageImportScriptName)
	if err := os.WriteFile(scriptPath, []byte(strings.Join(lines, "\n")), 0o755); err != nil {
		return scriptPath, err
	}
	return finalizeBundle(destDir, scriptPath, kindImage, entries, false)
}

// WriteVolumeImportScript writes import-volumes.sh with per-item error reporting.
func WriteVolumeImportScript(destDir string, results []ExportResult) (string, error) {
	entries := volumeScriptEntries(destDir, results)
	lines := importScriptPrelude("Importing Docker volumes")
	for _, entry := range entries {
		lines = append(lines, entry.lines...)
	}
	if len(entries) == 0 {
		lines = append(lines, noVolumePlaceholder)
	}
	lines = append(lines,
		`[ "$failed" -eq 0 ] || echo "WARNING: $failed item(s) failed to import" >&2`,
		`echo "Volume import complete."`,
		"",
	)

	scriptPath := filepath.Join(destDir, volumeImportScriptName)
	if err := os.WriteFile(scriptPath, []byte(strings.Join(lines, "\n")), 0o755); err != nil {
		return scriptPath, err
	}
	return finalizeBundle(destDir, scriptPath, kindVolume, entries, false)
}

// AppendImageImportScript merges freshly exported images into the
// import-images.sh already present in destDir. Everything that was in the
// script before is preserved verbatim; only the new load lines are inserted,
// and only when that image is not already part of the bundle.
func AppendImageImportScript(destDir string, results []ExportResult) (string, error) {
	scriptPath := filepath.Join(destDir, imageImportScriptName)
	entries := imageScriptEntries(destDir, results)
	if err := mergeEntriesIntoScript(scriptPath, entries, noImagePlaceholder); err != nil {
		return scriptPath, err
	}
	return finalizeBundle(destDir, scriptPath, kindImage, entries, true)
}

// AppendVolumeImportScript merges freshly exported volumes into the
// import-volumes.sh already present in destDir.
func AppendVolumeImportScript(destDir string, results []ExportResult) (string, error) {
	scriptPath := filepath.Join(destDir, volumeImportScriptName)
	entries := volumeScriptEntries(destDir, results)
	if err := mergeEntriesIntoScript(scriptPath, entries, noVolumePlaceholder); err != nil {
		return scriptPath, err
	}
	return finalizeBundle(destDir, scriptPath, kindVolume, entries, true)
}

func finalizeBundle(destDir, scriptPath, kind string, entries []scriptEntry, appendMode bool) (string, error) {
	if err := saveBundleEntries(destDir, kind, entries, appendMode); err != nil {
		return scriptPath, err
	}
	launcherPath, err := writeImportLauncherScript(destDir)
	if err != nil {
		return scriptPath, err
	}
	return launcherPath, nil
}

// mergeEntriesIntoScript inserts the new entries just before the trailing
// summary block of an existing import script, and drops the "nothing to
// import" placeholder that stopped being true the moment an entry was added.
func mergeEntriesIntoScript(scriptPath string, entries []scriptEntry, placeholders ...string) error {
	if len(entries) == 0 {
		return nil
	}

	raw, err := os.ReadFile(scriptPath)
	if err != nil {
		return err
	}
	content := strings.ReplaceAll(string(raw), "\r\n", "\n")

	// An entry is identified by its first generated line (the echo announcing
	// the item), which is unique per name and stable across runs.
	fresh := make([]string, 0, len(entries)*3)
	for _, entry := range entries {
		if entry.name != "" && strings.Contains(content, entry.lines[0]) {
			continue
		}
		fresh = append(fresh, entry.lines...)
	}
	if len(fresh) == 0 {
		return nil
	}

	lines := strings.Split(content, "\n")
	hadTrailingNewline := len(lines) > 0 && lines[len(lines)-1] == ""
	if hadTrailingNewline {
		lines = lines[:len(lines)-1]
	}

	// Refuse to rewrite a file that is not an import script we produced.
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "#!") {
		return fmt.Errorf("%s is not a dockertool import script", filepath.Base(scriptPath))
	}

	kept := lines[:0]
	for _, line := range lines {
		drop := false
		for _, placeholder := range placeholders {
			if strings.TrimSpace(line) == placeholder {
				drop = true
				break
			}
		}
		if !drop {
			kept = append(kept, line)
		}
	}
	lines = kept

	// Walk back over the summary block (blank lines, the failure counter and
	// the completion notice) so the counters keep covering the merged items.
	footerStart := len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, `[ "$failed"`) || strings.HasSuffix(trimmed, `import complete."`) {
			footerStart = i
			continue
		}
		break
	}

	merged := make([]string, 0, len(lines)+len(fresh)+2)
	merged = append(merged, lines[:footerStart]...)
	if len(merged) > 0 && strings.TrimSpace(merged[len(merged)-1]) != "" {
		merged = append(merged, "")
	}
	merged = append(merged, fresh...)
	merged = append(merged, "")
	merged = append(merged, lines[footerStart:]...)
	if hadTrailingNewline {
		merged = append(merged, "")
	}

	return os.WriteFile(scriptPath, []byte(strings.Join(merged, "\n")), 0o755)
}

func writeImportLauncherScript(destDir string) (string, error) {
	scriptPath := filepath.Join(destDir, "import.sh")
	lines := []string{
		"#!/usr/bin/env sh",
		`SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)`,
		`if [ -f "$SCRIPT_DIR/import-images.sh" ]; then`,
		`  sh "$SCRIPT_DIR/import-images.sh"`,
		`fi`,
		`if [ -f "$SCRIPT_DIR/import-volumes.sh" ]; then`,
		`  sh "$SCRIPT_DIR/import-volumes.sh"`,
		`fi`,
		`echo "All imports complete."`,
		"",
	}
	return scriptPath, os.WriteFile(scriptPath, []byte(strings.Join(lines, "\n")), 0o755)
}

const (
	kindImage  = "image"
	kindVolume = "volume"
)

// BundleItem records one artifact inside an export directory.
type BundleItem struct {
	Name string `json:"name"`
	File string `json:"file"`
}

// BundleManifest describes what an export directory holds. It is written
// alongside the import scripts so later runs (and future tooling) can tell an
// appendable bundle from an unrelated folder.
type BundleManifest struct {
	Version  int          `json:"version"`
	Exported string       `json:"exportedAt,omitempty"`
	Images   []BundleItem `json:"images,omitempty"`
	Volumes  []BundleItem `json:"volumes,omitempty"`
}

// BundleStatus summarises an existing export directory.
type BundleStatus struct {
	Images        int
	Volumes       int
	ImagesScript  bool
	VolumesScript bool
}

// Exists reports whether the directory already looks like an export bundle.
func (s BundleStatus) Exists() bool {
	return s.ImagesScript || s.VolumesScript
}

// InspectBundle counts what an export destination already contains. The import
// scripts are the source of truth so bundles created by earlier versions (which
// had no manifest) are recognised too.
func InspectBundle(destDir string) BundleStatus {
	status := BundleStatus{}
	imageScript := filepath.Join(destDir, imageImportScriptName)
	volumeScript := filepath.Join(destDir, volumeImportScriptName)

	if _, err := os.Stat(imageScript); err == nil {
		status.ImagesScript = true
		status.Images = countOccurrences(imageScript, imageEntryMarker)
	}
	if _, err := os.Stat(volumeScript); err == nil {
		status.VolumesScript = true
		status.Volumes = countOccurrences(volumeScript, volumeEntryMarker)
	}
	if !status.ImagesScript && !status.VolumesScript {
		manifest := LoadBundleManifest(destDir)
		status.Images = len(manifest.Images)
		status.Volumes = len(manifest.Volumes)
	}
	return status
}

func countOccurrences(path, marker string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(string(data), marker)
}

// LoadBundleManifest reads the manifest of an export directory. A missing or
// corrupt manifest yields an empty one so callers can always append safely.
func LoadBundleManifest(destDir string) BundleManifest {
	manifest := BundleManifest{Version: bundleManifestVersion}
	data, err := os.ReadFile(filepath.Join(destDir, bundleManifestName))
	if err != nil {
		return manifest
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return BundleManifest{Version: bundleManifestVersion}
	}
	if manifest.Version == 0 {
		manifest.Version = bundleManifestVersion
	}
	return manifest
}

// Save writes the manifest into the export directory.
func (m BundleManifest) Save(destDir string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(destDir, bundleManifestName), append(data, '\n'), 0o644)
}

// saveBundleEntries records the entries of one run in the manifest. A fresh
// export replaces the manifest; an append keeps what was already recorded.
func saveBundleEntries(destDir, kind string, entries []scriptEntry, appendMode bool) error {
	manifest := BundleManifest{Version: bundleManifestVersion}
	if appendMode {
		manifest = LoadBundleManifest(destDir)
	}
	items := make([]BundleItem, 0, len(entries))
	for _, entry := range entries {
		items = append(items, BundleItem{Name: entry.name, File: entry.file})
	}

	switch kind {
	case kindImage:
		if appendMode {
			manifest.Images = mergeBundleItems(manifest.Images, items)
		} else {
			manifest.Images = items
		}
	case kindVolume:
		if appendMode {
			manifest.Volumes = mergeBundleItems(manifest.Volumes, items)
		} else {
			manifest.Volumes = items
		}
	}
	manifest.Exported = time.Now().Format(time.RFC3339)

	return manifest.Save(destDir)
}

// mergeBundleItems keeps the existing entries in place and folds the new ones
// in, refreshing the file name of anything that was exported again.
func mergeBundleItems(existing, added []BundleItem) []BundleItem {
	merged := make([]BundleItem, len(existing))
	copy(merged, existing)
	index := make(map[string]int, len(merged))
	for i, item := range merged {
		index[item.Name] = i
	}
	for _, item := range added {
		if i, ok := index[item.Name]; ok {
			merged[i].File = item.File
			continue
		}
		index[item.Name] = len(merged)
		merged = append(merged, item)
	}
	return merged
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func escapeDoubleQuoted(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	value = strings.ReplaceAll(value, `$`, `\$`)
	value = strings.ReplaceAll(value, "`", "\\`")
	return value
}

// stripDockerMux reads the docker multiplexed stream and writes only stdout to w.
func stripDockerMux(w io.Writer, r io.Reader) error {
	hdr := make([]byte, 8)
	for {
		_, err := io.ReadFull(r, hdr)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil
		}
		if err != nil {
			return err
		}
		size := int64(hdr[4])<<24 | int64(hdr[5])<<16 | int64(hdr[6])<<8 | int64(hdr[7])
		if hdr[0] == 1 {
			if _, err := io.CopyN(w, r, size); err != nil {
				return err
			}
		} else {
			if _, err := io.CopyN(io.Discard, r, size); err != nil {
				return err
			}
		}
	}
}

func addDirToTar(tw *tar.Writer, srcDir, prefix string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		fullPath := filepath.Join(srcDir, entry.Name())
		tarPath := filepath.Join(prefix, entry.Name())

		info, err := entry.Info()
		if err != nil {
			return err
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = tarPath

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		if !entry.IsDir() {
			f, err := os.Open(fullPath)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(tw, f)
			f.Close()
			if copyErr != nil {
				return copyErr
			}
		} else {
			if err := addDirToTar(tw, fullPath, tarPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func sanitizeFilename(name string) string {
	result := make([]byte, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c == '/' || c == '\\' || c == ':' || c == '*' || c == '?' || c == '"' || c == '<' || c == '>' || c == '|' {
			result[i] = '_'
		} else {
			result[i] = c
		}
	}
	return string(result)
}

func timestamp() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
