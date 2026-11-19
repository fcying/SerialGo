package ui

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	gui "github.com/egoist/mygo/ui"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"
)

func setCurveProfileDialogInput(t *testing.T, tt *gui.Tester, label, value string) {
	t.Helper()
	field, found := tt.AccessByRole(gui.RoleTextField, label)
	if !found || field.Disabled || field.Offscreen {
		t.Fatalf("configuration field %q is unavailable", label)
	}
	tt.ClickAt(field.Bounds.X+field.Bounds.W/2, field.Bounds.Y+field.Bounds.H/2)
	tt.Key(gui.Ctrl, gui.KeyA)
	tt.Key(0, gui.KeyBackspace)
	tt.Type(value)
}

func TestCurveProfileDialogFitsSmallScaledWindows(t *testing.T) {
	for _, language := range []string{"en", "zh-CN"} {
		for _, scale := range []float32{1, 1.25, 1.5} {
			for _, width := range []int{300, 320} {
				t.Run(fmt.Sprintf("%s_scale_%g_width_%d", language, scale, width), func(t *testing.T) {
					s := newController(&internal.App{Settings: config.Settings{Language: language}})
					s.curve = newCurveViewState(s.settings)
					s.curve.profileEditor = newCurveProfileEdit(s.curve.draft)
					tt := gui.NewTester(s.curveProfileEditorView, width, 200)
					tt.SetScale(scale)
					for _, label := range []string{s.tr("方案名称", "Profile name"), s.tr("时间正则 (可选)", "Time regex (optional)"), s.tr("时间倍率", "Time scale")} {
						field, found := tt.AccessByRole(gui.RoleTextField, label)
						if !found || field.Offscreen || field.Bounds.W <= 0 || field.Bounds.H <= 0 || field.Bounds.X < 0 || field.Bounds.Y < 0 || field.Bounds.X+field.Bounds.W > float32(width) || field.Bounds.Y+field.Bounds.H > 200 {
							t.Fatalf("configuration field %q exceeds the window: %+v", label, field.Bounds)
						}
					}
					for _, withError := range []bool{false, true} {
						if withError {
							s.curve.profileEditor.error = localizedText{zh: strings.Repeat("无效的时间正则. ", 40), en: strings.Repeat("Invalid time regex. ", 40)}
							tt.Frame()
							errorBounds, found := tt.Find(s.tr(s.curve.profileEditor.error.zh, s.curve.profileEditor.error.en))
							if !found || errorBounds.H <= 0 || errorBounds.Y < 0 || errorBounds.Y+errorBounds.H > 200 {
								t.Fatalf("configuration error is hidden in the compact window: %+v", errorBounds)
							}
						}
						for _, label := range []string{s.tr("取消", "Cancel"), s.tr("确定", "OK")} {
							button, found := tt.AccessByRole(gui.RoleButton, label)
							if !found || button.Offscreen || button.Bounds.W <= 0 || button.Bounds.H <= 0 || button.Bounds.X < 0 || button.Bounds.Y < 0 || button.Bounds.X+button.Bounds.W > float32(width) || button.Bounds.Y+button.Bounds.H > 200 {
								t.Fatalf("configuration action %q exceeds the window (error=%v): %+v", label, withError, button.Bounds)
							}
						}
					}
				})
			}
		}
	}
}

func TestCurveProfileDialogRenamePreservesPreview(t *testing.T) {
	app := &internal.App{Settings: config.Settings{Language: "en", CurveProfiles: []config.CurveProfile{config.NewCurveProfile("firmware")}}}
	s := newController(app)
	s.curve = newCurveViewState(s.settings)
	s.startCurve()
	s.curve.previewText = "VBAT=3712\nVBAT=3680\nVBAT=3740\n"
	s.previewCurve()
	before := s.curve.snapshot
	if before.Matched != 3 || len(before.Series[0].Points) != 3 {
		t.Fatalf("test did not collect preview samples: %+v", before)
	}
	channels := append([]curveChannelDraft(nil), s.curve.draft.channels...)
	s.curve.profileEditor = newCurveProfileEdit(s.curve.draft)
	tt := gui.NewTester(s.curveProfileEditorView, 400, 360)
	setCurveProfileDialogInput(t, tt, "Profile name", "renamed firmware")
	if app.Settings.CurveProfiles[0].Name != "firmware" || app.SettingsDirty || !reflect.DeepEqual(before, s.curve.snapshot) {
		t.Fatal("unconfirmed rename changed stored settings or collected preview samples")
	}
	err := tt.Click("OK")
	if err != nil {
		t.Fatal(err)
	}
	if s.curve.profileEditor != nil || s.curve.dirty || !s.curve.running || !s.curve.previewing || !s.curve.paused || !reflect.DeepEqual(before, s.curve.snapshot) {
		t.Fatalf("rename discarded the current preview or stopped acquisition: %+v", s.curve.snapshot)
	}
	if app.Settings.CurveProfiles[0].Name != "renamed firmware" || !app.SettingsDirty || !reflect.DeepEqual(channels, s.curve.draft.channels) {
		t.Fatal("confirmed rename did not update only the common fields")
	}
	revision := app.SettingsRevision
	s.curve.profileEditor = newCurveProfileEdit(s.curve.draft)
	s.confirmCurveProfileEditor()
	if s.curve.profileEditor != nil || app.SettingsRevision != revision || !reflect.DeepEqual(before, s.curve.snapshot) {
		t.Fatal("unchanged confirmation modified the profile or its existing samples")
	}
}

func TestCurveProfileDialogCancellationPreservesActiveConfiguration(t *testing.T) {
	for _, action := range []string{"Cancel", "Escape"} {
		t.Run(action, func(t *testing.T) {
			app := &internal.App{Settings: config.Settings{Language: "en", CurveProfiles: []config.CurveProfile{config.NewCurveProfile("firmware")}}}
			s := newController(app)
			s.curve = newCurveViewState(s.settings)
			s.startCurve()
			s.curve.previewText = "t=12345 VBAT=3712"
			s.previewCurve()
			before := s.curve.snapshot
			profiles := config.CloneCurveProfiles(s.settings.CurveProfiles)
			revision := app.SettingsRevision
			s.curve.profileEditor = newCurveProfileEdit(s.curve.draft)
			tt := gui.NewTester(s.curveProfileEditorView, 400, 360)
			setCurveProfileDialogInput(t, tt, "Profile name", "unconfirmed")
			setCurveProfileDialogInput(t, tt, "Time regex (optional)", `t=(\d+)`)
			setCurveProfileDialogInput(t, tt, "Time scale", "0.01")
			if action == "Escape" {
				tt.Key(0, gui.KeyEscape)
			} else {
				err := tt.Click(action)
				if err != nil {
					t.Fatal(err)
				}
			}
			if s.curve.profileEditor != nil || s.curve.dirty || !s.curve.running || app.SettingsDirty || app.SettingsRevision != revision || !reflect.DeepEqual(profiles, s.settings.CurveProfiles) || !reflect.DeepEqual(before, s.curve.snapshot) {
				t.Fatal("cancelled configuration changed the active profile or displayed samples")
			}
			if s.curve.draft.name != "firmware" || s.curve.draft.timePattern != "" || s.curve.draft.timeScale != "0.001" {
				t.Fatal("cancelled configuration leaked into the profile draft")
			}
			var live internal.CurveSnapshot
			app.CurveSnapshot(&live)
			if live.DeviceTime {
				t.Fatal("cancelled time regex changed the live parser's clock")
			}
		})
	}
}

func TestCurveProfileDialogRejectsInvalidConfigurationWithoutSaving(t *testing.T) {
	for _, test := range []struct {
		name, profileName, pattern, scale string
	}{
		{"empty_name", "", "", "0.001"},
		{"invalid_regex", "changed", "[", "0.001"},
		{"missing_capture", "changed", `t=\d+`, "0.001"},
		{"invalid_scale", "changed", `t=(\d+)`, "bad"},
		{"system_invalid_scale", "changed", "", "bad"},
		{"nonfinite_scale", "changed", `t=(\d+)`, "NaN"},
		{"infinite_scale", "changed", `t=(\d+)`, "+Inf"},
		{"zero_device_scale", "changed", `t=(\d+)`, "0"},
		{"negative_device_scale", "changed", `t=(\d+)`, "-1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := t.TempDir() + "/config.toml"
			s, notified := settingsSaveFixture(t, path)
			s.curve = newCurveViewState(s.settings)
			applied := s.applyCurveDraft()
			if !applied {
				t.Fatalf("could not establish saved configuration: %+v", s.curve.error)
			}
			completeSettingsSave(t, s, notified)
			s.startCurve()
			s.curve.previewText = "VBAT=3712"
			s.previewCurve()
			before := s.curve.snapshot
			profiles := config.CloneCurveProfiles(s.settings.CurveProfiles)
			revision := s.app.SettingsRevision
			s.curve.profileEditor = newCurveProfileEdit(s.curve.draft)
			e := s.curve.profileEditor
			e.name, e.timePattern, e.timeScale = test.profileName, test.pattern, test.scale
			tt := gui.NewTester(s.curveProfileEditorView, 400, 360)
			err := tt.Click("OK")
			if err != nil {
				t.Fatal(err)
			}
			if s.curve.profileEditor != e || e.error.en == "" || s.curve.dirty || !s.curve.running || s.app.SettingsDirty || s.app.SettingsRevision != revision || !reflect.DeepEqual(profiles, s.settings.CurveProfiles) || !reflect.DeepEqual(before, s.curve.snapshot) {
				t.Fatalf("invalid configuration changed active settings or lost its editor error: %+v", e.error)
			}
			if !reflect.DeepEqual(curveDraft(profiles[0]), s.curve.draft) || !reflect.DeepEqual(profiles, readSavedSettings(t, path).CurveProfiles) {
				t.Fatal("invalid configuration polluted the draft or saved file")
			}
		})
	}
}

func TestCurveProfileDialogConfirmationSwitchesClockAndPersists(t *testing.T) {
	path := t.TempDir() + "/config.toml"
	s, notified := settingsSaveFixture(t, path)
	s.curve = newCurveViewState(s.settings)
	applied := s.applyCurveDraft()
	if !applied {
		t.Fatal(s.curve.error)
	}
	completeSettingsSave(t, s, notified)
	s.startCurve()
	s.curve.previewText = "t=12345 VBAT=3712\nt=13500 VBAT=3680"
	s.previewCurve()
	channels := append([]curveChannelDraft(nil), s.curve.draft.channels...)
	s.curve.profileEditor = newCurveProfileEdit(s.curve.draft)
	tt := gui.NewTester(s.curveProfileEditorView, 400, 360)
	setCurveProfileDialogInput(t, tt, "Profile name", "device firmware")
	setCurveProfileDialogInput(t, tt, "Time regex (optional)", `t=(\d+)`)
	setCurveProfileDialogInput(t, tt, "Time scale", "0.001")
	if s.curve.snapshot.DeviceTime || s.settings.CurveProfiles[0].TimePattern != "" || s.app.SettingsDirty {
		t.Fatal("unconfirmed device-time fields changed active configuration")
	}
	err := tt.Click("OK")
	if err != nil {
		t.Fatal(err)
	}
	points := s.curve.snapshot.Series[0].Points
	if s.curve.profileEditor != nil || s.curve.dirty || !s.curve.running || !s.curve.previewing || !s.curve.snapshot.DeviceTime || len(points) != 2 || points[0].X != 12.345 || points[1].X != 13.5 || math.Abs(points[0].Y-3.712) > 1e-12 {
		t.Fatalf("confirmation did not apply device time to the existing preview: %+v", s.curve.snapshot)
	}
	if !reflect.DeepEqual(channels, s.curve.draft.channels) {
		t.Fatal("configuration confirmation changed curve rules")
	}
	var live internal.CurveSnapshot
	s.app.CurveSnapshot(&live)
	if !live.DeviceTime {
		t.Fatal("configuration confirmation did not switch the live parser to device time")
	}
	completeSettingsSave(t, s, notified)
	stored := readSavedSettings(t, path)
	if s.app.SettingsDirty || len(stored.CurveProfiles) != 1 || stored.CurveProfiles[0].Name != "device firmware" || stored.CurveProfiles[0].TimePattern != `t=(\d+)` || stored.CurveProfiles[0].TimeScale != .001 {
		t.Fatalf("confirmed configuration was not persisted: %+v", stored.CurveProfiles)
	}
	s.curve.profileEditor = newCurveProfileEdit(s.curve.draft)
	tt = gui.NewTester(s.curveProfileEditorView, 400, 360)
	setCurveProfileDialogInput(t, tt, "Time regex (optional)", "")
	err = tt.Click("OK")
	if err != nil {
		t.Fatal(err)
	}
	if s.curve.profileEditor != nil || s.curve.snapshot.DeviceTime || !s.curve.running || s.curve.snapshot.Series[0].Points[0].X < 1e9 {
		t.Fatal("clearing the optional time regex did not restore system receive time")
	}
	s.app.CurveSnapshot(&live)
	if live.DeviceTime {
		t.Fatal("clearing the optional regex left the live parser in device-time mode")
	}
	completeSettingsSave(t, s, notified)
	stored = readSavedSettings(t, path)
	if s.app.SettingsDirty || stored.CurveProfiles[0].TimePattern != "" {
		t.Fatal("restored system-time configuration was not persisted")
	}
}

func TestCurveProfileDialogApplyFailureRetainsCorrectedCommonFields(t *testing.T) {
	app := &internal.App{Settings: config.Settings{Language: "en", CurveProfiles: []config.CurveProfile{config.NewCurveProfile("firmware")}}}
	s := newController(app)
	s.curve = newCurveViewState(s.settings)
	s.startCurve()
	s.curve.draft.channels[0].offset = "incomplete"
	s.curve.dirty = true
	s.curve.error = localizedText{zh: "未完成的曲线输入", en: "Incomplete curve input"}
	profiles := config.CloneCurveProfiles(s.settings.CurveProfiles)
	revision := app.SettingsRevision
	s.curve.profileEditor = newCurveProfileEdit(s.curve.draft)
	e := s.curve.profileEditor
	e.name = "renamed firmware"
	s.confirmCurveProfileEditor()
	if s.curve.profileEditor != e || e.error.en == "" || !s.curve.dirty || s.curve.error.en == "" || s.curve.draft.name != "renamed firmware" || s.curve.draft.channels[0].offset != "incomplete" {
		t.Fatal("failed application discarded corrected common fields or unfinished channel input")
	}
	if app.SettingsDirty || app.SettingsRevision != revision || !reflect.DeepEqual(profiles, s.settings.CurveProfiles) || !s.curve.running {
		t.Fatal("failed application changed the saved or active configuration")
	}
}

func TestCurveProfileDialogRetainsTimeScaleAcrossClockModes(t *testing.T) {
	for _, language := range []string{"en", "zh-CN"} {
		t.Run(language, func(t *testing.T) {
			profile := config.NewCurveProfile("firmware")
			profile.TimeScale = 1
			app := &internal.App{Settings: config.Settings{Language: language, CurveProfiles: []config.CurveProfile{profile}}}
			s := newController(app)
			s.curve = newCurveViewState(s.settings)
			s.startCurve()
			s.curve.previewText = "uptime_ms=12345 VBAT=3712\nuptime_ms=13345 VBAT=3680"
			s.previewCurve()
			s.curve.profileEditor = newCurveProfileEdit(s.curve.draft)
			tt := gui.NewTester(s.curveProfileEditorView, 300, 200)
			scaleLabel := s.tr("时间倍率", "Time scale")
			patternLabel := s.tr("时间正则 (可选)", "Time regex (optional)")
			field, found := tt.AccessByRole(gui.RoleTextField, scaleLabel)
			if !found || !field.Disabled || field.Value != "1" {
				t.Fatalf("system-time mode did not retain its inactive scale: %+v", field)
			}
			setCurveProfileDialogInput(t, tt, patternLabel, `uptime_ms=(\d+)`)
			setCurveProfileDialogInput(t, tt, scaleLabel, "0.001")
			setCurveProfileDialogInput(t, tt, patternLabel, "")
			field, _ = tt.AccessByRole(gui.RoleTextField, scaleLabel)
			if !field.Disabled || field.Value != "0.001" || app.Settings.CurveProfiles[0].TimeScale != 1 {
				t.Fatalf("clearing the regex lost the scale or changed unconfirmed settings: %+v", field)
			}
			setCurveProfileDialogInput(t, tt, patternLabel, `uptime_ms=(\d+)`)
			field, _ = tt.AccessByRole(gui.RoleTextField, scaleLabel)
			if field.Disabled || field.Value != "0.001" {
				t.Fatalf("restoring the regex did not restore scale editing: %+v", field)
			}
			err := tt.Click(s.tr("确定", "OK"))
			if err != nil {
				t.Fatal(err)
			}
			points := s.curve.snapshot.Series[0].Points
			if !s.curve.snapshot.DeviceTime || len(points) != 2 || math.Abs(points[0].X-12.345) > 1e-12 || math.Abs(points[1].X-13.345) > 1e-12 {
				t.Fatalf("device uptime was not plotted as numeric seconds: %+v", s.curve.snapshot)
			}
			s.curve.profileEditor = newCurveProfileEdit(s.curve.draft)
			tt = gui.NewTester(s.curveProfileEditorView, 300, 200)
			setCurveProfileDialogInput(t, tt, patternLabel, "")
			err = tt.Click(s.tr("确定", "OK"))
			if err != nil {
				t.Fatal(err)
			}
			if s.curve.snapshot.DeviceTime || s.curve.snapshot.Series[0].Points[0].X < 1e9 || app.Settings.CurveProfiles[0].TimeScale != .001 {
				t.Fatal("system-time confirmation mixed clock domains or discarded the retained scale")
			}
		})
	}
}

func TestCurveProfileTimeHelpStaysBelowInputAndFollowsHover(t *testing.T) {
	for _, language := range []string{"en", "zh-CN"} {
		for _, scale := range []float32{1, 1.25, 1.5} {
			t.Run(fmt.Sprintf("%s_scale_%g", language, scale), func(t *testing.T) {
				s := newController(&internal.App{Settings: config.Settings{Language: language}})
				s.curve = newCurveViewState(s.settings)
				s.curve.profileEditor = newCurveProfileEdit(s.curve.draft)
				tt := gui.NewTester(s.curveProfileEditorView, 300, 200)
				tt.SetScale(scale)
				pattern, found := tt.AccessByRole(gui.RoleTextField, s.tr("时间正则 (可选)", "Time regex (optional)"))
				if !found {
					t.Fatal("time regex input is missing")
				}
				_, tipFound := tt.AccessByRole(gui.RoleTooltip, "")
				if tipFound {
					t.Fatal("time help showed without hovering the input")
				}
				x, y := pattern.Bounds.X+pattern.Bounds.W/2, pattern.Bounds.Y+pattern.Bounds.H/2
				tt.Move(x, y)
				tip, found := tt.AccessByRole(gui.RoleTooltip, "")
				if !found || tip.Bounds.Y < pattern.Bounds.Y+pattern.Bounds.H || tip.Bounds.X < 0 || tip.Bounds.X+tip.Bounds.W > 300 || tip.Bounds.Y+tip.Bounds.H > 200 {
					t.Fatalf("time help obscures the input or exceeds the window: %+v", tip.Bounds)
				}
				tt.ClickAt(x, y)
				tt.Type(`uptime_ms=(\d+)`)
				if s.curve.profileEditor.timePattern != `uptime_ms=(\d+)` {
					t.Fatal("hover help intercepted editing the time regex")
				}
				_, tipFound = tt.AccessByRole(gui.RoleTooltip, "")
				if !tipFound {
					t.Fatal("hover help disappeared while the pointer remained over the input")
				}
				name, _ := tt.AccessByRole(gui.RoleTextField, s.tr("方案名称", "Profile name"))
				tt.Move(name.Bounds.X+name.Bounds.W/2, name.Bounds.Y+name.Bounds.H/2)
				tt.Key(gui.Ctrl, gui.KeyA)
				_, tipFound = tt.AccessByRole(gui.RoleTooltip, "")
				if tipFound {
					t.Fatal("keyboard focus kept the help open after the pointer left")
				}
				multiplier, _ := tt.AccessByRole(gui.RoleTextField, s.tr("时间倍率", "Time scale"))
				tt.Move(multiplier.Bounds.X+multiplier.Bounds.W/2, multiplier.Bounds.Y+multiplier.Bounds.H/2)
				_, tipFound = tt.AccessByRole(gui.RoleTooltip, "")
				if tipFound {
					t.Fatal("time help was repeated over the scale input")
				}
				tt.Move(x, y)
				tip, found = tt.AccessByRole(gui.RoleTooltip, "")
				if !found {
					t.Fatal("time help did not return when hovering again")
				}
				tt.Move(tip.Bounds.X+tip.Bounds.W/2, tip.Bounds.Y+tip.Bounds.H/2)
				_, tipFound = tt.AccessByRole(gui.RoleTooltip, "")
				if tipFound {
					t.Fatal("time help stayed open after leaving the input for the tip")
				}
			})
		}
	}
}

func TestCurveProfileDialogRepairsLegacyTimePatternsIndependently(t *testing.T) {
	path := t.TempDir() + "/config.toml"
	s, notified := settingsSaveFixture(t, path)
	first, second := config.NewCurveProfile("first legacy"), config.NewCurveProfile("second legacy")
	first.TimePattern, second.TimePattern = "[", "["
	s.settings.CurveProfiles = []config.CurveProfile{first, second}
	s.curve = newCurveViewState(s.settings)
	for index := range 2 {
		if index > 0 {
			view := gui.NewTester(s.serialCurveView, 1040, 600)
			err := view.Click("Firmware profile")
			if err != nil {
				t.Fatal(err)
			}
			err = view.Click("2. second legacy")
			if err != nil {
				t.Fatal(err)
			}
			completeSettingsSave(t, s, notified)
		}
		s.curve.profileEditor = newCurveProfileEdit(s.curve.draft)
		s.curve.profileEditor.timePattern = `T=(\d+)`
		s.confirmCurveProfileEditor()
		if s.curve.profileEditor != nil || s.curve.dirty || s.curve.error.en != "" || s.settings.CurveProfiles[index].TimePattern != `T=(\d+)` {
			t.Fatal("another untouched invalid regex blocked the current correction")
		}
		completeSettingsSave(t, s, notified)
		stored := readSavedSettings(t, path)
		if len(stored.CurveProfiles) != 2 || !reflect.DeepEqual(stored.CurveProfiles, s.settings.CurveProfiles) {
			t.Fatal("repair did not persist both profiles in their original order")
		}
		if index == 0 && stored.CurveProfiles[1].TimePattern != "[" {
			t.Fatal("repair silently replaced the other invalid legacy rule")
		}
	}
	s.startCurve()
	if !s.curve.running || !s.app.CurveRunning() || s.curve.error.en != "" {
		t.Fatal("repaired profile did not start real acquisition")
	}
}
