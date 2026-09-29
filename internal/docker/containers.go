package docker

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/docker/docker/api/types/container"
)

// Container represents a Docker container with display-friendly fields.
type Container struct {
	ID      string
	ShortID string
	Name    string
	Image   string
	State   string // running, exited, created, paused, restarting, dead
	Status  string // human-readable, e.g. "Exited (0) 2 hours ago"
	Created int64
}

// DisplayName returns a human-readable name for the container.
func (c Container) DisplayName() string { return c.Name }

// IsRunning reports whether the container currently occupies its image.
func (c Container) IsRunning() bool {
	switch c.State {
	case "running", "paused", "restarting":
		return true
	}
	return false
}

// ListContainers returns the containers known to the daemon. Stopped containers
// come first because they are the usual cleanup targets; each group is sorted
// by name so the order stays stable between runs.
func (c *Client) ListContainers(ctx context.Context, all bool) ([]Container, error) {
	list, err := c.cli.ContainerList(ctx, container.ListOptions{All: all})
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}

	containers := make([]Container, 0, len(list))
	for _, ctr := range list {
		name := ""
		if len(ctr.Names) > 0 {
			name = strings.TrimPrefix(ctr.Names[0], "/")
		}
		if name == "" {
			name = shortID(ctr.ID)
		}
		containers = append(containers, Container{
			ID:      ctr.ID,
			ShortID: shortID(ctr.ID),
			Name:    name,
			Image:   ctr.Image,
			State:   ctr.State,
			Status:  ctr.Status,
			Created: ctr.Created,
		})
	}

	sort.SliceStable(containers, func(i, j int) bool {
		if containers[i].IsRunning() != containers[j].IsRunning() {
			return !containers[i].IsRunning()
		}
		return containers[i].Name < containers[j].Name
	})

	return containers, nil
}
