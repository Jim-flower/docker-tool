package operations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTar(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("stub"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func readScript(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// The layout of a freshly written script is what every existing bundle looks
// like, so it is pinned here to keep the append logic honest.
func TestWriteImageImportScriptLayout(t *testing.T) {
	dir := t.TempDir()
	tar := writeTar(t, dir, "nginx_latest.tar")

	if _, err := WriteImageImportScript(dir, []ExportResult{{Name: "nginx:latest", FilePath: tar}}); err != nil {
		t.Fatalf("WriteImageImportScript: %v", err)
	}

	want := strings.Join([]string{
		"#!/usr/bin/env sh",
		`SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)`,
		`command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 1; }`,
		`failed=0`,
		`echo "Importing Docker images..."`,
		`echo "Loading: nginx:latest"`,
		`docker load -i "$SCRIPT_DIR"/'nginx_latest.tar' || { echo "  ERROR: failed to load nginx:latest" >&2; failed=$((failed+1)); }`,
		`[ "$failed" -eq 0 ] || echo "WARNING: $failed image(s) failed to load" >&2`,
		`echo "Image import complete."`,
		"",
	}, "\n")

	if got := readScript(t, dir, "import-images.sh"); got != want {
		t.Fatalf("unexpected script:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestWriteVolumeImportScriptLayout(t *testing.T) {
	dir := t.TempDir()
	tar := writeTar(t, dir, "data.tar")

	if _, err := WriteVolumeImportScript(dir, []ExportResult{{Name: "data", FilePath: tar}}); err != nil {
		t.Fatalf("WriteVolumeImportScript: %v", err)
	}

	want := strings.Join([]string{
		"#!/usr/bin/env sh",
		`SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)`,
		`command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 1; }`,
		`failed=0`,
		`echo "Importing Docker volumes..."`,
		`echo "Restoring volume: data"`,
		`docker volume create 'data' >/dev/null || { echo "  ERROR: failed to create volume data" >&2; failed=$((failed+1)); }`,
		`docker run --rm -i -v 'data:/data' alpine:latest sh -c 'tar -xf - -C /data' < "$SCRIPT_DIR"/'data.tar' || { echo "  ERROR: failed to restore volume data" >&2; failed=$((failed+1)); }`,
		`[ "$failed" -eq 0 ] || echo "WARNING: $failed item(s) failed to import" >&2`,
		`echo "Volume import complete."`,
		"",
	}, "\n")

	if got := readScript(t, dir, "import-volumes.sh"); got != want {
		t.Fatalf("unexpected script:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestAppendImageImportScriptKeepsExistingEntries(t *testing.T) {
	dir := t.TempDir()
	first := writeTar(t, dir, "a.tar")
	second := writeTar(t, dir, "b.tar")

	if _, err := WriteImageImportScript(dir, []ExportResult{{Name: "a:1", FilePath: first}}); err != nil {
		t.Fatalf("fresh write: %v", err)
	}
	if _, err := AppendImageImportScript(dir, []ExportResult{{Name: "b:1", FilePath: second}}); err != nil {
		t.Fatalf("append: %v", err)
	}

	content := readScript(t, dir, "import-images.sh")

	if !strings.HasPrefix(content, "#!/usr/bin/env sh\n") {
		t.Fatalf("header was damaged:\n%s", content)
	}
	for _, want := range []string{`echo "Loading: a:1"`, `echo "Loading: b:1"`} {
		if got := strings.Count(content, want); got != 1 {
			t.Fatalf("%q appears %d times, want 1:\n%s", want, got, content)
		}
	}
	// The new entry must land inside the body, before the failure counter that
	// summarises the whole run.
	footer := strings.Index(content, `[ "$failed" -eq 0 ]`)
	appended := strings.Index(content, `echo "Loading: b:1"`)
	if appended < 0 || appended > footer {
		t.Fatalf("appended entry not placed before the summary block:\n%s", content)
	}
	if !strings.HasSuffix(content, "echo \"Image import complete.\"\n") {
		t.Fatalf("trailing summary was damaged:\n%s", content)
	}
}

func TestAppendImageImportScriptIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	tar := writeTar(t, dir, "a.tar")
	result := ExportResult{Name: "a:1", FilePath: tar}

	if _, err := WriteImageImportScript(dir, nil); err != nil {
		t.Fatalf("fresh write: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := AppendImageImportScript(dir, []ExportResult{result}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	content := readScript(t, dir, "import-images.sh")
	if got := strings.Count(content, `echo "Loading: a:1"`); got != 1 {
		t.Fatalf("load line appears %d times, want 1:\n%s", got, content)
	}
	// The "nothing to import" placeholder stops being true once an entry lands.
	if strings.Contains(content, "No image archives found") {
		t.Fatalf("stale placeholder kept:\n%s", content)
	}
}

func TestAppendVolumeImportScriptKeepsExistingEntries(t *testing.T) {
	dir := t.TempDir()
	first := writeTar(t, dir, "data.tar")
	second := writeTar(t, dir, "logs.tar")

	if _, err := WriteVolumeImportScript(dir, []ExportResult{{Name: "data", FilePath: first}}); err != nil {
		t.Fatalf("fresh write: %v", err)
	}
	if _, err := AppendVolumeImportScript(dir, []ExportResult{{Name: "logs", FilePath: second}}); err != nil {
		t.Fatalf("append: %v", err)
	}

	content := readScript(t, dir, "import-volumes.sh")
	for _, want := range []string{`echo "Restoring volume: data"`, `echo "Restoring volume: logs"`} {
		if got := strings.Count(content, want); got != 1 {
			t.Fatalf("%q appears %d times, want 1:\n%s", want, got, content)
		}
	}
}

func TestAppendRefusesForeignScript(t *testing.T) {
	dir := t.TempDir()
	writeTar(t, dir, "a.tar")
	if err := os.WriteFile(filepath.Join(dir, "import-images.sh"), []byte("echo hi\n"), 0o644); err != nil {
		t.Fatalf("seed script: %v", err)
	}

	_, err := AppendImageImportScript(dir, []ExportResult{{Name: "a:1", FilePath: filepath.Join(dir, "a.tar")}})
	if err == nil {
		t.Fatal("expected an error for a script we did not generate")
	}
	if got := readScript(t, dir, "import-images.sh"); got != "echo hi\n" {
		t.Fatalf("foreign script was modified: %q", got)
	}
}

func TestInspectBundleDetectsExistingExport(t *testing.T) {
	dir := t.TempDir()

	if status := InspectBundle(dir); status.Exists() {
		t.Fatalf("empty dir reported as an export: %+v", status)
	}

	imageTar := writeTar(t, dir, "a.tar")
	volumeTar := writeTar(t, dir, "data.tar")
	if _, err := WriteImageImportScript(dir, []ExportResult{{Name: "a:1", FilePath: imageTar}}); err != nil {
		t.Fatalf("images: %v", err)
	}
	if _, err := WriteVolumeImportScript(dir, []ExportResult{{Name: "data", FilePath: volumeTar}}); err != nil {
		t.Fatalf("volumes: %v", err)
	}

	status := InspectBundle(dir)
	if !status.Exists() || status.Images != 1 || status.Volumes != 1 {
		t.Fatalf("unexpected status: %+v", status)
	}

	// A bundle written by an older version has no manifest; the scripts alone
	// must still be recognised and counted.
	if err := os.Remove(filepath.Join(dir, bundleManifestName)); err != nil {
		t.Fatalf("remove manifest: %v", err)
	}
	status = InspectBundle(dir)
	if !status.Exists() || status.Images != 1 || status.Volumes != 1 {
		t.Fatalf("legacy bundle not detected: %+v", status)
	}
}

func TestBundleManifestTracksAppend(t *testing.T) {
	dir := t.TempDir()
	first := writeTar(t, dir, "a.tar")
	second := writeTar(t, dir, "b.tar")

	if _, err := WriteImageImportScript(dir, []ExportResult{{Name: "a:1", FilePath: first}}); err != nil {
		t.Fatalf("fresh write: %v", err)
	}
	if _, err := AppendImageImportScript(dir, []ExportResult{{Name: "b:1", FilePath: second}}); err != nil {
		t.Fatalf("append: %v", err)
	}

	manifest := LoadBundleManifest(dir)
	if len(manifest.Images) != 2 {
		t.Fatalf("manifest holds %d images, want 2: %+v", len(manifest.Images), manifest.Images)
	}
	if manifest.Images[1].Name != "b:1" || manifest.Images[1].File != "b.tar" {
		t.Fatalf("unexpected manifest entry: %+v", manifest.Images[1])
	}
}

func TestMergeBundleItemsRefreshesFileName(t *testing.T) {
	existing := []BundleItem{{Name: "a:1", File: "old.tar"}, {Name: "b:1", File: "b.tar"}}
	added := []BundleItem{{Name: "a:1", File: "new.tar"}, {Name: "c:1", File: "c.tar"}}

	merged := mergeBundleItems(existing, added)
	if len(merged) != 3 {
		t.Fatalf("merged length = %d, want 3: %+v", len(merged), merged)
	}
	if merged[0].File != "new.tar" {
		t.Fatalf("existing entry not refreshed: %+v", merged[0])
	}
	if merged[2].Name != "c:1" {
		t.Fatalf("new entry not appended: %+v", merged)
	}
}
