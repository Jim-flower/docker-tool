package docker

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
)

// Image represents a Docker image with display-friendly fields.
type Image struct {
	ID         string
	ShortID    string
	Repository string
	Tag        string
	Size       int64
	Created    int64
}

// DisplayName returns a human-readable name for the image.
func (i Image) DisplayName() string {
	if i.Repository == "<none>" || i.Repository == "" {
		return i.ShortID
	}
	if i.Tag == "<none>" || i.Tag == "" {
		return i.Repository
	}
	return fmt.Sprintf("%s:%s", i.Repository, i.Tag)
}

// ListImages returns all local Docker images.
func (c *Client) ListImages(ctx context.Context) ([]Image, error) {
	summaries, err := c.cli.ImageList(ctx, image.ListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("listing images: %w", err)
	}

	images := make([]Image, 0, len(summaries))
	for _, s := range summaries {
		repo, tag := parseRepoTag(s.RepoTags)
		images = append(images, Image{
			ID:         s.ID,
			ShortID:    shortID(s.ID),
			Repository: repo,
			Tag:        tag,
			Size:       s.Size,
			Created:    s.Created,
		})
	}
	return images, nil
}

// ListRunningContainerImages returns unique images used by currently running containers.
func (c *Client) ListRunningContainerImages(ctx context.Context) ([]Image, error) {
	containers, err := c.cli.ContainerList(ctx, container.ListOptions{
		Filters: filters.NewArgs(filters.Arg("status", "running")),
	})
	if err != nil {
		return nil, fmt.Errorf("listing running containers: %w", err)
	}

	allImages, err := c.ListImages(ctx)
	if err != nil {
		return nil, err
	}

	imagesByID := make(map[string]Image, len(allImages))
	for _, img := range allImages {
		imagesByID[img.ID] = img
	}

	seen := make(map[string]struct{}, len(containers))
	images := make([]Image, 0, len(containers))
	for _, ctr := range containers {
		if ctr.ImageID == "" {
			continue
		}
		if _, ok := seen[ctr.ImageID]; ok {
			continue
		}
		seen[ctr.ImageID] = struct{}{}

		img, ok := imagesByID[ctr.ImageID]
		if !ok {
			repo, tag := parseRepoTag([]string{ctr.Image})
			img = Image{
				ID:         ctr.ImageID,
				ShortID:    shortID(ctr.ImageID),
				Repository: repo,
				Tag:        tag,
			}
		}
		images = append(images, img)
	}

	sort.Slice(images, func(i, j int) bool {
		return images[i].DisplayName() < images[j].DisplayName()
	})

	return images, nil
}

func parseRepoTag(repoTags []string) (repo, tag string) {
	if len(repoTags) == 0 {
		return "<none>", "<none>"
	}
	parts := strings.SplitN(repoTags[0], ":", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return parts[0], "<none>"
}

func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
