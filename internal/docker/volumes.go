package docker

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/volume"
)

// Volume represents a Docker volume.
type Volume struct {
	Name       string
	Driver     string
	Mountpoint string
	Labels     map[string]string
	Size       int64
	RefCount   int64
}

// ListVolumes returns all local Docker volumes.
func (c *Client) ListVolumes(ctx context.Context) ([]Volume, error) {
	resp, err := c.cli.VolumeList(ctx, volume.ListOptions{Filters: filters.Args{}})
	if err != nil {
		return nil, fmt.Errorf("listing volumes: %w", err)
	}

	usageByName := map[string]*volume.UsageData{}
	if du, err := c.cli.DiskUsage(ctx, types.DiskUsageOptions{Types: []types.DiskUsageObject{types.VolumeObject}}); err == nil {
		for _, v := range du.Volumes {
			if v != nil && v.UsageData != nil {
				usageByName[v.Name] = v.UsageData
			}
		}
	}

	volumes := make([]Volume, 0, len(resp.Volumes))
	for _, v := range resp.Volumes {
		size := int64(-1)
		refCount := int64(-1)
		if usage := usageByName[v.Name]; usage != nil {
			size = usage.Size
			refCount = usage.RefCount
		}
		volumes = append(volumes, Volume{
			Name:       v.Name,
			Driver:     v.Driver,
			Mountpoint: v.Mountpoint,
			Labels:     v.Labels,
			Size:       size,
			RefCount:   refCount,
		})
	}
	return volumes, nil
}
