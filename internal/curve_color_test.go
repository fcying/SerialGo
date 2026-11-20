package internal

import (
	"reflect"
	"testing"
	"time"

	"github.com/fcying/SerialGo/config"
)

func TestCurveLiveColorPreservesAcquisition(t *testing.T) {
	profile := config.NewCurveProfile("device")
	profile.TimePattern = `t=([0-9]+)`
	profile.Channels[0].Mode = "regex"
	profile.Channels[0].Pattern = `VBAT=([0-9]+)`
	app := &App{}
	err := app.ApplyCurveProfiles([]config.CurveProfile{profile}, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = app.StartCurve(0)
	if err != nil {
		t.Fatal(err)
	}
	receive := testCurveReceiver(app.curveReceiver())
	receive([]byte("t=1000 VBAT=1000\nt=2000 VBAT="), time.Unix(10, 0))
	var snapshot CurveSnapshot
	app.CurveSnapshot(&snapshot)
	points := append([]CurvePoint(nil), snapshot.Series[0].Points...)
	revision, settingsRevision := snapshot.Revision, app.SettingsRevision
	generation := app.curve.generation
	regex, timeRegex := app.curve.parser.compiled.rules[0].regex, app.curve.parser.compiled.timeRegex
	ringPoints := &app.curve.parser.rings[0].points[0]
	profiles := config.CloneCurveProfiles(app.Settings.CurveProfiles)
	profiles[0].Channels[0].Color = "#00000000"
	err = app.ApplyCurveProfiles(profiles, 0)
	if err != nil {
		t.Fatal(err)
	}
	app.CurveSnapshot(&snapshot)
	if snapshot.Series[0].Color != 0 || snapshot.Revision != revision+1 || app.SettingsRevision != settingsRevision+1 || !app.SettingsDirty {
		t.Fatalf("transparent black was lost or did not invalidate the snapshot/settings: %+v", snapshot)
	}
	if !app.curve.enabled.Load() || app.curve.generation != generation || app.curve.parser.lineSize != len("t=2000 VBAT=") || !app.curve.parser.haveX || app.curve.parser.lastX != 1 {
		t.Fatal("color edit changed running state, partial input, or device-time continuity")
	}
	if app.curve.parser.compiled.rules[0].regex != regex || app.curve.parser.compiled.timeRegex != timeRegex || &app.curve.parser.rings[0].points[0] != ringPoints {
		t.Fatal("color edit recompiled regexes or replaced ring storage")
	}
	if snapshot.Lines != 1 || snapshot.Matched != 1 || snapshot.Invalid != 0 || snapshot.Oversize != 0 || !snapshot.DeviceTime || !reflect.DeepEqual(snapshot.Series[0].Points, points) {
		t.Fatalf("color edit lost existing samples or counters: %+v", snapshot)
	}
	receive([]byte("2000\n"), time.Unix(11, 0))
	app.CurveSnapshot(&snapshot)
	if len(snapshot.Series[0].Points) != 2 || snapshot.Series[0].Points[1].X != 2 || snapshot.Series[0].Points[1].Y != 2 || snapshot.Series[0].Points[1].Break {
		t.Fatalf("color edit discarded the partial line or split the series: %+v", snapshot)
	}
	app.StopCurve()
	profiles = config.CloneCurveProfiles(app.Settings.CurveProfiles)
	profiles[0].Channels[0].Color = "#11223380"
	revision = snapshot.Revision
	err = app.ApplyCurveProfiles(profiles, 0)
	if err != nil {
		t.Fatal(err)
	}
	app.CurveSnapshot(&snapshot)
	if app.curve.enabled.Load() || snapshot.Series[0].Color != 0x11223380 || snapshot.Revision != revision+1 || len(snapshot.Series[0].Points) != 2 {
		t.Fatalf("stopped color edit restarted or cleared acquisition: %+v", snapshot)
	}
	settingsRevision, revision = app.SettingsRevision, snapshot.Revision
	err = app.ApplyCurveProfiles(profiles, 0)
	if err != nil {
		t.Fatal(err)
	}
	if app.SettingsRevision != settingsRevision || app.curve.parser.revision != revision {
		t.Fatal("unchanged colors triggered a settings write or snapshot update")
	}
}

func TestCurveColorsFollowConfiguredChannels(t *testing.T) {
	profile := config.NewCurveProfile("device")
	profile.Channels[0].Enabled = false
	profile.Channels[0].Color = ""
	second := profile.Channels[0]
	second.Name, second.Pattern, second.Enabled = "current", "I=", true
	third := second
	third.Name, third.Pattern, third.Color = "temperature", "T=", "#000000"
	profile.Channels = append(profile.Channels, second, third)
	app := &App{}
	err := app.ApplyCurveProfiles([]config.CurveProfile{profile}, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = app.StartCurve(0)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot CurveSnapshot
	app.CurveSnapshot(&snapshot)
	if len(snapshot.Series) != 2 || snapshot.Series[0].Color != 0xD94B35FF || snapshot.Series[1].Color != 0x000000FF {
		t.Fatalf("legacy fallback colors used enabled indexes or treated black as missing: %+v", snapshot)
	}
	profiles := config.CloneCurveProfiles(app.Settings.CurveProfiles)
	profiles[0].Channels[0].Enabled = true
	err = app.ApplyCurveProfiles(profiles, 0)
	if err != nil {
		t.Fatal(err)
	}
	app.CurveSnapshot(&snapshot)
	if len(snapshot.Series) != 3 || snapshot.Series[0].Color != 0x1677CCFF || snapshot.Series[1].Color != 0xD94B35FF || snapshot.Series[2].Color != 0x000000FF {
		t.Fatalf("enabling a preceding channel shifted another channel's color: %+v", snapshot)
	}
	profiles = config.CloneCurveProfiles(app.Settings.CurveProfiles)
	profiles[0].Channels = profiles[0].Channels[2:]
	err = app.ApplyCurveProfiles(profiles, 0)
	if err != nil {
		t.Fatal(err)
	}
	app.CurveSnapshot(&snapshot)
	if len(snapshot.Series) != 1 || snapshot.Series[0].Name != "temperature" || snapshot.Series[0].Color != 0x000000FF {
		t.Fatalf("deleting preceding channels shifted a selected color: %+v", snapshot)
	}
}

func TestCurveColorWithoutCompiledSeries(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "not started", true: "all disabled"}[disabled], func(t *testing.T) {
			profile := config.NewCurveProfile("device")
			profile.Channels[0].Enabled = !disabled
			app := &App{}
			// Settings can exist before the first parser has been created.
			app.Settings.CurveProfiles = []config.CurveProfile{profile}
			if disabled {
				err := app.StartCurve(0)
				if err != nil {
					t.Fatal(err)
				}
			}
			profiles := config.CloneCurveProfiles(app.Settings.CurveProfiles)
			profiles[0].Channels[0].Color = "#00000000"
			err := app.ApplyCurveProfiles(profiles, 0)
			if err != nil {
				t.Fatal(err)
			}
			var snapshot CurveSnapshot
			app.CurveSnapshot(&snapshot)
			if len(snapshot.Series) != 0 || app.curve.enabled.Load() != disabled || app.Settings.CurveProfiles[0].Channels[0].Color != "#00000000" {
				t.Fatalf("color update without series changed acquisition state: %+v", snapshot)
			}
		})
	}
}
