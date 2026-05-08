package operations

import (
	"archive/tar"
	"context"
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

const maxConcurrentExports = 3

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

	scriptPath, scriptErr := WriteImageImportScript(destDir, results)
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

	rc, err := cli.ImageSave(ctx, []string{id})
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
	scriptPath, scriptErr := WriteVolumeImportScript(destDir, results)
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

	if err := cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		os.Remove(outPath)
		return ExportResult{Name: volName, Err: fmt.Errorf("start container: %w", err)}
	}

	statusCh, errCh := cli.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			os.Remove(outPath)
			return ExportResult{Name: volName, Err: fmt.Errorf("wait container: %w", err)}
		}
	case <-statusCh:
	}

	rc, err := cli.ContainerLogs(ctx, containerID, container.LogsOptions{ShowStdout: true, ShowStderr: false})
	if err != nil {
		os.Remove(outPath)
		return ExportResult{Name: volName, Err: fmt.Errorf("container logs: %w", err)}
	}
	defer rc.Close()

	if err := stripDockerMux(out, rc); err != nil {
		os.Remove(outPath)
		return ExportResult{Name: volName, Err: fmt.Errorf("write archive: %w", err)}
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

// WriteImageImportScript writes import-images.sh with per-item error reporting
// and a counter that prevents silent failures.
func WriteImageImportScript(destDir string, results []ExportResult) (string, error) {
	scriptPath := filepath.Join(destDir, "import-images.sh")
	lines := []string{
		"#!/usr/bin/env sh",
		`SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)`,
		`command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 1; }`,
		`failed=0`,
		`echo "Importing Docker images..."`,
	}

	count := 0
	for _, result := range results {
		if result.Err != nil || result.FilePath == "" {
			continue
		}
		relPath, err := filepath.Rel(destDir, result.FilePath)
		if err != nil {
			relPath = filepath.Base(result.FilePath)
		}
		relPath = filepath.ToSlash(relPath)
		lines = append(lines,
			fmt.Sprintf(`echo "Loading: %s"`, escapeDoubleQuoted(result.Name)),
			fmt.Sprintf(`docker load -i "$SCRIPT_DIR"/%s || { echo "  ERROR: failed to load %s" >&2; failed=$((failed+1)); }`,
				shellQuote(relPath), escapeDoubleQuoted(result.Name)),
		)
		count++
	}

	if count == 0 {
		lines = append(lines, `echo "No image archives found in this export."`)
	}
	lines = append(lines,
		`[ "$failed" -eq 0 ] || echo "WARNING: $failed image(s) failed to load" >&2`,
		`echo "Image import complete."`,
		"",
	)

	if err := os.WriteFile(scriptPath, []byte(strings.Join(lines, "\n")), 0o755); err != nil {
		return scriptPath, err
	}
	launcherPath, err := writeImportLauncherScript(destDir)
	if err != nil {
		return scriptPath, err
	}
	return launcherPath, nil
}

// WriteVolumeImportScript writes import-volumes.sh with per-item error reporting.
func WriteVolumeImportScript(destDir string, results []ExportResult) (string, error) {
	scriptPath := filepath.Join(destDir, "import-volumes.sh")
	lines := []string{
		"#!/usr/bin/env sh",
		`SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)`,
		`command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 1; }`,
		`failed=0`,
		`echo "Importing Docker volumes..."`,
	}

	count := 0
	for _, result := range results {
		if result.Err != nil || result.FilePath == "" {
			continue
		}
		relPath, err := filepath.Rel(destDir, result.FilePath)
		if err != nil {
			relPath = filepath.Base(result.FilePath)
		}
		relPath = filepath.ToSlash(relPath)
		lines = append(lines,
			fmt.Sprintf(`echo "Restoring volume: %s"`, escapeDoubleQuoted(result.Name)),
			fmt.Sprintf(`docker volume create %s >/dev/null || { echo "  ERROR: failed to create volume %s" >&2; failed=$((failed+1)); }`,
				shellQuote(result.Name), escapeDoubleQuoted(result.Name)),
			fmt.Sprintf(`docker run --rm -i -v %s alpine:latest sh -c 'tar -xf - -C /data' < "$SCRIPT_DIR"/%s || { echo "  ERROR: failed to restore volume %s" >&2; failed=$((failed+1)); }`,
				shellQuote(result.Name+":/data"), shellQuote(relPath), escapeDoubleQuoted(result.Name)),
		)
		count++
	}

	if count == 0 {
		lines = append(lines, `echo "No volume archives found in this export."`)
	}
	lines = append(lines,
		`[ "$failed" -eq 0 ] || echo "WARNING: $failed item(s) failed to import" >&2`,
		`echo "Volume import complete."`,
		"",
	)

	if err := os.WriteFile(scriptPath, []byte(strings.Join(lines, "\n")), 0o755); err != nil {
		return scriptPath, err
	}
	launcherPath, err := writeImportLauncherScript(destDir)
	if err != nil {
		return scriptPath, err
	}
	return launcherPath, nil
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
