package ui

import (
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/egoist/mygo"
	gui "github.com/egoist/mygo/ui"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"
)

func TestCurveSidebarWindowClampPreservesPreferredWidth(t *testing.T) {
	for _, language := range []string{"en", "zh-CN"} {
		for _, scale := range []float32{1, 1.25, 1.5} {
			t.Run(fmt.Sprintf("%s_scale_%g", language, scale), func(t *testing.T) {
				const preferredWidth = 650
				app := &internal.App{Settings: config.Settings{Language: language, CurveSidebarWidth: preferredWidth}}
				s := newController(app)
				s.curve = newCurveViewState(s.settings)
				tt := gui.NewTester(s.serialCurveView, 1200, 600)
				tt.SetScale(scale)
				initial, found := tt.Find(s.tr("串口曲线图", "Serial curve chart"))
				if !found {
					t.Fatal("chart is missing")
				}
				tt.SetSize(800, 600)
				clamped, _ := tt.Find(s.tr("串口曲线图", "Serial curve chart"))
				if clamped.W < curveChartMinWidth-1 || clamped.X+clamped.W > 800 || clamped.X >= initial.X {
					t.Fatalf("narrow window did not reserve chart space: %+v", clamped)
				}
				divider, found := tt.AccessByRole(gui.RoleSplitter, "")
				if !found {
					t.Fatal("sidebar divider is missing")
				}
				tt.ClickAt(divider.Bounds.X+divider.Bounds.W/2, divider.Bounds.Y+divider.Bounds.H/2)
				err := tt.Click(s.tr("隐藏侧边栏", "Hide sidebar"))
				if err != nil {
					t.Fatal(err)
				}
				err = tt.Click(s.tr("显示侧边栏", "Show sidebar"))
				if err != nil {
					t.Fatal(err)
				}
				tt.SetSize(1200, 600)
				restored, _ := tt.Find(s.tr("串口曲线图", "Serial curve chart"))
				if math.Abs(float64(restored.X-initial.X)) > 1 || s.curve.rulesWidth != preferredWidth || app.Settings.CurveSidebarWidth != preferredWidth || app.SettingsDirty {
					t.Fatalf("viewport clamping or visibility overwrote the width preference: chart=%+v width=%g", restored, s.curve.rulesWidth)
				}
			})
		}
	}
}

func TestCurveSidebarMinimumWidthSurvivesDoubleClickAndSavesOnClose(t *testing.T) {
	path := t.TempDir() + "/config.toml"
	s, notified := settingsSaveFixture(t, path)
	s.curve = newCurveViewState(s.settings)
	if s.curve.rulesWidth != 260 || !s.curve.rulesVisible {
		t.Fatal("first opening did not use the default visible sidebar")
	}
	tt := gui.NewTester(s.serialCurveView, 1000, 600)
	divider, found := tt.AccessByRole(gui.RoleSplitter, "")
	if !found {
		t.Fatal("sidebar divider is missing")
	}
	x, y := divider.Bounds.X+divider.Bounds.W/2, divider.Bounds.Y+divider.Bounds.H/2
	tt.Press(x, y)
	tt.Move(x-200, y)
	if s.app.SettingsDirty {
		t.Fatal("dragging marked the width dirty before release")
	}
	_, err := os.Stat(path)
	if !os.IsNotExist(err) {
		t.Fatalf("config must not be written while dragging: stat error = %v", err)
	}
	tt.Release(x-200, y)
	if s.settings.CurveSidebarWidth != curveRulesContentMinWidth || !s.app.SettingsDirty {
		t.Fatal("drag release did not mark the minimum width for the next save")
	}
	divider, found = tt.AccessByRole(gui.RoleSplitter, "")
	if !found {
		t.Fatal("sidebar divider is missing after resizing")
	}
	x, y = divider.Bounds.X+divider.Bounds.W/2, divider.Bounds.Y+divider.Bounds.H/2
	revision := s.app.SettingsRevision
	tt.ClickAt(x, y)
	tt.ClickAt(x, y)
	if s.curve.rulesWidth != curveRulesContentMinWidth || s.app.SettingsRevision != revision {
		t.Fatal("double-click changed the sidebar width")
	}
	err = tt.Click("Hide sidebar")
	if err != nil {
		t.Fatal(err)
	}
	if s.curve.rulesVisible || s.curve.rulesWidth != curveRulesContentMinWidth {
		t.Fatal("hiding discarded the dragged width")
	}
	_, err = os.Stat(path)
	if !os.IsNotExist(err) {
		t.Fatalf("resizing and hiding must not write config: stat error = %v", err)
	}
	// Close without a geometry change, so the window-state store does not save for us.
	w := &mygo.Window{}
	s.curveWindow = w
	s.closeSerialCurve(w)
	completeSettingsSave(t, s, notified)
	stored := readSavedSettings(t, path)
	restarted := newController(&internal.App{Settings: stored})
	restarted.curve = newCurveViewState(restarted.settings)
	if !restarted.curve.rulesVisible || restarted.curve.rulesWidth != curveRulesContentMinWidth {
		t.Fatal("restart did not restore the saved width independently of session visibility")
	}
}
