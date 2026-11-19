package ui

import (
	"testing"

	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"

	"github.com/egoist/mygo"
)

func TestWindowStateStoreLoadsExplicitGeometry(t *testing.T) {
	want := config.WindowSettings{
		X: 720, Y: 169, Width: 1000, Height: 700,
		PixelX: 1080, PixelY: 254, PixelWidth: 1500, PixelHeight: 1050,
		Maximized: true, PositionSet: true,
	}
	app := &internal.App{Settings: config.Settings{Window: want}}
	s := newController(app)
	states, err := s.LoadWindowStates()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := states[mainWindowStateKey]
	if !ok {
		t.Fatal("explicit window geometry was omitted from MyGo state")
	}
	wantState := mygo.WindowState{
		X: want.X, Y: want.Y, Width: want.Width, Height: want.Height,
		PixelX: want.PixelX, PixelY: want.PixelY, PixelWidth: want.PixelWidth, PixelHeight: want.PixelHeight,
		Maximized: want.Maximized,
	}
	if got != wantState {
		t.Fatalf("stored MyGo window state = %+v, want %+v", got, wantState)
	}
}

func TestWindowStateStoreOmitsCenteredGeometry(t *testing.T) {
	app := &internal.App{Settings: config.Settings{Window: config.WindowSettings{Width: 800, Height: 600}}}
	s := newController(app)
	states, err := s.LoadWindowStates()
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 0 {
		t.Fatalf("centered window unexpectedly restored saved state: %+v", states)
	}
}

func TestWindowStateStorePersistsNativePixelBounds(t *testing.T) {
	initial := config.WindowSettings{X: 100, Y: 80, Width: 900, Height: 600, PositionSet: true}
	app := &internal.App{Settings: config.Settings{Window: initial}}
	s := newController(app)
	state := mygo.WindowState{
		X: 120, Y: 90, Width: 1000, Height: 650, Maximized: true,
		PixelX: 180, PixelY: 135, PixelWidth: 1500, PixelHeight: 975,
	}
	if err := s.SaveWindowStates(map[string]mygo.WindowState{mainWindowStateKey: state}); err != nil {
		t.Fatal(err)
	}
	s.syncWindowState()
	s.persistWindowGeometry()
	want := config.WindowSettings{
		X: 120, Y: 90, Width: 1000, Height: 650, Maximized: true, PositionSet: true,
		PixelX: 180, PixelY: 135, PixelWidth: 1500, PixelHeight: 975,
	}
	if app.Settings.Window != want || !app.SettingsDirty || app.SettingsRevision != 1 {
		t.Fatalf("native window bounds were not persisted: settings=%+v dirty=%v revision=%d", app.Settings.Window, app.SettingsDirty, app.SettingsRevision)
	}
}

func TestWindowStateStoreKeepsUnsetPositionCentered(t *testing.T) {
	initial := config.WindowSettings{Width: 800, Height: 600}
	app := &internal.App{Settings: config.Settings{Window: initial}}
	s := newController(app)
	state := mygo.WindowState{X: 100, Y: 80, Width: 800, Height: 600, PixelX: 100, PixelY: 80, PixelWidth: 800, PixelHeight: 600}
	if err := s.SaveWindowStates(map[string]mygo.WindowState{mainWindowStateKey: state}); err != nil {
		t.Fatal(err)
	}
	s.syncWindowState()
	s.persistWindowGeometry()
	if app.Settings.Window != initial || app.SettingsDirty || app.SettingsRevision != 0 {
		t.Fatalf("unchanged centered window was persisted as an explicit position: settings=%+v dirty=%v revision=%d", app.Settings.Window, app.SettingsDirty, app.SettingsRevision)
	}
}

func TestCurveWindowStatePersistsIndependentlyOfMainWindow(t *testing.T) {
	path := t.TempDir() + "/config.toml"
	s, notified := settingsSaveFixture(t, path)
	main := config.WindowSettings{X: 50, Y: 40, Width: 825, Height: 620, PositionSet: true}
	s.settings.Window = main
	plot := mygo.WindowState{
		X: 0, Y: 0, Width: 970, Height: 650,
		PixelX: 0, PixelY: 0, PixelWidth: 1455, PixelHeight: 975,
		Maximized: true,
	}
	states := map[string]mygo.WindowState{
		mainWindowStateKey:  {X: 150, Y: 90, Width: 900, Height: 700},
		curveWindowStateKey: plot,
	}
	err := s.SaveWindowStates(states)
	if err != nil {
		t.Fatal(err)
	}
	completeSettingsSave(t, s, notified)
	stored := readSavedSettings(t, path)
	if stored.Window != main || !stored.CurveWindow.PositionSet || s.app.SettingsDirty {
		t.Fatalf("plot close did not save its own geometry independently: main=%+v plot=%+v dirty=%v", stored.Window, stored.CurveWindow, s.app.SettingsDirty)
	}
	reopened := newController(&internal.App{Settings: stored})
	restored, err := reopened.LoadWindowStates()
	if err != nil {
		t.Fatal(err)
	}
	if restored[curveWindowStateKey] != plot {
		t.Fatalf("restored plot geometry = %+v, want %+v", restored[curveWindowStateKey], plot)
	}
	revision := s.app.SettingsRevision
	err = s.SaveWindowStates(states)
	if err != nil {
		t.Fatal(err)
	}
	if s.app.SettingsRevision != revision {
		t.Fatal("unchanged plot geometry scheduled another settings save")
	}
}
