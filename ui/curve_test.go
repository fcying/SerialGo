package ui

import (
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo"
	gui "github.com/egoist/mygo/ui"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"
)

func TestCurveDraftDoesNotMutateStoredFirmwareRules(t *testing.T) {
	settings := config.Settings{CurveProfiles: []config.CurveProfile{config.NewCurveProfile("firmware")}}
	v := newCurveViewState(&settings)
	v.draft.channels[0].pattern = "VCORE="
	v.draft.channels[0].scale = "0.01"
	profile, err := v.profile()
	if err != nil {
		t.Fatal(err)
	}
	if settings.CurveProfiles[0].Channels[0].Pattern != "VBAT=" || settings.CurveProfiles[0].Channels[0].Scale != .001 {
		t.Fatal("editing a draft changed saved extraction rules")
	}
	snapshot, err := internal.PreviewCurve(profile, "VCORE=120\nVBAT=9999\nVCORE=125")
	if err != nil {
		t.Fatal(err)
	}
	points := snapshot.Series[0].Points
	if len(points) != 2 || math.Abs(points[0].Y-1.2) > 1e-12 || points[1].Y != 1.25 {
		t.Fatalf("preview did not use the draft's extraction and conversion: %+v", points)
	}
	v.draft.channels[0].offset = "bad"
	if _, err := v.profile(); err == nil {
		t.Fatal("invalid draft offset accepted")
	}
}

func TestCurveHistoryWindowAutoscale(t *testing.T) {
	v := newCurveViewState(&config.Settings{})
	v.span = "10 s"
	v.snapshot.LatestX = 100
	v.snapshot.Series = []internal.CurveSeries{
		{Name: "voltage", Points: []internal.CurvePoint{{X: 0, Y: 1}, {X: 5, Y: 2}, {X: 95, Y: 3}, {X: 100, Y: 4}}},
		{Name: "current", Points: []internal.CurvePoint{{X: 0, Y: .5}, {X: 5, Y: 1}, {X: 95, Y: 6}, {X: 100, Y: 7}}},
	}
	bounds, ok := v.bounds()
	if !ok || bounds.x0 != 90 || bounds.x1 != 100 || bounds.y0 >= 3 || bounds.y1 <= 7 || bounds.y0 < 2 || bounds.y1 > 8 {
		t.Fatalf("newest window did not scale all channels: %+v", bounds)
	}
	v.position = 0
	bounds, ok = v.bounds()
	if !ok || bounds.x0 != 0 || bounds.x1 != 10 || bounds.y0 >= .5 || bounds.y1 <= 2 || bounds.y0 < 0 || bounds.y1 > 3 {
		t.Fatalf("historical position did not show earliest retained samples: %+v", bounds)
	}
	v.snapshot.Series = nil
	if _, ok := v.bounds(); ok {
		t.Fatal("empty snapshot still contributed to the viewport")
	}
}

func TestCurvePreviewRemainsUsableInSmallScaledWindows(t *testing.T) {
	for _, language := range []string{"en", "zh-CN"} {
		for _, scale := range []float32{1, 1.25, 1.5} {
			for _, channels := range []int{1, 8, 12} {
				t.Run(fmt.Sprintf("%s_scale_%g_channels_%d", language, scale, channels), func(t *testing.T) {
					profile := config.NewCurveProfile("firmware")
					for len(profile.Channels) < channels {
						profile.Channels = append(profile.Channels, profile.Channels[0])
					}
					app := &internal.App{Settings: config.Settings{Language: language, CurveProfiles: []config.CurveProfile{profile}}}
					s := newController(app)
					s.curve = newCurveViewState(&app.Settings)
					s.curve.previewText = "[ADC] VBAT=3712 mV\n[ADC] VBAT=3680 mV\n[ADC] VBAT=3740 mV\n"
					profile, err := s.curve.profile()
					if err != nil {
						t.Fatal(err)
					}
					s.curve.snapshot, err = internal.PreviewCurve(profile, s.curve.previewText)
					if err != nil {
						t.Fatal(err)
					}
					s.curve.running = true
					tt := gui.NewTester(s.serialCurveView, 800, 600)
					tt.SetScale(scale)
					button, found := tt.AccessByRole(gui.RoleButton, s.tr("测试规则", "Test rules"))
					if !found || button.Bounds.W <= 0 || button.Bounds.H <= 0 || button.Bounds.X < 0 || button.Bounds.Y < 0 || button.Bounds.X+button.Bounds.W > 800 || button.Bounds.Y+button.Bounds.H > 600 {
						t.Fatalf("rule test action is outside the visible window: %+v", button.Bounds)
					}
					for _, label := range []string{s.tr("添加曲线", "Add curve"), s.tr("编辑曲线 VBAT", "Edit curve VBAT")} {
						field, visible := tt.Find(label)
						if !visible || field.X < 8 || field.X+field.W > 268 || field.Y < 0 || field.Y+field.H > 600 {
							t.Fatalf("rule control %q exceeds the narrow sidebar: %+v", label, field)
						}
					}
					chart, chartFound := tt.Find(s.tr("串口曲线图", "Serial curve chart"))
					list, listFound := tt.Find(s.tr("曲线列表", "Curves"))
					if !chartFound || !listFound || chart.Y > list.Y {
						t.Fatalf("channel count pushed the chart below the curve list: chart=%+v list=%+v", chart, list)
					}
					for _, label := range []string{s.tr("方案名称", "Profile name"), s.tr("时间正则 (可选)", "Time regex (optional)"), s.tr("时间倍率", "Time scale")} {
						if _, found := tt.AccessByRole(gui.RoleTextField, label); found {
							t.Fatalf("common profile setting %q remains in the curve list", label)
						}
					}
					if _, found := tt.Find(s.tr("预览日志", "Preview log")); found {
						t.Fatal("log preview must not remain expanded in the main plot")
					}
					testWindow := gui.NewTester(s.curveRuleTestView, 400, 400)
					testWindow.SetScale(scale)
					err = testWindow.Click(s.tr("预览提取", "Preview extraction"))
					if err != nil {
						t.Fatal(err)
					}
					if !s.curve.previewing || !s.curve.paused || s.curve.snapshot.Matched != 3 {
						t.Fatalf("preview did not freeze the displayed log samples: %+v", s.curve.snapshot)
					}
					points := s.curve.snapshot.Series[0].Points
					if len(points) != 3 || math.Abs(points[0].Y-3.712) > 1e-12 || math.Abs(points[2].Y-3.740) > 1e-12 {
						t.Fatalf("preview displayed incorrect voltage samples: %+v", points)
					}
					button, found = testWindow.AccessByRole(gui.RoleButton, s.tr("预览提取", "Preview extraction"))
					if !found || button.Bounds.Y < 0 || button.Bounds.Y+button.Bounds.H > 400 {
						t.Fatalf("preview results displaced the test action: %+v", button.Bounds)
					}
				})
			}
		}
	}
}

func TestCurveDefaultPreviewSeparatesVoltageAndCurrent(t *testing.T) {
	s := newController(&internal.App{Settings: config.Settings{Language: "en"}})
	s.curve = newCurveViewState(s.settings)
	s.curve.draft.channels = []curveChannelDraft{
		{name: "Voltage", mode: "prefix", pattern: "VBAT=", scale: "0.001", offset: "0", unit: "V", enabled: true},
		{name: "Current", mode: "prefix", pattern: "IBAT=", scale: "0.001", offset: "0", unit: "mA", enabled: true},
	}
	tt := gui.NewTester(s.curveRuleTestView, 480, 440)
	err := tt.Click("Preview extraction")
	if err != nil {
		t.Fatal(err)
	}
	for i, expected := range [][]float64{{3.712, 3.680, 3.740}, {1.024, .926, 1.524}} {
		points := s.curve.snapshot.Series[i].Points
		if len(points) != len(expected) {
			t.Fatalf("series %d mixed voltage and current records: %+v", i, points)
		}
		for j, value := range expected {
			if math.Abs(points[j].Y-value) > 1e-12 {
				t.Fatalf("series %d sample %d: got %g, want %g", i, j, points[j].Y, value)
			}
		}
	}
}

func TestCurveRuleDialogConfirmationAndCancellation(t *testing.T) {
	for _, index := range []int{-1, 0} {
		for _, action := range []string{"OK", "Cancel", "Escape"} {
			t.Run(fmt.Sprintf("index_%d_%s", index, action), func(t *testing.T) {
				app := &internal.App{Settings: config.Settings{Language: "en", CurveProfiles: []config.CurveProfile{config.NewCurveProfile("firmware")}}}
				s := newController(app)
				s.curve = newCurveViewState(&app.Settings)
				s.curve.ruleEditor = newCurveRuleEdit(s.curve.draft, index)
				e := s.curve.ruleEditor
				tt := gui.NewTester(s.curveRuleEditorView, 420, 440)
				for _, field := range []struct{ label, value string }{{"Curve name", "TEMP"}, {"Numeric prefix", "TEMP="}, {"Scale", "0.1"}, {"Unit", "C"}} {
					input, found := tt.AccessByRole(gui.RoleTextField, field.label)
					if !found {
						t.Fatalf("missing editor field %q", field.label)
					}
					tt.ClickAt(input.Bounds.X+input.Bounds.W/2, input.Bounds.Y+input.Bounds.H/2)
					tt.Key(gui.Ctrl, gui.KeyA)
					tt.Type(field.value)
				}
				if s.curve.draft.channels[0].name != "VBAT" || s.curve.dirty || app.SettingsDirty {
					t.Fatal("unconfirmed window edits leaked into the profile")
				}
				if action == "Escape" {
					tt.Key(0, gui.KeyEscape)
				} else {
					err := tt.Click(action)
					if err != nil {
						t.Fatal(err)
					}
				}
				if s.curve.ruleEditor != nil {
					t.Fatalf("editor remained open: %+v", e.error)
				}
				if action == "OK" {
					want := 1
					if index < 0 {
						want = 2
					}
					channels := s.curve.draft.channels
					if len(channels) != want || channels[want-1].name != "TEMP" || s.curve.dirty {
						t.Fatalf("confirmed rule was not committed: %+v", channels)
					}
					stored := app.Settings.CurveProfiles[0].Channels
					if len(stored) != want || stored[want-1].Name != "TEMP" || !app.SettingsDirty {
						t.Fatal("confirmed rule did not immediately update settings")
					}
					s.curve.previewText = "TEMP=215"
					s.previewCurve()
					points := s.curve.snapshot.Series[want-1].Points
					if len(points) != 1 || points[0].Y != 21.5 {
						t.Fatalf("confirmed conversion did not affect parsing: %+v", points)
					}
				} else if len(s.curve.draft.channels) != 1 || s.curve.draft.channels[0].name != "VBAT" || s.curve.dirty {
					t.Fatal("cancelled window changed the rule list")
				}
				if action != "OK" && (app.Settings.CurveProfiles[0].Channels[0].Name != "VBAT" || app.SettingsDirty) {
					t.Fatal("cancelled rule editor changed stored settings")
				}
			})
		}
	}
}

func TestCurveRuleDialogInvalidAndUnchangedEdits(t *testing.T) {
	app := &internal.App{Settings: config.Settings{Language: "en"}}
	s := newController(app)
	s.curve = newCurveViewState(&app.Settings)
	s.curve.ruleEditor = newCurveRuleEdit(s.curve.draft, 0)
	e := s.curve.ruleEditor
	e.channel.mode, e.channel.pattern = "regex", "["
	tt := gui.NewTester(s.curveRuleEditorView, 420, 440)
	err := tt.Click("OK")
	if err != nil {
		t.Fatal(err)
	}
	if s.curve.ruleEditor != e || e.error.en == "" || s.curve.dirty || s.curve.draft.channels[0].mode != "prefix" {
		t.Fatal("invalid regex was committed or its error was lost")
	}
	s.closeCurveRuleEditor()
	s.curve.ruleEditor = newCurveRuleEdit(s.curve.draft, 0)
	s.confirmCurveRuleEditor()
	if s.curve.ruleEditor != nil || s.curve.dirty {
		t.Fatal("unchanged confirmation dirtied the profile")
	}
}

func TestCurveAutomaticChangesPersistWithoutManualApply(t *testing.T) {
	path := t.TempDir() + "/config.toml"
	s, notified := settingsSaveFixture(t, path)
	s.curve = newCurveViewState(s.settings)
	s.curve.draft.name = "persisted firmware"
	s.curveChanged()
	completeSettingsSave(t, s, notified)
	stored := readSavedSettings(t, path)
	if s.app.SettingsDirty || len(stored.CurveProfiles) != 1 || stored.CurveProfiles[0].Name != "persisted firmware" {
		t.Fatal("valid inline edit was not persisted automatically")
	}
	s.curve.ruleEditor = newCurveRuleEdit(s.curve.draft, -1)
	s.curve.ruleEditor.channel = curveChannelDraft{name: "TEMP", mode: "prefix", pattern: "TEMP=", scale: "0.1", offset: "0", unit: "C", color: config.DefaultCurveColor(1), enabled: true}
	s.confirmCurveRuleEditor()
	completeSettingsSave(t, s, notified)
	stored = readSavedSettings(t, path)
	if len(stored.CurveProfiles[0].Channels) != 2 || stored.CurveProfiles[0].Channels[1].Pattern != "TEMP=" || s.app.SettingsDirty {
		t.Fatal("confirmed channel was not persisted automatically")
	}
	revision := s.app.SettingsRevision
	s.curve.draft.name = ""
	s.curveChanged()
	if !s.curve.dirty || s.app.SettingsRevision != revision || s.app.SettingsDirty || readSavedSettings(t, path).CurveProfiles[0].Name != "persisted firmware" {
		t.Fatal("invalid input overwrote the last saved rule")
	}
}

func TestCurveRulesDividerKeepsControlsVisibleAtMinimumWidth(t *testing.T) {
	for _, scale := range []float32{1, 1.25, 1.5} {
		t.Run(fmt.Sprintf("scale_%g", scale), func(t *testing.T) {
			app := &internal.App{Settings: config.Settings{Language: "en"}}
			s := newController(app)
			s.curve = newCurveViewState(&app.Settings)
			tt := gui.NewTester(s.serialCurveView, 800, 600)
			tt.SetScale(scale)
			divider, found := tt.AccessByRole(gui.RoleSplitter, "")
			if !found {
				t.Fatal("curve list divider is missing")
			}
			listX := divider.Bounds.X - s.curve.rulesWidth
			dragTo := func(width float32) {
				t.Helper()
				divider, found := tt.AccessByRole(gui.RoleSplitter, "")
				if !found {
					t.Fatal("divider disappeared while narrowing the list")
				}
				x, y := divider.Bounds.X+divider.Bounds.W/2, divider.Bounds.Y+divider.Bounds.H/2
				target := listX + width + divider.Bounds.W/2
				tt.Press(x, y)
				tt.Move(target, y)
				if width == 0 {
					plot, found := tt.Find("Serial curve chart")
					if !found || math.Abs(float64(plot.X-listX-curveRulesContentMinWidth-curveDividerWidth)) > 1 {
						t.Fatalf("held divider did not stop at the sidebar minimum: %+v", plot)
					}
				}
				tt.Release(target, y)
			}
			dragTo(360)
			plot, found := tt.Find("Serial curve chart")
			if !found || math.Abs(float64(plot.X-listX-360-curveDividerWidth)) > 1 {
				t.Fatalf("wide list did not move the chart: %+v", plot)
			}
			_, wideLabel := tt.Find("Time range")
			if !wideLabel {
				t.Fatal("wide chart did not show the time-range caption")
			}
			dragTo(800)
			plot, _ = tt.Find("Serial curve chart")
			if math.Abs(float64(plot.W-curveChartMinWidth)) > 1 || plot.X+plot.W > 800 {
				t.Fatalf("wide list clipped the chart: %+v", plot)
			}
			slider, found := tt.Find("History position")
			if !found || slider.W < 48 || slider.X < plot.X || slider.X+slider.W > plot.X+plot.W+1 {
				t.Fatalf("history slider unavailable at the plot minimum: %+v", slider)
			}
			_, narrowLabel := tt.Find("Time range")
			picker, found := tt.Find("Plot time range")
			if narrowLabel || !found || picker.W <= 0 || picker.X < plot.X || picker.X+picker.W > slider.X {
				t.Fatalf("time-range selector left no room for the slider: %+v", picker)
			}
			tt.ClickAt(slider.X+slider.W/2, slider.Y+slider.H/2)
			if s.curve.position <= 0 || s.curve.position >= 100 {
				t.Fatal("history slider did not respond at the plot minimum")
			}
			// Capture the control layout at the minimum, then drag well past it.
			dragTo(curveRulesContentMinWidth)
			labels := []string{"Add curve", "Enable curve VBAT", "Edit curve VBAT", "Delete curve VBAT", "VBAT= · V"}
			minimumBounds := make([]gui.Rect, len(labels))
			for i, label := range labels {
				bounds, found := tt.Find(label)
				if !found || bounds.W <= 0 || bounds.X+bounds.W > listX+curveRulesContentMinWidth+1 {
					t.Fatalf("control %q unavailable at the minimum: %+v", label, bounds)
				}
				minimumBounds[i] = bounds
			}
			dragTo(300)
			dragTo(0)
			plot, _ = tt.Find("Serial curve chart")
			if !s.curve.rulesVisible || s.curve.rulesWidth != curveRulesContentMinWidth || math.Abs(float64(plot.X-listX-curveRulesContentMinWidth-curveDividerWidth)) > 1 {
				t.Fatalf("dragging left did not preserve the visible minimum: width=%g plot=%+v", s.curve.rulesWidth, plot)
			}
			for i, label := range labels {
				if bounds, found := tt.Find(label); !found || bounds != minimumBounds[i] {
					t.Fatalf("dragging left clipped %q: before=%+v after=%+v", label, minimumBounds[i], bounds)
				}
			}
			divider, _ = tt.AccessByRole(gui.RoleSplitter, "")
			tt.ClickAt(divider.Bounds.X+divider.Bounds.W/2, divider.Bounds.Y+divider.Bounds.H/2)
			revision := app.SettingsRevision
			tt.Key(0, gui.KeyLeft)
			if !s.curve.rulesVisible || s.curve.rulesWidth != curveRulesContentMinWidth || app.SettingsRevision != revision {
				t.Fatal("left arrow moved the divider or saved a change at the minimum")
			}
			tt.Key(0, gui.KeyRight)
			if s.curve.rulesWidth != curveRulesContentMinWidth+10 {
				t.Fatal("right arrow could not widen the sidebar from the minimum")
			}
			tt.Key(0, gui.KeyLeft)
			if s.curve.rulesWidth != curveRulesContentMinWidth {
				t.Fatal("left arrow could not restore the minimum width")
			}
			revision = app.SettingsRevision
			err := tt.Click("Hide sidebar")
			if err != nil {
				t.Fatal(err)
			}
			plot, _ = tt.Find("Serial curve chart")
			if s.curve.rulesVisible || s.curve.rulesWidth != curveRulesContentMinWidth || plot.X > listX+curveDividerWidth+1 {
				t.Fatal("hide action lost the sidebar width or did not release viewport space")
			}
			err = tt.Click("Show sidebar")
			if err != nil {
				t.Fatal(err)
			}
			if !s.curve.rulesVisible || s.curve.rulesWidth != curveRulesContentMinWidth || app.SettingsRevision != revision {
				t.Fatal("show action changed the remembered width or persisted visibility")
			}
			if !app.SettingsDirty || app.Settings.CurveSidebarWidth != curveRulesContentMinWidth {
				t.Fatal("resizing did not record the preferred sidebar width")
			}
		})
	}
}

func TestCurveDeleteIconConfirmsAndPreservesLastChannel(t *testing.T) {
	for _, language := range []string{"en", "zh-CN"} {
		t.Run(language, func(t *testing.T) {
			profile := config.NewCurveProfile("firmware")
			profile.Channels = append(profile.Channels, config.CurveChannel{Name: "TEMP", Mode: "prefix", Pattern: "TEMP=", Scale: 1, Enabled: true})
			app := &internal.App{Settings: config.Settings{Language: language, CurveProfiles: []config.CurveProfile{profile}}}
			s := newController(app)
			s.curve = newCurveViewState(&app.Settings)
			tt := gui.NewTester(s.serialCurveView, 800, 600)
			label := s.tr("删除曲线 TEMP", "Delete curve TEMP")
			for _, action := range []string{s.tr("取消", "Cancel"), s.tr("确定", "OK")} {
				err := tt.Click(label)
				if err != nil {
					t.Fatal(err)
				}
				if len(s.curve.draft.channels) != 2 {
					t.Fatal("delete icon removed the curve without confirmation")
				}
				err = tt.Click(action)
				if err != nil {
					t.Fatal(err)
				}
				if action == s.tr("取消", "Cancel") && (len(app.Settings.CurveProfiles[0].Channels) != 2 || app.SettingsDirty) {
					t.Fatal("cancelled deletion changed saved rules")
				}
			}
			if len(app.Settings.CurveProfiles[0].Channels) != 1 || app.Settings.CurveProfiles[0].Channels[0].Name != "VBAT" {
				t.Fatal("confirmed icon action removed the wrong channel")
			}
			last, found := tt.AccessByRole(gui.RoleButton, s.tr("删除曲线 VBAT", "Delete curve VBAT"))
			if !found || !last.Disabled {
				t.Fatal("last remaining curve must not be deletable")
			}
		})
	}
}

func TestCurveSystemClockKeepsMilliseconds(t *testing.T) {
	for _, millis := range []int64{1700000000123, 1700000000999, 1700000001000, -1} {
		seconds := float64(millis) / 1000
		got := curvePointTime(seconds)
		if got.UnixMilli() != millis || got.Nanosecond()%int(time.Millisecond) != 0 {
			t.Fatalf("clock display lost millisecond precision: %d -> %s", millis, got)
		}
		if got.Location() != time.Local {
			t.Fatal("clock display did not use the system time zone")
		}
	}
}

func TestCurveConfigurationTimeModesAndInvalidDraft(t *testing.T) {
	app := &internal.App{Settings: config.Settings{Language: "en", CurveProfiles: []config.CurveProfile{config.NewCurveProfile("firmware")}}}
	s := newController(app)
	s.curve = newCurveViewState(&app.Settings)
	s.startCurve()
	s.curve.draft.timePattern = `t=(\d+)`
	s.curve.draft.timeScale = "0.001"
	s.curveChanged()
	if s.curve.dirty || !s.curve.running || !s.curve.snapshot.DeviceTime {
		t.Fatal("configuration did not apply device time while acquisition continued")
	}
	s.curve.previewText = "t=12345 VBAT=3712"
	s.previewCurve()
	points := s.curve.snapshot.Series[0].Points
	if len(points) != 1 || points[0].X != 12.345 || !s.curve.snapshot.DeviceTime {
		t.Fatalf("configured device conversion was not used: %+v", points)
	}
	revision := app.SettingsRevision
	s.curve.draft.timePattern = "["
	s.curveChanged()
	if !s.curve.dirty || s.curve.error.en == "" || app.SettingsRevision != revision || !s.curve.snapshot.DeviceTime || s.curve.snapshot.Series[0].Points[0].X != 12.345 {
		t.Fatal("incomplete configuration changed the active timestamp mode or preview")
	}
	s.curve.draft.timePattern = ""
	s.curveChanged()
	if s.curve.dirty || s.curve.error.en != "" || !s.curve.running || s.curve.snapshot.DeviceTime || app.Settings.CurveProfiles[0].TimePattern != "" {
		t.Fatal("clearing the optional rule did not restore system-time plotting")
	}
}

func curveSnapshotsEqual(got, want internal.CurveSnapshot) bool {
	return got.Lines == want.Lines && got.Matched == want.Matched &&
		got.Invalid == want.Invalid && got.Oversize == want.Oversize &&
		got.Revision == want.Revision && got.LatestX == want.LatestX &&
		got.DeviceTime == want.DeviceTime &&
		slices.EqualFunc(got.Series, want.Series, func(a, b internal.CurveSeries) bool {
			return a.Name == b.Name && a.Unit == b.Unit && a.Color == b.Color && slices.Equal(a.Points, b.Points)
		})
}

func TestCurveCloseDiscardsPlotAcrossReopen(t *testing.T) {
	for _, mode := range []string{"follow", "paused", "preview"} {
		t.Run(mode, func(t *testing.T) {
			profile := config.NewCurveProfile("firmware")
			profile.TimePattern, profile.TimeScale = `t=(\d+)`, 1
			app := &internal.App{Settings: config.Settings{Language: "en", CurveProfiles: []config.CurveProfile{profile}}}
			err := app.StartCurve(0)
			if err != nil {
				t.Fatal(err)
			}
			s := newController(app)
			s.curve = newCurveViewState(&app.Settings)
			v := s.curve
			text := "t=1 VBAT=1000\nt=2 VBAT=bad\nt=3 VBAT=2000"
			v.snapshot, err = internal.PreviewCurve(profile, text)
			if err != nil {
				t.Fatal(err)
			}
			v.running, v.paused, v.previewing = true, mode == "paused", mode == "preview"
			v.draft.name = "unsaved edit"
			v.dirty = true
			v.prepareCurveGeometry(gui.Rect{W: 400, H: 200})
			closed := &mygo.Window{}
			s.curveWindow = closed
			s.closeSerialCurve(closed)
			if s.curve != nil || s.curveWindow != nil || app.CurveRunning() {
				t.Fatal("closing retained the plot or left acquisition running")
			}
			var live internal.CurveSnapshot
			app.CurveSnapshot(&live)
			if len(live.Series) != 0 || live.Lines != 0 || app.Settings.CurveProfiles[0].Name != profile.Name {
				t.Fatal("closing retained live plot data or changed the saved profile")
			}
			s.curve = newCurveViewState(s.settings)
			s.curveWindow = &mygo.Window{}
			s.pollSerialCurve(s.curveWindow)
			fresh := s.curve
			if fresh.running || fresh.paused || fresh.previewing || fresh.dirty || len(fresh.snapshot.Series) != 0 || fresh.geometry.valid || len(fresh.paths) != 0 || fresh.draft.name != profile.Name {
				t.Fatal("reopening did not create a clean stopped plot using the saved profile")
			}
		})
	}
}

func TestCurveRetiredWindowCallbacksCannotOverwriteReopenedModel(t *testing.T) {
	profile := config.NewCurveProfile("firmware")
	app := &internal.App{Settings: config.Settings{Language: "en", CurveProfiles: []config.CurveProfile{profile}}}
	err := app.StartCurve(0)
	if err != nil {
		t.Fatal(err)
	}
	s := newController(app)
	s.curve = newCurveViewState(s.settings)
	retired, current := &mygo.Window{}, &mygo.Window{}
	s.curveWindow = current
	v := s.curve
	v.snapshot = curveTimeWindowSnapshot(t, "t=1 VBAT=1000\nt=2 VBAT=2000")
	want := curveTimeWindowSnapshot(t, "t=1 VBAT=1000\nt=2 VBAT=2000")
	v.running = true
	v.status = internal.Text{ZH: "新窗口", EN: "new window"}
	status := v.status
	changed := s.pollSerialCurve(retired)
	s.closeSerialCurve(retired)
	if changed || s.curveWindow != current || s.curve != v || !app.CurveRunning() || !v.running || v.status != status || !curveSnapshotsEqual(v.snapshot, want) {
		t.Fatal("a queued retired-window poll or close changed the new window's model")
	}
}
