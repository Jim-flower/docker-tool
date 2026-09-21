package docker

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeCompose(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "docker-compose.yml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write compose file: %v", err)
	}
	return path
}

func TestParseComposeImages(t *testing.T) {
	path := writeCompose(t, `
services:
  web:
    image: nginx:1.27
  db:
    image: postgres
  cache:
    image: redis:7
  web2:
    image: nginx:1.27
  buildonly:
    build: .
`)
	got, err := ParseComposeImages(path)
	if err != nil {
		t.Fatalf("ParseComposeImages: %v", err)
	}
	want := []string{"nginx:1.27", "postgres:latest", "redis:7"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseComposeImagesInterpolation(t *testing.T) {
	t.Setenv("APP_TAG", "3.14")
	path := writeCompose(t, `
services:
  app:
    image: "registry.example.com/app:${APP_TAG}"
  sidecar:
    image: 'busybox:${MISSING_TAG:-1.36}'
`)
	got, err := ParseComposeImages(path)
	if err != nil {
		t.Fatalf("ParseComposeImages: %v", err)
	}
	want := []string{"busybox:1.36", "registry.example.com/app:3.14"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseComposeImagesRegistryPort(t *testing.T) {
	path := writeCompose(t, `
services:
  app:
    image: localhost:5000/myapp
`)
	got, err := ParseComposeImages(path)
	if err != nil {
		t.Fatalf("ParseComposeImages: %v", err)
	}
	want := []string{"localhost:5000/myapp:latest"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseComposeImagesEmpty(t *testing.T) {
	path := writeCompose(t, `
services:
  app:
    build: .
`)
	if _, err := ParseComposeImages(path); err == nil {
		t.Fatal("expected error for compose file without images")
	}
}

func TestParseComposeImagesInvalidYAML(t *testing.T) {
	path := writeCompose(t, "services: [unclosed")
	if _, err := ParseComposeImages(path); err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}
