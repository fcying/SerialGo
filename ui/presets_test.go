package ui

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/fcying/SerialGo/config"

	gui "github.com/egoist/mygo/ui"
)

func TestPresetAddDeleteKeepsRemainingDataAndEmptyList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	s, notified := settingsSaveFixture(t, path, config.Save)
	s.settings.ShowPresets = true
	s.presetSplit = 64
	first := config.Preset{Label: "first", Data: "01 02", Interval: "25", Hex: true}
	second := config.Preset{Label: "second", Data: "keep\ncontent", Interval: "75", CRLF: true, Enabled: true}
	s.settings.Presets = []config.Preset{first, second}
	s.configureLog()
	tt := gui.NewTester(s.view, 800, 600)
	if err := tt.Click("Add"); err != nil {
		t.Fatal(err)
	}
	completeSettingsSave(t, s, notified)
	stored := readSavedSettings(t, path)
	if len(stored.Presets) != 3 || stored.Presets[2].Data != "" || stored.Presets[0] != first || stored.Presets[1] != second {
		t.Fatalf("added blank entry was not preserved with existing data: %+v", stored.Presets)
	}
	before := slices.Clone(s.settings.Presets)
	if err := tt.Click("Delete preset 1"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.settings.Presets, before) || s.settingsDirty {
		t.Fatal("canceling deletion changed presets or marked settings dirty")
	}
	for remaining := 2; remaining >= 0; remaining-- {
		if err := tt.Click("Delete preset 1"); err != nil {
			t.Fatal(err)
		}
		if err := tt.Click("Confirm preset deletion"); err != nil {
			t.Fatal(err)
		}
		completeSettingsSave(t, s, notified)
		stored = readSavedSettings(t, path)
		if len(stored.Presets) != remaining {
			t.Fatalf("deleted entry returned in saved list: %+v", stored.Presets)
		}
		if remaining == 2 && stored.Presets[0] != second {
			t.Fatal("deleting first entry changed the following entry's data or flags")
		}
	}
	if err := tt.Click("Add"); err != nil {
		t.Fatal(err)
	}
	completeSettingsSave(t, s, notified)
	if stored := readSavedSettings(t, path); len(stored.Presets) != 1 || stored.Presets[0].Data != "" {
		t.Fatalf("could not add a fresh entry after deleting all presets: %+v", stored.Presets)
	}
}

func TestPresetLimitAndActiveSendPreventStructuralEdits(t *testing.T) {
	s := &controller{settings: config.Settings{Language: "en", ShowPresets: true}, presetSplit: 64}
	for range config.PresetCount {
		s.addPreset()
	}
	s.configureLog()
	tt := gui.NewTester(s.view, 800, 600)
	button, found := tt.AccessByRole(gui.RoleButton, "Add")
	if !found || !button.Disabled {
		t.Fatal("Add must be unavailable at the preset limit")
	}
	s.addPreset()
	if len(s.settings.Presets) != config.PresetCount {
		t.Fatal("Add exceeded the supported preset count")
	}
	s.settings.Presets = []config.Preset{{Label: "active", Data: "keep"}}
	s.cancelSend = func() {}
	s.addPreset()
	s.openPresetDelete(0)
	s.applyPresetDelete()
	if len(s.settings.Presets) != 1 || s.settings.Presets[0].Data != "keep" || s.presetDeleteOpen {
		t.Fatal("active send allowed a structural preset edit")
	}
}
