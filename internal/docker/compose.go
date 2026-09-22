package docker

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// composeFile mirrors the subset of the Compose specification we need.
type composeFile struct {
	Services map[string]composeService `yaml:"services"`
	Volumes  map[string]composeVolume  `yaml:"volumes"`
}

type composeService struct {
	Image   string        `yaml:"image"`
	Volumes []interface{} `yaml:"volumes"`
}

type composeVolume struct {
	Name string `yaml:"name"`
}

// composeVarPattern matches $VAR, ${VAR} and ${VAR:-default} substitutions.
var composeVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// interpolateEnv replaces compose-style variable references using the
// process environment, honouring ${VAR:-default} fallbacks.
func interpolateEnv(s string) string {
	return composeVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		parts := composeVarPattern.FindStringSubmatch(match)
		name, def := parts[1], parts[2]
		if name == "" {
			name = parts[3]
		}
		if value, ok := os.LookupEnv(name); ok && value != "" {
			return value
		}
		return def
	})
}

// normalizeImageRef appends ":latest" when the reference has neither a tag
// nor a digest, matching Docker's default pull behaviour.
func normalizeImageRef(ref string) string {
	if strings.Contains(ref, "@") {
		return ref
	}
	lastSlash := strings.LastIndex(ref, "/")
	if idx := strings.LastIndex(ref, ":"); idx == -1 || idx < lastSlash {
		return ref + ":latest"
	}
	return ref
}

// ParseComposeImages extracts the unique, normalised image references
// (repo:tag) declared by the services of a docker-compose file.
func ParseComposeImages(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read compose file: %w", err)
	}

	var cf composeFile
	if err := yaml.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("parse compose file: %w", err)
	}

	seen := make(map[string]struct{}, len(cf.Services))
	images := make([]string, 0, len(cf.Services))
	for _, svc := range cf.Services {
		img := strings.TrimSpace(interpolateEnv(svc.Image))
		if img == "" {
			continue
		}
		ref := normalizeImageRef(img)
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		images = append(images, ref)
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("no services with an 'image' field found in %s", path)
	}

	sort.Strings(images)
	return images, nil
}

// composeVolumeSource extracts the volume name from a service volume mount
// specification. Returns "" for bind mounts (host paths), which cannot be
// exported as Docker volumes.
func composeVolumeSource(spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" || strings.HasPrefix(spec, "/") || strings.HasPrefix(spec, "~") ||
		strings.HasPrefix(spec, ".") {
		return ""
	}
	// Windows bind path, e.g. C:\data:/data
	if len(spec) >= 2 && spec[1] == ':' {
		return ""
	}
	src := strings.SplitN(spec, ":", 2)[0]
	if src == "" || strings.ContainsAny(src, `/\`) {
		return ""
	}
	return src
}

// ParseComposeVolumes extracts the unique names of named volumes mounted by
// the services of a docker-compose file. Custom names declared via the
// top-level `volumes: <key>: name:` attribute are honoured, and bind mounts
// are skipped.
func ParseComposeVolumes(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read compose file: %w", err)
	}

	var cf composeFile
	if err := yaml.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("parse compose file: %w", err)
	}

	// Top-level custom names: volumes: <key>: name: <actual>
	customNames := make(map[string]string, len(cf.Volumes))
	for key, vol := range cf.Volumes {
		if name := strings.TrimSpace(interpolateEnv(vol.Name)); name != "" {
			customNames[key] = name
		}
	}

	seen := make(map[string]struct{})
	volumes := make([]string, 0)
	add := func(name string) {
		name = strings.TrimSpace(interpolateEnv(name))
		if name == "" {
			return
		}
		if actual, ok := customNames[name]; ok {
			name = actual
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		volumes = append(volumes, name)
	}

	for _, svc := range cf.Services {
		for _, entry := range svc.Volumes {
			switch v := entry.(type) {
			case string:
				add(composeVolumeSource(v))
			case map[string]interface{}:
				// Long syntax: {type: volume, source: data, target: /data}
				if t, _ := v["type"].(string); t != "" && t != "volume" {
					continue
				}
				src, _ := v["source"].(string)
				if src == "" {
					src, _ = v["src"].(string)
				}
				add(composeVolumeSource(src))
			}
		}
	}

	if len(volumes) == 0 {
		return nil, fmt.Errorf("no named volumes found in %s", path)
	}

	sort.Strings(volumes)
	return volumes, nil
}

// MatchVolumesByName resolves compose volume names against the local volume
// store. Besides exact matches, a local volume named "<project>_<name>" also
// matches, because docker compose prefixes volume names with the project
// name unless an explicit name is set. Unresolvable names go to missing.
func (c *Client) MatchVolumesByName(ctx context.Context, names []string) (matched []Volume, missing []string, err error) {
	vols, err := c.ListVolumes(ctx)
	if err != nil {
		return nil, nil, err
	}

	byName := make(map[string]Volume, len(vols))
	for _, v := range vols {
		byName[v.Name] = v
	}

	for _, name := range names {
		if v, ok := byName[name]; ok {
			matched = append(matched, v)
			continue
		}
		// Fall back to a "<project>_<name>" suffix match.
		var found *Volume
		for _, v := range vols {
			if strings.HasSuffix(v.Name, "_"+name) {
				candidate := v
				found = &candidate
				break
			}
		}
		if found != nil {
			matched = append(matched, *found)
		} else {
			missing = append(missing, name)
		}
	}
	return matched, missing, nil
}

// MatchImagesByRef resolves image references (repo:tag) against the local
// image store. References that cannot be resolved are returned in missing.
func (c *Client) MatchImagesByRef(ctx context.Context, refs []string) (matched []Image, missing []string, err error) {
	all, err := c.ListImages(ctx)
	if err != nil {
		return nil, nil, err
	}

	byRef := make(map[string]Image, len(all))
	for _, img := range all {
		if img.Repository == "<none>" || img.Repository == "" {
			continue
		}
		if _, exists := byRef[img.DisplayName()]; !exists {
			byRef[img.DisplayName()] = img
		}
	}

	for _, ref := range refs {
		if img, ok := byRef[ref]; ok {
			matched = append(matched, img)
		} else {
			missing = append(missing, ref)
		}
	}
	return matched, missing, nil
}
