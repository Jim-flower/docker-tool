package operations

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	dockerclient "github.com/jim/dockertool/internal/docker"
)

// ExportResult holds the outcome of a single export operation.
type ExportResult struct {
	Name     string
	FilePath string
	Err      error
}

// ExportProgress reports item-level export progress.
type ExportProgress struct {
	Index      int
	Total      int
	Name       string
	Result     ExportResult
	HasResult  bool
	ScriptPath string
	ScriptErr  error
	Done       bool
}

// ExportImages saves the selected images as .tar files into destDir.
func ExportImages(ctx context.Context, dc *dockerclient.Client, imageIDs []string, imageNames []string, destDir string) []ExportResult {
	return ExportImagesWithProgress(ctx, dc, imageIDs, imageNames, destDir, nil)
}

// ExportImagesWithProgress saves images and reports progress before and after each item.
func ExportImagesWithProgress(ctx context.Context, dc *dockerclient.Client, imageIDs []string, imageNames []string, destDir string, onProgress func(ExportProgress)) []ExportResult {
	results := make([]ExportResult, 0, len(imageIDs))
	cli := dc.Raw()
	total := len(imageIDs)

	for i, id := range imageIDs {
		name := imageNames[i]
		if onProgress != nil {
			onProgress(ExportProgress{Index: i, Total: total, Name: name})
		}
		result := exportSingleImage(ctx, cli, id, name, destDir)
		results = append(results, result)
		if onProgress != nil {
			onProgress(ExportProgress{Index: i + 1, Total: total, Name: name, Result: result, HasResult: true})
		}
	}
	scriptPath, scriptErr := WriteImageImportScript(destDir, results)
	if onProgress != nil {
		onProgress(ExportProgress{Index: total, Total: total, ScriptPath: scriptPath, ScriptErr: scriptErr, Done: true})
	}
	return results
}

func exportSingleImage(ctx context.Context, cli *client.Client, id, name, destDir string) ExportResult {
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

	if _, err := io.Copy(out, rc); err != nil {
		os.Remove(outPath)
		return ExportResult{Name: name, Err: fmt.Errorf("write tar: %w", err)}
	}

	return ExportResult{Name: name, FilePath: outPath}
}

// ExportVolumes archives the selected volume data as .tar files into destDir.
// It spins up a temporary Alpine container that mounts the volume and streams a tar archive.
func ExportVolumes(ctx context.Context, dc *dockerclient.Client, volumeNames []string, destDir string) []ExportResult {
	return ExportVolumesWithProgress(ctx, dc, volumeNames, destDir, nil)
}

// ExportVolumesWithProgress archives volumes and reports progress before and after each item.
func ExportVolumesWithProgress(ctx context.Context, dc *dockerclient.Client, volumeNames []string, destDir string, onProgress func(ExportProgress)) []ExportResult {
	results := make([]ExportResult, 0, len(volumeNames))
	cli := dc.Raw()
	total := len(volumeNames)

	for i, volName := range volumeNames {
		if onProgress != nil {
			onProgress(ExportProgress{Index: i, Total: total, Name: volName})
		}
		result := exportSingleVolume(ctx, cli, volName, destDir)
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

func exportSingleVolume(ctx context.Context, cli *client.Client, volName, destDir string) ExportResult {
	outPath := filepath.Join(destDir, sanitizeFilename(volName)+".tar")

	out, err := os.Create(outPath)
	if err != nil {
		return ExportResult{Name: volName, Err: fmt.Errorf("create file: %w", err)}
	}
	defer out.Close()

	// Create a temporary container mounting the volume.
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

	// Wait for container to finish.
	statusCh, errCh := cli.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			os.Remove(outPath)
			return ExportResult{Name: volName, Err: fmt.Errorf("wait container: %w", err)}
		}
	case <-statusCh:
	}

	// Collect logs (stdout = tar data).
	rc, err := cli.ContainerLogs(ctx, containerID, container.LogsOptions{ShowStdout: true, ShowStderr: false})
	if err != nil {
		os.Remove(outPath)
		return ExportResult{Name: volName, Err: fmt.Errorf("container logs: %w", err)}
	}
	defer rc.Close()

	// Docker multiplexes stdout/stderr — strip the 8-byte header.
	if err := stripDockerMux(out, rc); err != nil {
		os.Remove(outPath)
		return ExportResult{Name: volName, Err: fmt.Errorf("write archive: %w", err)}
	}

	return ExportResult{Name: volName, FilePath: outPath}
}

// WriteImageImportScript writes shell scripts that import exported image tar files.
func WriteImageImportScript(destDir string, results []ExportResult) (string, error) {
	scriptPath := filepath.Join(destDir, "import-images.sh")
	lines := []string{
		"#!/usr/bin/env sh",
		"set -eu",
		`SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)`,
		`command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 1; }`,
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
			fmt.Sprintf(`echo "Loading image: %s"`, escapeDoubleQuoted(result.Name)),
			fmt.Sprintf(`docker load -i "$SCRIPT_DIR"/%s`, shellQuote(relPath)),
		)
		count++
	}

	if count == 0 {
		lines = append(lines, `echo "No image archives found in this export."`)
	}
	lines = append(lines, `echo "Image import complete."`, "")

	if err := os.WriteFile(scriptPath, []byte(strings.Join(lines, "\n")), 0o755); err != nil {
		return scriptPath, err
	}
	if err := writeImportLauncherScript(destDir); err != nil {
		return scriptPath, err
	}
	return scriptPath, nil
}

// WriteVolumeImportScript writes shell scripts that import exported volume tar files.
func WriteVolumeImportScript(destDir string, results []ExportResult) (string, error) {
	scriptPath := filepath.Join(destDir, "import-volumes.sh")
	lines := []string{
		"#!/usr/bin/env sh",
		"set -eu",
		`SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)`,
		`command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 1; }`,
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
			fmt.Sprintf(`docker volume create %s >/dev/null`, shellQuote(result.Name)),
			fmt.Sprintf(`docker run --rm -i -v %s alpine:latest sh -c 'tar -xf - -C /data' < "$SCRIPT_DIR"/%s`, shellQuote(result.Name+":/data"), shellQuote(relPath)),
		)
		count++
	}

	if count == 0 {
		lines = append(lines, `echo "No volume archives found in this export."`)
	}
	lines = append(lines, `echo "Volume import complete."`, "")

	if err := os.WriteFile(scriptPath, []byte(strings.Join(lines, "\n")), 0o755); err != nil {
		return scriptPath, err
	}
	if err := writeImportLauncherScript(destDir); err != nil {
		return scriptPath, err
	}
	return scriptPath, nil
}

func writeImportLauncherScript(destDir string) error {
	scriptPath := filepath.Join(destDir, "import.sh")
	lines := []string{
		"#!/usr/bin/env sh",
		"set -eu",
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
	return os.WriteFile(scriptPath, []byte(strings.Join(lines, "\n")), 0o755)
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
		// hdr[0]: stream type (1=stdout, 2=stderr)
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

// ExportVolumesDirect archives volume directories directly (Windows-friendly fallback).
func ExportVolumesDirect(ctx context.Context, dc *dockerclient.Client, volumeNames []string, destDir string) []ExportResult {
	results := make([]ExportResult, 0, len(volumeNames))
	cli := dc.Raw()

	for _, volName := range volumeNames {
		vols, err := cli.VolumeInspect(ctx, volName)
		if err != nil {
			results = append(results, ExportResult{Name: volName, Err: fmt.Errorf("inspect volume: %w", err)})
			continue
		}

		outPath := filepath.Join(destDir, sanitizeFilename(volName)+".tar")
		out, err := os.Create(outPath)
		if err != nil {
			results = append(results, ExportResult{Name: volName, Err: fmt.Errorf("create file: %w", err)})
			continue
		}

		tw := tar.NewWriter(out)
		if err := addDirToTar(tw, vols.Mountpoint, ""); err != nil {
			out.Close()
			os.Remove(outPath)
			results = append(results, ExportResult{Name: volName, Err: fmt.Errorf("archive volume: %w", err)})
			continue
		}

		tw.Close()
		out.Close()
		results = append(results, ExportResult{Name: volName, FilePath: outPath})
	}
	return results
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
