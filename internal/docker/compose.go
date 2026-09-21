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
}

type composeService struct {
	Image string `yaml:"image"`
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
