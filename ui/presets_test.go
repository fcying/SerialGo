package ui

import (
	gui "github.com/egoist/mygo/ui"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"
	"path/filepath"
	"slices"
	"testing"
)

func TestPresetAddDeleteKeepsRemainingDataAndEmptyList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	s, notified := settingsSaveFixture(t, path)
	s.settings.ShowPresets = true
	s.presetSplit = presetReceiveShare
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
	if !slices.Equal(s.settings.Presets, before) || s.app.SettingsDirty {
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

func TestPresetLimitDisablesAdd(t *testing.T) {
	s := newController(&internal.App{Settings: config.Settings{Language: "en", ShowPresets: true}})
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
}

func TestPresetRowsStayTwoLinesAtMinimumPaneWidth(t *testing.T) {
	for _, language := range []string{"en", "zh-CN"} {
		t.Run(language, func(t *testing.T) {
			s := newController(&internal.App{Settings: config.Settings{
				Language: language, ShowPresets: true,
				Presets: []config.Preset{{Label: "Query device name", Data: "AT+NAME?", Interval: "1000", Enabled: true, CRLF: true}},
			}})
			s.presetSplit = 80
			s.configureLog()
			tt := gui.NewTester(s.view, 800, 600)
			type box struct{ x, y, w, h float32 }
			find := func(label string) box {
				t.Helper()
				bounds, ok := tt.Find(label)
				if !ok {
					t.Fatalf("control %q is missing", label)
				}
				return box{x: bounds.X, y: bounds.Y, w: bounds.W, h: bounds.H}
			}
			note := find(s.tr("条目备注", "Preset note"))
			hex := find(s.tr("第 1 条 HEX", "Preset 1 HEX"))
			crlf := find(s.tr("第 1 条 CRLF", "Preset 1 CRLF"))
			intervalLabelInfo, intervalLabelFound := tt.AccessByRole(gui.RoleText, s.tr("间隔", "Interval"))
			if !intervalLabelFound {
				t.Fatal("preset interval label is missing")
			}
			intervalLabel := box{x: intervalLabelInfo.Bounds.X, y: intervalLabelInfo.Bounds.Y, w: intervalLabelInfo.Bounds.W, h: intervalLabelInfo.Bounds.H}
			interval := find(s.tr("条目发送间隔 (ms)", "Preset interval (ms)"))
			deleteButton := find(s.tr("删除第 1 条", "Delete preset 1"))
			index := find("01")
			data := find(s.tr("条目内容", "Preset data"))
			sendButton := find(s.tr("发送第 1 条", "Send preset 1"))
			optionCenter := note.y + note.h/2
			for label, bounds := range map[string]box{
				"HEX": hex, "CRLF": crlf, "interval label": intervalLabel, "interval": interval, "delete": deleteButton, "index": index,
			} {
				center := bounds.y + bounds.h/2
				if delta := center - optionCenter; delta < -1 || delta > 1 {
					t.Errorf("%s is not aligned with the metadata row: %+v vs %+v", label, bounds, note)
				}
			}
			if intervalLabel.x <= crlf.x+crlf.w || interval.x <= intervalLabel.x+intervalLabel.w {
				t.Errorf("interval label must be between CRLF and interval field: CRLF=%+v label=%+v interval=%+v", crlf, intervalLabel, interval)
			}
			if data.y <= note.y || data.w < 200 {
				t.Errorf("data field should have its own wide row below metadata: note=%+v data=%+v", note, data)
			}
			if delta := data.y + data.h/2 - (sendButton.y + sendButton.h/2); delta < -1 || delta > 1 {
				t.Errorf("send button is not aligned with the data row: %+v vs %+v", sendButton, data)
			}
			if note.w < presetNoteMinWidth {
				t.Errorf("preset pane is too narrow for its note field: %+v", note)
			}
		})
	}
}
