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
	if !field.Focused {
		tt.ClickAt(field.Bounds.X+field.Bounds.W/2, field.Bounds.Y+field.Bounds.H/2)
	}
	tt.Key(gui.Ctrl, gui.KeyA)
	tt.Key(0, gui.KeyBackspace)
	tt.Type(value)
	field, found = tt.AccessByRole(gui.RoleTextField, label)
	if !found || field.Value != value {
		t.Fatalf("configuration field %q contains %q, want %q", label, field.Value, value)
	}
}

func typeCurvePointLimitInput(t *testing.T, tt *gui.Tester, value string) string {
	t.Helper()
	setCurveProfileDialogInput(t, tt, "Samples per curve", "")
	tt.Type(value)
	field, found := tt.AccessByRole(gui.RoleTextField, "Samples per curve")
	if !found {
		t.Fatal("curve sample limit input is unavailable")
	}
	return field.Value
}

func TestCurveProfileDialogFitsSmallScaledWindows(t *testing.T) {
	const height = 260 // Leave room for native window decorations at the minimum size.
	for _, language := range []string{"en", "zh-CN"} {
		for _, scale := range []float32{1, 1.25, 1.5} {
			for _, width := range []int{280, 300} {
				t.Run(fmt.Sprintf("%s_scale_%g_width_%d", language, scale, width), func(t *testing.T) {
					s := newController(&internal.App{Settings: config.Settings{Language: language}})
					s.curve = newCurveViewState(s.settings)
					s.curve.profileEditor = newCurveProfileEdit(s.curve.draft)
					tt := gui.NewTester(s.curveProfileEditorView, width, height)
					tt.SetScale(scale)
					for _, label := range []string{s.tr("方案名称", "Profile name"), s.tr("时间正则 (可选)", "Time regex (optional)"), s.tr("时间倍率", "Time scale"), s.tr("每条曲线采样上限", "Samples per curve")} {
						field, found := tt.AccessByRole(gui.RoleTextField, label)
						if !found || field.Offscreen || field.Bounds.W <= 0 || field.Bounds.H <= 0 || field.Bounds.X < 0 || field.Bounds.Y < 0 || field.Bounds.X+field.Bounds.W > float32(width) || field.Bounds.Y+field.Bounds.H > height {
							t.Fatalf("configuration field %q exceeds the window: %+v", label, field.Bounds)
						}
					}
					for _, withError := range []bool{false, true} {
						if withError {
							s.curve.profileEditor.error = localizedText{zh: strings.Repeat("无效的时间正则. ", 40), en: strings.Repeat("Invalid time regex. ", 40)}
							tt.Frame()
							errorBounds, found := tt.Find(s.tr(s.curve.profileEditor.error.zh, s.curve.profileEditor.error.en))
							if !found || errorBounds.H <= 0 || errorBounds.Y < 0 || errorBounds.Y+errorBounds.H > height {
								t.Fatalf("configuration error is hidden in the compact window: %+v", errorBounds)
							}
						}
						for _, label := range []string{s.tr("取消", "Cancel"), s.tr("确定", "OK")} {
							button, found := tt.AccessByRole(gui.RoleButton, label)
							if !found || button.Offscreen || button.Bounds.W <= 0 || button.Bounds.H <= 0 || button.Bounds.X < 0 || button.Bounds.Y < 0 || button.Bounds.X+button.Bounds.W > float32(width) || button.Bounds.Y+button.Bounds.H > height {
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

func TestCurveProfilePointLimitPasteRejectsOverflow(t *testing.T) {
	s := newController(&internal.App{Settings: config.Settings{Language: "en"}})
	s.curve = newCurveViewState(s.settings)
	s.curve.profileEditor = newCurveProfileEdit(s.curve.draft)
	tt := gui.NewTester(s.curveProfileEditorView, 280, 360)
	setCurveProfileDialogInput(t, tt, "Samples per curve", "42")
	for _, value := range []string{"000000001", "123456789", "100000000"} {
		tt.Key(gui.Ctrl, gui.KeyA)
		tt.SetClipboard(value)
		tt.Key(gui.Ctrl, gui.KeyV)
		field, found := tt.AccessByRole(gui.RoleTextField, "Samples per curve")
		if !found || field.Value != "42" {
			t.Fatalf("pasting %q changed the sample limit to %q", value, field.Value)
		}
	}
	// A rejected paste retains the selection for a subsequent valid replacement.
	tt.SetClipboard("12x3456")
	tt.Key(gui.Ctrl, gui.KeyV)
	field, found := tt.AccessByRole(gui.RoleTextField, "Samples per curve")
	if !found || field.Value != "123456" {
		t.Fatalf("filtered replacement = %q, want 123456", field.Value)
	}
	// Even a short paste must not be truncated when appended to existing text.
	tt.Key(0, gui.KeyEnd)
	tt.SetClipboard("789")
	tt.Key(gui.Ctrl, gui.KeyV)
	field, found = tt.AccessByRole(gui.RoleTextField, "Samples per curve")
	if !found || field.Value != "123456" {
		t.Fatalf("overflowing insertion changed sample limit to %q", field.Value)
	}
	tt.SetClipboard("78")
	tt.Key(gui.Ctrl, gui.KeyV)
	field, found = tt.AccessByRole(gui.RoleTextField, "Samples per curve")
	ok, okFound := tt.AccessByRole(gui.RoleButton, "OK")
	if !found || field.Value != "12345678" || !okFound || !ok.Disabled {
		t.Fatalf("valid-length out-of-range insertion = %q, OK disabled = %v", field.Value, ok.Disabled)
	}
}

func TestCurveProfileDialogChangesPreviewPointLimitAndPersists(t *testing.T) {
	path := t.TempDir() + "/config.toml"
	s, notified := settingsSaveFixture(t, path)
	s.settings.CurveProfiles = []config.CurveProfile{config.NewCurveProfile("firmware")}
	s.curve = newCurveViewState(s.settings)
	s.startCurve()
	s.curve.previewText = "VBAT=1000\nVBAT=2000\nVBAT=3000"
	s.previewCurve()
	s.curve.profileEditor = newCurveProfileEdit(s.curve.draft)
	// Keep the input fully visible with validation messages and platform font metrics.
	const dialogHeight = 360
	tt := gui.NewTester(s.curveProfileEditorView, 280, dialogHeight)
	field, found := tt.AccessByRole(gui.RoleTextField, "Samples per curve")
	if !found || field.Offscreen || field.Value != "10000" {
		t.Fatalf("new profile did not show the editable default: %+v", field)
	}
	setCurveProfileDialogInput(t, tt, "Samples per curve", "")
	message, visible := tt.Find("Enter an integer in the range shown.")
	ok, found := tt.AccessByRole(gui.RoleButton, "OK")
	if !visible || message.H <= 0 || !found || !ok.Disabled || s.app.SettingsDirty || s.settings.CurveProfiles[0].PointLimit != config.DefaultCurvePointLimit {
		t.Fatal("an empty sample limit should still block confirmation")
	}
	for _, tc := range []struct {
		input, want string
	}{
		{"12x3", "123"},
		{"-1", "1"},
		{"0.5", "05"},
	} {
		got := typeCurvePointLimitInput(t, tt, tc.input)
		if got != tc.want {
			t.Fatalf("sample limit %q retained %q, want filtered value %q", tc.input, got, tc.want)
		}
		_, visible = tt.Find("Enter an integer in the range shown.")
		ok, found = tt.AccessByRole(gui.RoleButton, "OK")
		if visible || !found || ok.Disabled || s.app.SettingsDirty || s.settings.CurveProfiles[0].PointLimit != config.DefaultCurvePointLimit {
			t.Fatalf("filtered sample limit %q did not remain a valid unconfirmed edit", tc.input)
		}
	}
	before := s.curve.snapshot
	for _, value := range []string{"1000001", "99999999"} {
		setCurveProfileDialogInput(t, tt, "Samples per curve", value)
		message, visible = tt.Find("Enter an integer in the range shown.")
		ok, found = tt.AccessByRole(gui.RoleButton, "OK")
		if !visible || message.H <= 0 || !found || !ok.Disabled {
			t.Fatalf("out-of-range sample limit %q did not block confirmation", value)
		}
		s.confirmCurveProfileEditor()
		if s.curve.profileEditor == nil || s.app.SettingsDirty || s.settings.CurveProfiles[0].PointLimit != config.DefaultCurvePointLimit || !reflect.DeepEqual(before, s.curve.snapshot) {
			t.Fatalf("out-of-range sample limit %q changed settings or retained samples", value)
		}
	}
	if got := typeCurvePointLimitInput(t, tt, strings.Repeat("9", 20)); got != "99999999" {
		t.Fatalf("long sample limit retained %q, want eight digits", got)
	}
	ok, found = tt.AccessByRole(gui.RoleButton, "OK")
	if !found || !ok.Disabled {
		t.Fatal("truncated out-of-range limit should block confirmation")
	}
	setCurveProfileDialogInput(t, tt, "Samples per curve", "1000000")
	ok, found = tt.AccessByRole(gui.RoleButton, "OK")
	if !found || ok.Disabled {
		t.Fatal("maximum sample count should enable confirmation")
	}
	_, visible = tt.Find("Enter an integer in the range shown.")
	if visible {
		t.Fatal("correcting the sample count did not clear the error")
	}
	setCurveProfileDialogInput(t, tt, "Samples per curve", "2")
	err := tt.Click("OK")
	if err != nil {
		t.Fatal(err)
	}
	points := s.curve.snapshot.Series[0].Points
	if s.curve.profileEditor != nil || !s.curve.running || !s.curve.previewing || len(points) != 2 || points[0].Y != 2 || points[1].Y != 3 {
		t.Fatalf("new limit did not keep the latest preview points: %+v", s.curve.snapshot)
	}
	completeSettingsSave(t, s, notified)
	stored := readSavedSettings(t, path)
	if len(stored.CurveProfiles) != 1 || stored.CurveProfiles[0].PointLimit != 2 {
		t.Fatalf("sample count was not saved in the profile: %+v", stored.CurveProfiles)
	}
	s.curve.profileEditor = newCurveProfileEdit(s.curve.draft)
	tt = gui.NewTester(s.curveProfileEditorView, 280, dialogHeight)
	setCurveProfileDialogInput(t, tt, "Samples per curve", "0")
	err = tt.Click("OK")
	if err != nil {
		t.Fatal(err)
	}
	points = s.curve.snapshot.Series[0].Points
	if s.curve.profileEditor != nil || !s.curve.running || !s.curve.previewing || len(points) != 3 || points[0].Y != 1 || points[2].Y != 3 {
		t.Fatalf("unlimited preview did not show all samples: %+v", s.curve.snapshot)
	}
	completeSettingsSave(t, s, notified)
	stored = readSavedSettings(t, path)
	if len(stored.CurveProfiles) != 1 || stored.CurveProfiles[0].PointLimit != 0 {
		t.Fatalf("unlimited setting was not saved: %+v", stored.CurveProfiles)
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
