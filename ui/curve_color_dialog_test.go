package ui

import (
	"reflect"
	"testing"

	gui "github.com/egoist/mygo/ui"
	"github.com/fcying/SerialGo/config"
)

const curveColorHexLabel = "Curve color HEX (#RRGGBB or #RRGGBBAA)"

func curveColorDialogFixture(t *testing.T) (*controller, <-chan func(), string) {
	t.Helper()
	path := t.TempDir() + "/config.toml"
	s, notified := settingsSaveFixture(t, path)
	s.curve = newCurveViewState(s.settings)
	applied := s.applyCurveDraft()
	if !applied {
		t.Fatal(s.curve.error)
	}
	completeSettingsSave(t, s, notified)
	s.startCurve()
	s.curve.previewText = "VBAT=3712\nVBAT=3680\nVBAT=3740\n"
	s.previewCurve()
	if s.curve.snapshot.Matched != 3 || len(s.curve.snapshot.Series[0].Points) != 3 {
		t.Fatal("fixture did not collect preview samples")
	}
	s.curve.position = 37
	s.curve.ruleEditor = newCurveRuleEdit(s.curve.draft, 0)
	return s, notified, path
}

func TestCurveColorDialogCancellationKeepsActiveAndSavedColors(t *testing.T) {
	for _, test := range []struct {
		language, action, enabled, color, hex string
	}{
		{"en", "Cancel", "Enabled", "Curve color", curveColorHexLabel},
		{"zh-CN", "Escape", "启用", "曲线颜色", "曲线颜色 HEX (#RRGGBB 或 #RRGGBBAA)"},
	} {
		t.Run(test.language+"_"+test.action, func(t *testing.T) {
			s, _, path := curveColorDialogFixture(t)
			s.settings.Language = test.language
			before := s.curve.snapshot
			profiles := config.CloneCurveProfiles(s.settings.CurveProfiles)
			channels := append([]curveChannelDraft(nil), s.curve.draft.channels...)
			revision := s.app.SettingsRevision
			e := s.curve.ruleEditor
			tt := gui.NewTester(s.curveRuleEditorView, 380, 440)
			enabled, found := tt.AccessByRole(gui.RoleCheckBox, test.enabled)
			if !found || enabled.Offscreen {
				t.Fatal("enabled checkbox is unavailable")
			}
			well, found := tt.AccessByRole(gui.RoleColorWell, test.color)
			if !found || well.Offscreen || well.Disabled {
				t.Fatal("color picker is unavailable")
			}
			hex, found := tt.AccessByRole(gui.RoleTextField, test.hex)
			if !found || hex.Offscreen || hex.Disabled || hex.Bounds.W < 90 {
				t.Fatal("color HEX input is unavailable or too narrow")
			}
			if well.Bounds.Y >= enabled.Bounds.Y+enabled.Bounds.H || enabled.Bounds.Y >= well.Bounds.Y+well.Bounds.H || hex.Bounds.Y >= well.Bounds.Y+well.Bounds.H || well.Bounds.Y >= hex.Bounds.Y+hex.Bounds.H {
				t.Fatal("color controls added a row instead of sharing the enabled row")
			}
			setCurveProfileDialogInput(t, tt, test.hex, "#12345678")
			if e.picker != gui.Hex("#12345678") || e.channel.color != "#12345678" {
				t.Fatal("HEX edit did not update the draft and picker together")
			}
			if test.action == "Escape" {
				tt.Key(0, gui.KeyEscape)
			} else {
				err := tt.Click(test.action)
				if err != nil {
					t.Fatal(err)
				}
			}
			if s.curve.ruleEditor != nil || s.curve.dirty || !s.curve.running || !s.curve.previewing || !s.curve.paused || s.app.SettingsDirty || s.app.SettingsRevision != revision {
				t.Fatal("cancelled color edit changed acquisition or settings state")
			}
			if !reflect.DeepEqual(channels, s.curve.draft.channels) || !reflect.DeepEqual(profiles, s.settings.CurveProfiles) || !reflect.DeepEqual(profiles, readSavedSettings(t, path).CurveProfiles) || !reflect.DeepEqual(before, s.curve.snapshot) {
				t.Fatal("cancelled color edit polluted the draft, saved profile, or preview")
			}
		})
	}
}

func TestCurveColorDialogRejectsInvalidHexWithoutSaving(t *testing.T) {
	for _, value := range []string{"", "#12345", "#GG0000", "#123456789", "red", "#112233zz"} {
		t.Run(value, func(t *testing.T) {
			s, _, path := curveColorDialogFixture(t)
			before := s.curve.snapshot
			profiles := config.CloneCurveProfiles(s.settings.CurveProfiles)
			channels := append([]curveChannelDraft(nil), s.curve.draft.channels...)
			revision := s.app.SettingsRevision
			e := s.curve.ruleEditor
			tt := gui.NewTester(s.curveRuleEditorView, 420, 440)
			setCurveProfileDialogInput(t, tt, curveColorHexLabel, "#12345678")
			setCurveProfileDialogInput(t, tt, curveColorHexLabel, value)
			if e.channel.color != value || e.picker != gui.Hex("#12345678") {
				t.Fatal("invalid HEX was discarded or replaced the last valid picker color")
			}
			err := tt.Click("OK")
			if err != nil {
				t.Fatal(err)
			}
			if s.curve.ruleEditor != e || e.error.en == "" || e.error.zh == "" || e.channel.color != value || s.curve.dirty || s.app.SettingsDirty || s.app.SettingsRevision != revision {
				t.Fatal("invalid HEX was accepted, rewritten, or lost its localized error")
			}
			if !s.curve.running || !s.curve.previewing || !s.curve.paused || !reflect.DeepEqual(before, s.curve.snapshot) || !reflect.DeepEqual(channels, s.curve.draft.channels) || !reflect.DeepEqual(profiles, s.settings.CurveProfiles) || !reflect.DeepEqual(profiles, readSavedSettings(t, path).CurveProfiles) {
				t.Fatal("invalid color edit changed active settings, saved colors, or preview samples")
			}
		})
	}
}

func TestCurveColorDialogConfirmsHexAndPickerWithoutLosingPreview(t *testing.T) {
	for _, test := range []struct {
		name, input, want string
		picker, opacity   bool
	}{
		{"hex_rgb", "#123456", "#123456", false, false},
		{"hex_rgba", "#12345678", "#12345678", false, false},
		{"picker_rgb", "#3264c8ff", "#3264C8", true, false},
		{"picker_rgba", "#3264c880", "#3264C880", true, false},
		{"picker_opacity", "", "#EF444480", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, notified, path := curveColorDialogFixture(t)
			before := s.curve.snapshot
			e := s.curve.ruleEditor
			tt := gui.NewTester(s.curveRuleEditorView, 420, 440)
			if test.picker {
				err := tt.Click("Curve color")
				if err != nil {
					t.Fatal(err)
				}
				if test.opacity {
					err = tt.Click("Red")
					if err != nil {
						t.Fatal(err)
					}
					opacity, found := tt.AccessByRole(gui.RoleSlider, "Opacity")
					if !found || opacity.Disabled || opacity.Offscreen {
						t.Fatal("native opacity slider is unavailable")
					}
					tt.ClickAt(opacity.Bounds.X+opacity.Bounds.W/2, opacity.Bounds.Y+opacity.Bounds.H/2)
				} else {
					setCurveProfileDialogInput(t, tt, "Hex", test.input)
				}
				// Dismiss the popover without invoking the editor's Escape shortcut.
				err = tt.Click("Curve name")
				if err != nil {
					t.Fatal(err)
				}
			} else {
				setCurveProfileDialogInput(t, tt, curveColorHexLabel, test.input)
			}
			hex, found := tt.AccessByRole(gui.RoleTextField, curveColorHexLabel)
			if !found || hex.Value != test.want || e.channel.color != test.want || e.picker != gui.Hex(test.want) {
				t.Fatalf("picker and HEX disagree: field=%q draft=%q picker=%+v", hex.Value, e.channel.color, e.picker)
			}
			if !reflect.DeepEqual(before, s.curve.snapshot) || s.app.SettingsDirty {
				t.Fatal("unconfirmed color changed active metadata or preview")
			}
			err := tt.Click("OK")
			if err != nil {
				t.Fatal(err)
			}
			if s.curve.ruleEditor != nil || s.curve.dirty || !s.curve.running || !s.curve.previewing || !s.curve.paused || s.curve.position != 37 || s.settings.CurveProfiles[0].Channels[0].Color != test.want {
				t.Fatal("confirmed color was not applied without changing acquisition state")
			}
			if s.curve.snapshot.Matched != before.Matched || len(s.curve.snapshot.Series) != len(before.Series) || !reflect.DeepEqual(s.curve.snapshot.Series[0].Points, before.Series[0].Points) || &s.curve.snapshot.Series[0].Points[0] != &before.Series[0].Points[0] {
				t.Fatal("color-only confirmation discarded or regenerated preview samples")
			}
			completeSettingsSave(t, s, notified)
			stored := readSavedSettings(t, path)
			if s.app.SettingsDirty || stored.CurveProfiles[0].Channels[0].Color != test.want {
				t.Fatal("confirmed color was not persisted")
			}
		})
	}
}
