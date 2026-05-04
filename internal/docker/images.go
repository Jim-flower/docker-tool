package docker

import (
	"context"
	"fmt"
	"strings"

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
