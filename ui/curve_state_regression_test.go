package ui

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/egoist/mygo"
	gui "github.com/egoist/mygo/ui"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"
)

func TestCurveCloseProtectsPartiallyRepairedLegacyRules(t *testing.T) {
	profile := config.NewCurveProfile("legacy")
	profile.Channels[0].Mode, profile.Channels[0].Pattern = "regex", "["
	second := profile.Channels[0]
	second.Name = "Second"
	profile.Channels = append(profile.Channels, second)
	app := &internal.App{Settings: config.Settings{Language: "en", CurveProfiles: []config.CurveProfile{profile}}}
	s := newController(app)
	s.curve = newCurveViewState(s.settings)
	s.curve.ruleEditor = newCurveRuleEdit(s.curve.draft, 0)
	s.curve.ruleEditor.channel.pattern = `(\d+)`
	s.confirmCurveRuleEditor()
	if s.curve.ruleEditor != nil || !s.curve.dirty || s.curve.error.en == "" || app.SettingsDirty {
		t.Fatal("partial repair must remain an unapplied dirty draft")
	}
	closed := false
	event := &mygo.CloseEvent{}
	s.confirmSerialCurveClose(event, func() { closed = true })
	if !event.DefaultPrevented() || !s.curve.confirmOpen {
		t.Fatal("closing a dirty curve did not request confirmation")
	}
	tt := gui.NewTester(s.serialCurveView, 1040, 600)
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if closed || s.curve.confirmOpen || !s.curve.dirty || s.curve.draft.channels[0].pattern != `(\d+)` || !reflect.DeepEqual(app.Settings.CurveProfiles[0], profile) {
		t.Fatal("canceling close lost or applied the partial repair")
	}
	s.curve.ruleEditor = newCurveRuleEdit(s.curve.draft, 1)
	s.curve.ruleEditor.channel.pattern = `(\d+)`
	s.confirmCurveRuleEditor()
	if s.curve.ruleEditor != nil || s.curve.dirty || !app.SettingsDirty || app.Settings.CurveProfiles[0].Channels[0].Pattern != `(\d+)` || app.Settings.CurveProfiles[0].Channels[1].Pattern != `(\d+)` {
		t.Fatal("completing the repair did not apply both rules")
	}
	event = &mygo.CloseEvent{}
	s.confirmSerialCurveClose(event, func() { closed = true })
	if event.DefaultPrevented() || s.curve.confirmOpen || closed {
		t.Fatal("clean close should proceed without confirmation")
	}
}

func TestCurveCloseDiscardConfirmation(t *testing.T) {
	s := newController(&internal.App{Settings: config.Settings{Language: "en"}})
	s.curve = newCurveViewState(s.settings)
	s.curve.dirty = true
	closed := false
	event := &mygo.CloseEvent{}
	s.confirmSerialCurveClose(event, func() { closed = true })
	if !event.DefaultPrevented() || !s.curve.confirmOpen {
		t.Fatal("dirty close was not intercepted")
	}
	tt := gui.NewTester(s.serialCurveView, 1040, 600)
	if err := tt.Click("OK"); err != nil {
		t.Fatal(err)
	}
	if !closed || s.curve.confirmOpen {
		t.Fatal("confirming discard did not close the curve")
	}
}

func TestMainCloseProtectsCurveDraft(t *testing.T) {
	for _, choice := range []string{"Cancel", "OK"} {
		t.Run(choice, func(t *testing.T) {
			s := newController(&internal.App{Settings: config.Settings{Language: "en"}})
			s.curve = newCurveViewState(s.settings)
			s.curve.dirty = true
			s.curveWindow = &mygo.Window{}
			closed := []string{}
			event := &mygo.CloseEvent{}
			s.confirmMainWindowClose(event, func() { closed = append(closed, "curve") }, func() { closed = append(closed, "main") })
			if !event.DefaultPrevented() || !s.curve.confirmOpen || len(closed) != 0 {
				t.Fatal("main close did not wait for dirty curve confirmation")
			}
			tt := gui.NewTester(s.serialCurveView, 1040, 600)
			if err := tt.Click(choice); err != nil {
				t.Fatal(err)
			}
			if choice == "Cancel" {
				if len(closed) != 0 || !s.curve.dirty || s.curve.confirmOpen {
					t.Fatal("canceling exit did not retain both windows and the draft")
				}
			} else if !reflect.DeepEqual(closed, []string{"curve", "main"}) {
				t.Fatalf("confirmed exit did not close both windows in order: %v", closed)
			}
		})
	}
}

func TestMainCloseWithoutDirtyCurve(t *testing.T) {
	for _, open := range []bool{false, true} {
		s := newController(&internal.App{Settings: config.Settings{Language: "en"}})
		if open {
			s.curve = newCurveViewState(s.settings)
			s.curveWindow = &mygo.Window{}
		}
		curveClosed, mainClosed := false, false
		event := &mygo.CloseEvent{}
		s.confirmMainWindowClose(event, func() { curveClosed = true }, func() { mainClosed = true })
		if event.DefaultPrevented() || curveClosed != open || mainClosed {
			t.Fatalf("unexpected clean close handling: open=%v prevented=%v curveClosed=%v mainClosed=%v", open, event.DefaultPrevented(), curveClosed, mainClosed)
		}
	}
}

func TestCurveManyStoredChannelsRemainEditable(t *testing.T) {
	for _, invalidColor := range []bool{false, true} {
		t.Run(fmt.Sprintf("invalid_color_%v", invalidColor), func(t *testing.T) {
			profile := config.NewCurveProfile("legacy")
			profile.Channels = make([]config.CurveChannel, 12)
			for i := range profile.Channels {
				profile.Channels[i] = config.CurveChannel{
					Name: fmt.Sprintf("Curve %d", i+1), Mode: "prefix", Pattern: "VBAT=", Scale: 1,
					Color: config.DefaultCurveColor(i), Enabled: true,
				}
			}
			if invalidColor {
				profile.Channels[len(profile.Channels)-1].Color = "invalid"
			}
			app := &internal.App{Settings: config.Settings{Language: "en", CurveProfiles: []config.CurveProfile{profile}}}
			before := config.CloneCurveProfiles(app.Settings.CurveProfiles)
			s := newController(app)
			s.curve = newCurveViewState(s.settings)
			tt := gui.NewTester(s.serialCurveView, 1040, 1100)
			if invalidColor {
				_, visible := tt.Find(s.curve.error.en)
				if s.curve.error.en == "" || !visible {
					t.Fatal("invalid stored color did not expose a visible validation error")
				}
			} else if s.curve.error.en != "" {
				t.Fatal("valid stored channels were rejected")
			}
			add, canAdd := tt.AccessByRole(gui.RoleButton, "Add curve")
			if !canAdd || add.Disabled {
				t.Fatal("the add action is unavailable above eight channels")
			}
			last := fmt.Sprintf("Curve %d", len(profile.Channels))
			edit, found := tt.AccessByRole(gui.RoleButton, "Edit curve "+last)
			if !found || edit.Offscreen || edit.Disabled {
				t.Fatal("the last stored channel cannot be edited")
			}
			if app.SettingsDirty || !reflect.DeepEqual(before, app.Settings.CurveProfiles) {
				t.Fatal("opening a profile modified or truncated stored channels")
			}
			err := tt.Click("Delete curve " + last)
			if err != nil {
				t.Fatal(err)
			}
			err = tt.Click("OK")
			if err != nil {
				t.Fatal(err)
			}
			channels := app.Settings.CurveProfiles[0].Channels
			if s.curve.error.en != "" || !reflect.DeepEqual(channels, before[0].Channels[:len(profile.Channels)-1]) {
				t.Fatal("confirmed correction did not retain all remaining channels")
			}
		})
	}
}

func TestCurveProfileSelectionRebuildsPreview(t *testing.T) {
	voltage := config.NewCurveProfile("Voltage")
	current := config.NewCurveProfile("Current")
	current.TimePattern, current.TimeScale = `T=(\d+)`, .001
	current.Channels[0].Name, current.Channels[0].Pattern, current.Channels[0].Unit = "IBAT", "IBAT=", "mA"
	app := &internal.App{Settings: config.Settings{Language: "en", CurveProfiles: []config.CurveProfile{voltage, current}}}
	s := newController(app)
	s.curve = newCurveViewState(s.settings)
	s.curve.previewText = "T=1000 VBAT=3712 IBAT=1024\nT=2000 VBAT=3680 IBAT=926\n"
	s.previewCurve()
	tt := gui.NewTester(s.serialCurveView, 1040, 600)
	err := tt.Click("Firmware profile")
	if err != nil {
		t.Fatal(err)
	}
	err = tt.Click("2. Current")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &s.curve.snapshot
	if app.Settings.CurveProfile != 1 || !s.curve.previewing || !snapshot.DeviceTime || len(snapshot.Series) != 1 {
		t.Fatal("profile selection did not rebuild the preview in the new clock mode")
	}
	series := &snapshot.Series[0]
	if series.Name != "IBAT" || series.Unit != "mA" || len(series.Points) != 2 || series.Points[0].X != 1 || series.Points[0].Y != 1.024 || series.Points[1].X != 2 || series.Points[1].Y != .926 {
		t.Fatalf("new profile displays stale extraction results: %+v", series)
	}
}

func TestCurvePausedColorEditRetainsFrozenSamples(t *testing.T) {
	s, notified, path := curveColorDialogFixture(t)
	// A paused display owns its retained points independently of the live parser.
	s.curve.previewing = false
	before := s.curve.snapshot
	points := append([]internal.CurvePoint(nil), before.Series[0].Points...)
	s.curve.ruleEditor = newCurveRuleEdit(s.curve.draft, 0)
	tt := gui.NewTester(s.curveRuleEditorView, 420, 440)
	setCurveProfileDialogInput(t, tt, curveColorHexLabel, "#D020A080")
	err := tt.Click("OK")
	if err != nil {
		t.Fatal(err)
	}
	after := &s.curve.snapshot
	if !s.curve.paused || s.curve.previewing || !s.curve.running || s.curve.position != 37 {
		t.Fatal("color confirmation changed paused acquisition or browsing state")
	}
	if len(after.Series) != 1 || after.Series[0].Color != 0xD020A080 || !reflect.DeepEqual(after.Series[0].Points, points) || after.Lines != before.Lines || after.Matched != before.Matched || after.Invalid != before.Invalid || after.Oversize != before.Oversize || after.DeviceTime != before.DeviceTime || after.LatestX != before.LatestX {
		t.Fatal("color confirmation replaced the frozen samples or counters with live data")
	}
	completeSettingsSave(t, s, notified)
	if readSavedSettings(t, path).CurveProfiles[0].Channels[0].Color != "#D020A080" {
		t.Fatal("paused color edit was not persisted")
	}
}

func TestCurveAdditionIgnoresUntouchedInvalidLegacyProfile(t *testing.T) {
	active := config.NewCurveProfile("active")
	invalid := config.NewCurveProfile("invalid stored regex")
	invalid.TimePattern = "["
	app := &internal.App{Settings: config.Settings{Language: "en", CurveProfiles: []config.CurveProfile{active, invalid}}}
	s := newController(app)
	s.curve = newCurveViewState(s.settings)
	s.curve.ruleEditor = newCurveRuleEdit(s.curve.draft, -1)
	tt := gui.NewTester(s.curveRuleEditorView, 420, 440)
	setCurveProfileDialogInput(t, tt, "Curve name", "Second")
	setCurveProfileDialogInput(t, tt, "Numeric prefix", "IBAT=")
	err := tt.Click("OK")
	if err != nil {
		t.Fatal(err)
	}
	view := gui.NewTester(s.serialCurveView, 1040, 600)
	edit, editable := view.AccessByRole(gui.RoleButton, "Edit curve Second")
	if s.curve.error.en != "" || !editable || edit.Offscreen || edit.Disabled {
		t.Fatal("untouched invalid legacy profile blocked a valid channel addition")
	}
	if len(app.Settings.CurveProfiles[0].Channels) != 2 || !app.SettingsDirty || app.Settings.CurveProfiles[1].TimePattern != "[" {
		t.Fatal("valid addition did not preserve the untouched legacy profile")
	}
}

func TestCurveAddBeyondEightPersistsAndPreviews(t *testing.T) {
	s, notified := settingsSaveFixture(t, t.TempDir()+"/config.toml")
	profile := config.NewCurveProfile("many channels")
	profile.Channels = make([]config.CurveChannel, 12)
	for i := range profile.Channels {
		profile.Channels[i] = config.CurveChannel{
			Name: fmt.Sprintf("Curve %d", i+1), Mode: "prefix", Pattern: "VBAT=", Scale: .001,
			Color: config.DefaultCurveColor(i), Enabled: true,
		}
	}
	err := s.app.ApplyCurveProfiles([]config.CurveProfile{profile}, 0)
	if err != nil {
		t.Fatal(err)
	}
	completeSettingsSave(t, s, notified)
	s.curve = newCurveViewState(s.settings)
	s.curve.previewText = "VBAT=3712\nEXTRA=99\n"
	s.previewCurve()
	view := gui.NewTester(s.serialCurveView, 1040, 600)
	add, found := view.AccessByRole(gui.RoleButton, "Add curve")
	if !found || add.Disabled {
		t.Fatal("cannot add beyond eight channels")
	}
	s.curve.ruleEditor = newCurveRuleEdit(s.curve.draft, -1)
	editor := gui.NewTester(s.curveRuleEditorView, 420, 440)
	setCurveProfileDialogInput(t, editor, "Curve name", "Extra")
	setCurveProfileDialogInput(t, editor, "Numeric prefix", "EXTRA=")
	err = editor.Click("OK")
	if err != nil {
		t.Fatal(err)
	}
	view.Frame()
	snapshot := &s.curve.snapshot
	if len(snapshot.Series) != 13 || snapshot.Series[12].Name != "Extra" || len(snapshot.Series[12].Points) != 1 || snapshot.Series[12].Points[0].Y != 99 {
		t.Fatal("new channel did not extract and display its own samples")
	}
	completeSettingsSave(t, s, notified)
	stored := readSavedSettings(t, s.app.SettingsPath)
	if !reflect.DeepEqual(stored.CurveProfiles, s.settings.CurveProfiles) || stored.CurveProfiles[0].Channels[12].Pattern != "EXTRA=" {
		t.Fatal("additional channels did not persist unchanged")
	}
}

func TestCurveInvalidLegacySelectionAndDeletionStopAcquisition(t *testing.T) {
	for _, selectInvalid := range []bool{false, true} {
		t.Run(fmt.Sprintf("select_%v", selectInvalid), func(t *testing.T) {
			valid, invalid := config.NewCurveProfile("valid"), config.NewCurveProfile("invalid")
			invalid.TimePattern = "["
			app := &internal.App{Settings: config.Settings{Language: "en", CurveProfiles: []config.CurveProfile{valid, invalid}}}
			s := newController(app)
			s.curve = newCurveViewState(s.settings)
			s.startCurve()
			view := gui.NewTester(s.serialCurveView, 1040, 600)
			var err error
			if selectInvalid {
				err = view.Click("Firmware profile")
				if err != nil {
					t.Fatal(err)
				}
				err = view.Click("2. invalid")
			} else {
				err = view.Click("Delete")
				if err != nil {
					t.Fatal(err)
				}
				err = view.Click("OK")
			}
			if err != nil {
				t.Fatal(err)
			}
			view.Frame()
			_, errorVisible := view.Find(s.curve.error.en)
			start, canStart := view.AccessByRole(gui.RoleButton, "Plot acquisition")
			if app.CurveRunning() || s.curve.running || s.curve.error.en == "" || !errorVisible || !canStart || start.Disabled || len(s.curve.snapshot.Series) != 0 {
				t.Fatalf("invalid legacy replacement state: app running=%v, view running=%v, error=%q, error visible=%v, start available=%v, series=%d", app.CurveRunning(), s.curve.running, s.curve.error.en, errorVisible, canStart, len(s.curve.snapshot.Series))
			}
			err = view.Click("Plot acquisition")
			if err != nil {
				t.Fatal(err)
			}
			if app.CurveRunning() || s.curve.running || s.curve.error.en == "" {
				t.Fatal("invalid legacy rule was treated as successful empty acquisition")
			}
			s.curve.profileEditor = newCurveProfileEdit(s.curve.draft)
			s.curve.profileEditor.timePattern = ""
			s.confirmCurveProfileEditor()
			s.startCurve()
			if !app.CurveRunning() || !s.curve.running || s.curve.error.en != "" || s.settings.CurveProfiles[s.curve.selected].TimePattern != "" {
				t.Fatal("stopped invalid legacy profile could not be repaired and restarted")
			}
		})
	}
}

func TestCurveInvalidLegacyDoesNotBlockLanguageSave(t *testing.T) {
	path := t.TempDir() + "/config.toml"
	s, notified := settingsSaveFixture(t, path)
	legacy := config.NewCurveProfile("legacy")
	legacy.TimePattern, legacy.TimeScale = "[", 0
	s.settings.CurveProfiles = []config.CurveProfile{legacy}
	s.curve = newCurveViewState(s.settings)
	changed := s.app.SetLanguage("zh-CN")
	if !changed {
		t.Fatal("language change was not accepted")
	}
	completeSettingsSave(t, s, notified)
	stored := readSavedSettings(t, path)
	if stored.Language != "zh-CN" || !reflect.DeepEqual(stored.CurveProfiles, []config.CurveProfile{legacy}) || s.app.SettingsDirty {
		t.Fatal("invalid legacy curves blocked or contaminated the language settings save")
	}
	changed = s.app.SetLanguage("en")
	if !changed {
		t.Fatal("second language change was not accepted")
	}
	completeSettingsSave(t, s, notified)
	stored = readSavedSettings(t, path)
	if stored.Language != "en" || !reflect.DeepEqual(stored.CurveProfiles, []config.CurveProfile{legacy}) || s.app.CurveRunning() {
		t.Fatal("saving an unrelated setting changed legacy rules or started acquisition")
	}
}
