package operations

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	dockerclient "github.com/jim/dockertool/internal/docker"
)

// ImportResult holds the outcome of a single import operation.
type ImportResult struct {
	Name string
	Err  error
}

// ImportProgress reports item-level import progress.
type ImportProgress struct {
	Index     int
	Total     int
	Name      string
	Result    ImportResult
	HasResult bool
	Done      bool
}

// ImportImages loads .tar image archives into Docker.
func ImportImages(ctx context.Context, dc *dockerclient.Client, filePaths []string) []ImportResult {
	return ImportImagesWithProgress(ctx, dc, filePaths, nil)
}

// ImportImagesWithProgress loads images and reports progress before and after each file.
func ImportImagesWithProgress(ctx context.Context, dc *dockerclient.Client, filePaths []string, onProgress func(ImportProgress)) []ImportResult {
	results := make([]ImportResult, 0, len(filePaths))
	cli := dc.Raw()
	total := len(filePaths)

	for i, fp := range filePaths {
		name := filepath.Base(fp)
		if onProgress != nil {
			onProgress(ImportProgress{Index: i, Total: total, Name: name})
		}
		result := importSingleImage(ctx, cli, fp)
		results = append(results, result)
		if onProgress != nil {
			onProgress(ImportProgress{Index: i + 1, Total: total, Name: name, Result: result, HasResult: true})
		}
	}
	if onProgress != nil {
		onProgress(ImportProgress{Index: total, Total: total, Done: true})
	}
	return results
}

func importSingleImage(ctx context.Context, cli *client.Client, filePath string) ImportResult {
	name := filepath.Base(filePath)

	f, err := os.Open(filePath)
	if err != nil {
		return ImportResult{Name: name, Err: fmt.Errorf("open file: %w", err)}
	}
	defer f.Close()

	resp, err := cli.ImageLoad(ctx, f)
	if err != nil {
		return ImportResult{Name: name, Err: fmt.Errorf("docker image load: %w", err)}
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	return ImportResult{Name: name}
}

// ImportVolumes restores volume data from .tar archives into named Docker volumes.
func ImportVolumes(ctx context.Context, dc *dockerclient.Client, filePaths []string, volumeNames []string) []ImportResult {
	return ImportVolumesWithProgress(ctx, dc, filePaths, volumeNames, nil)
}

// ImportVolumesWithProgress restores volumes and reports progress before and after each file.
func ImportVolumesWithProgress(ctx context.Context, dc *dockerclient.Client, filePaths []string, volumeNames []string, onProgress func(ImportProgress)) []ImportResult {
	results := make([]ImportResult, 0, len(filePaths))
	cli := dc.Raw()
	total := len(filePaths)

	for i, fp := range filePaths {
		volName := volumeNames[i]
		if onProgress != nil {
			onProgress(ImportProgress{Index: i, Total: total, Name: volName})
		}
		result := importSingleVolume(ctx, cli, fp, volName)
		results = append(results, result)
		if onProgress != nil {
			onProgress(ImportProgress{Index: i + 1, Total: total, Name: volName, Result: result, HasResult: true})
		}
	}
	if onProgress != nil {
		onProgress(ImportProgress{Index: total, Total: total, Done: true})
	}
	return results
}

func importSingleVolume(ctx context.Context, cli *client.Client, filePath, volName string) ImportResult {
	f, err := os.Open(filePath)
	if err != nil {
		return ImportResult{Name: volName, Err: fmt.Errorf("open file: %w", err)}
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return ImportResult{Name: volName, Err: fmt.Errorf("stat file: %w", err)}
	}

	// Create container that will receive the tar via stdin.
	resp, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image:     "alpine:latest",
			Cmd:       []string{"tar", "-xf", "-", "-C", "/data"},
			OpenStdin: true,
			StdinOnce: true,
		},
		&container.HostConfig{
			Binds: []string{volName + ":/data"},
		},
		nil, nil,
		"dockertool-import-"+volName+"-"+timestamp(),
	)
	if err != nil {
		return ImportResult{Name: volName, Err: fmt.Errorf("create container: %w", err)}
	}

	containerID := resp.ID
	defer cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})

	// Attach stdin to pipe the tar data.
	attach, err := cli.ContainerAttach(ctx, containerID, container.AttachOptions{Stdin: true, Stream: true})
	if err != nil {
		return ImportResult{Name: volName, Err: fmt.Errorf("attach container: %w", err)}
	}

	if err := cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		attach.Close()
		return ImportResult{Name: volName, Err: fmt.Errorf("start container: %w", err)}
	}

	// Stream the file into the container's stdin.
	if _, err := io.CopyN(attach.Conn, f, info.Size()); err != nil && err != io.EOF {
		attach.Close()
		return ImportResult{Name: volName, Err: fmt.Errorf("stream tar: %w", err)}
	}
	attach.CloseWrite()
	attach.Close()

	statusCh, errCh := cli.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return ImportResult{Name: volName, Err: fmt.Errorf("wait container: %w", err)}
		}
	case <-statusCh:
	}

	return ImportResult{Name: volName}
}

// ImportVolumesDirect extracts a tar archive directly to the volume's mountpoint.
func ImportVolumesDirect(ctx context.Context, dc *dockerclient.Client, filePaths []string, volumeNames []string) []ImportResult {
	results := make([]ImportResult, 0, len(filePaths))
	cli := dc.Raw()

	for i, fp := range filePaths {
		volName := volumeNames[i]
		vols, err := cli.VolumeInspect(ctx, volName)
		if err != nil {
			results = append(results, ImportResult{Name: volName, Err: fmt.Errorf("inspect volume: %w", err)})
			continue
		}

		if err := extractTar(fp, vols.Mountpoint); err != nil {
			results = append(results, ImportResult{Name: volName, Err: err})
			continue
		}

		results = append(results, ImportResult{Name: volName})
	}
	return results
}

func extractTar(tarPath, destDir string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	tr := tar.NewReader(f)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}

		target := filepath.Join(destDir, filepath.Clean(hdr.Name))

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.Create(target)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		}
	}
	return nil
}
