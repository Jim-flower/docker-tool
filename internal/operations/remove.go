package operations

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	dockerclient "github.com/jim/dockertool/internal/docker"
)

// RemoveResult holds the outcome of a single removal.
type RemoveResult struct {
	Name string
	Err  error
}

// RemoveProgress reports item-level removal progress.
type RemoveProgress struct {
	Index     int
	Total     int
	Name      string
	Result    RemoveResult
	HasResult bool
	Done      bool
}

// RemoveImages deletes the given images.
func RemoveImages(ctx context.Context, dc *dockerclient.Client, imageIDs []string, imageNames []string, force bool) []RemoveResult {
	return RemoveImagesWithProgress(ctx, dc, imageIDs, imageNames, force, nil)
}

// RemoveImagesWithProgress deletes images one at a time. This is deliberately
// serial: images inside one export often share parent layers, and removing
// parent and child concurrently makes the daemon reject both with "image has
// dependent child images" errors that never happen sequentially.
func RemoveImagesWithProgress(ctx context.Context, dc *dockerclient.Client, imageIDs []string, imageNames []string, force bool, onProgress func(RemoveProgress)) []RemoveResult {
	results := make([]RemoveResult, 0, len(imageIDs))
	cli := dc.Raw()
	total := len(imageIDs)

	for i, id := range imageIDs {
		name := imageNames[i]
		if onProgress != nil {
			onProgress(RemoveProgress{Index: i, Total: total, Name: name})
		}
		result := removeSingleImage(ctx, cli, id, name, force)
		results = append(results, result)
		if onProgress != nil {
			onProgress(RemoveProgress{Index: i + 1, Total: total, Name: name, Result: result, HasResult: true})
		}
	}
	if onProgress != nil {
		onProgress(RemoveProgress{Index: total, Total: total, Done: true})
	}
	return results
}

func removeSingleImage(ctx context.Context, cli *client.Client, id, name string, force bool) RemoveResult {
	// PruneChildren stays off on purpose: `force` should only override Docker's
	// "in use" refusal, never cascade into deleting dependent images.
	_, err := cli.ImageRemove(ctx, id, image.RemoveOptions{Force: force, PruneChildren: false})
	if err != nil {
		return RemoveResult{Name: name, Err: fmt.Errorf("docker image rm: %w", err)}
	}
	return RemoveResult{Name: name}
}

// RemoveVolumes deletes the given volumes. Force is never used, so a volume
// still referenced by a container is reported as a failure instead of being
// torn out from under it.
func RemoveVolumes(ctx context.Context, dc *dockerclient.Client, volumeNames []string) []RemoveResult {
	return RemoveVolumesWithProgress(ctx, dc, volumeNames, nil)
}

// RemoveVolumesWithProgress deletes volumes one at a time.
func RemoveVolumesWithProgress(ctx context.Context, dc *dockerclient.Client, volumeNames []string, onProgress func(RemoveProgress)) []RemoveResult {
	results := make([]RemoveResult, 0, len(volumeNames))
	cli := dc.Raw()
	total := len(volumeNames)

	for i, name := range volumeNames {
		if onProgress != nil {
			onProgress(RemoveProgress{Index: i, Total: total, Name: name})
		}
		result := removeSingleVolume(ctx, cli, name)
		results = append(results, result)
		if onProgress != nil {
			onProgress(RemoveProgress{Index: i + 1, Total: total, Name: name, Result: result, HasResult: true})
		}
	}
	if onProgress != nil {
		onProgress(RemoveProgress{Index: total, Total: total, Done: true})
	}
	return results
}

func removeSingleVolume(ctx context.Context, cli *client.Client, name string) RemoveResult {
	if err := cli.VolumeRemove(ctx, name, false); err != nil {
		return RemoveResult{Name: name, Err: fmt.Errorf("docker volume rm: %w", err)}
	}
	return RemoveResult{Name: name}
}

// RemoveContainers deletes the given containers.
func RemoveContainers(ctx context.Context, dc *dockerclient.Client, containerIDs []string, containerNames []string, force bool) []RemoveResult {
	return RemoveContainersWithProgress(ctx, dc, containerIDs, containerNames, force, nil)
}

// RemoveContainersWithProgress deletes containers one at a time. Anonymous
// volumes attached by the container are always left in place: dropping them
// along with the container would silently discard data.
func RemoveContainersWithProgress(ctx context.Context, dc *dockerclient.Client, containerIDs []string, containerNames []string, force bool, onProgress func(RemoveProgress)) []RemoveResult {
	results := make([]RemoveResult, 0, len(containerIDs))
	cli := dc.Raw()
	total := len(containerIDs)

	for i, id := range containerIDs {
		name := containerNames[i]
		if onProgress != nil {
			onProgress(RemoveProgress{Index: i, Total: total, Name: name})
		}
		result := removeSingleContainer(ctx, cli, id, name, force)
		results = append(results, result)
		if onProgress != nil {
			onProgress(RemoveProgress{Index: i + 1, Total: total, Name: name, Result: result, HasResult: true})
		}
	}
	if onProgress != nil {
		onProgress(RemoveProgress{Index: total, Total: total, Done: true})
	}
	return results
}

func removeSingleContainer(ctx context.Context, cli *client.Client, id, name string, force bool) RemoveResult {
	err := cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: force, RemoveVolumes: false})
	if err != nil {
		return RemoveResult{Name: name, Err: fmt.Errorf("docker container rm: %w", err)}
	}
	return RemoveResult{Name: name}
}
