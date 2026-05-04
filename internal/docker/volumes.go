package docker

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/volume"
)

// Volume represents a Docker volume.
type Volume struct {
	Name       string
	Driver     string
	Mountpoint string
	Labels     map[string]string
}

// ListVolumes returns all local Docker volumes.
func (c *Client) ListVolumes(ctx context.Context) ([]Volume, error) {
	resp, err := c.cli.VolumeList(ctx, volume.ListOptions{Filters: filters.Args{}})
	if err != nil {
		return nil, fmt.Errorf("listing volumes: %w", err)
	}

	volumes := make([]Volume, 0, len(resp.Volumes))
	for _, v := range resp.Volumes {
		volumes = append(volumes, Volume{
			Name:       v.Name,
			Driver:     v.Driver,
			Mountpoint: v.Mountpoint,
			Labels:     v.Labels,
		})
	}
	return volumes, nil
}
