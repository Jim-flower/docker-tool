package ui

import (
	"strings"
	"testing"
)

// The menu is index-driven, so the row order and the action each row triggers
// have to stay in sync.
func TestMenuIndexesMatchActions(t *testing.T) {
	cases := []struct {
		idx    int
		action action
	}{
		{0, actionExportImages},
		{1, actionExportRunningContainerImages},
		{3, actionExportVolumes},
		{5, actionImportImages},
		{6, actionImportVolumes},
		{7, actionRemoveImages},
		{8, actionRemoveVolumes},
		{9, actionRemoveContainers},
	}

	for _, tc := range cases {
		app := NewApp(nil)
		app.menuIdx = tc.idx
		app.activateMenuItem()
		if app.action != tc.action {
			t.Errorf("menu index %d selected action %v, want %v", tc.idx, app.action, tc.action)
		}
	}

	if last := menuItems[len(menuItems)-1]; !last.divider || last.label != "Quit" {
		t.Errorf("last menu row should be the divider-prefixed Quit, got %+v", last)
	}
}

func TestMenuViewRendersSections(t *testing.T) {
	app := NewApp(nil)
	app.windowWidth = 80

	view := app.View()
	for _, want := range []string{
		"EXPORT", "IMPORT", "CLEANUP",
		"Export Images", "Import Volumes", "Remove Images", "Remove Containers", "Quit",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("menu view is missing %q:\n%s", want, view)
		}
	}
	t.Log("\n" + view)
}

func TestConfirmViewShowsImpactAndForceToggle(t *testing.T) {
	app := NewApp(nil)
	app.windowWidth = 80
	app.screen = screenConfirm
	app.confirm = confirmState{
		title:      "Delete Docker Images",
		rows:       [][2]string{{"Items", "3 image(s)"}, {"Reclaimable", "1.2 GB"}},
		warnings:   []string{"2 image(s) carry several tags; deleting them drops every tag."},
		allowForce: true,
		forceLabel: "also remove images referenced by stopped containers",
	}

	view := app.View()
	for _, want := range []string{
		"Delete Docker Images", "3 image(s)", "1.2 GB",
		"several tags", "Force [ off ]", "toggle force",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("confirm view is missing %q:\n%s", want, view)
		}
	}

	app.confirm.force = true
	if view := app.View(); !strings.Contains(view, "Force [ on ]") {
		t.Errorf("confirm view does not reflect force being enabled:\n%s", view)
	}
	t.Log("\n" + app.View())
}
