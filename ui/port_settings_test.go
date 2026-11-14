package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/fcying/SerialGo/communication/serialport"

	gui "github.com/egoist/mygo/ui"
)

func portSettingsFixture(t *testing.T) (*controller, <-chan func()) {
	t.Helper()
	s, notified := settingsSaveFixture(t, filepath.Join(t.TempDir(), "config.toml"))
	s.settings.Port = serialport.Settings{
		Name: `\\.\COM17`, Baud: "115200", DataBits: "8", Parity: "None", StopBits: "1",
	}
	s.settings.BaudRates = []string{"9600", "115200"}
	s.portDraft = s.settings.Port
	return s, notified
}

func TestPortFramingSelectionAppliesOnlyOnConfirmation(t *testing.T) {
	s, notified := portSettingsFixture(t)
	s.configureLog()
	original := s.settings.Port
	tt := gui.NewTester(s.view, 1280, 600)
	if err := tt.Click("Port settings"); err != nil {
		t.Fatal(err)
	}
	trigger, found := tt.AccessByRole(gui.RolePopUpButton, "Data bits")
	if !found || trigger.Disabled {
		t.Fatal("property data-bits selector is missing or disabled")
	}
	tt.ClickAt(trigger.Bounds.X+trigger.Bounds.W/2, trigger.Bounds.Y+trigger.Bounds.H/2)
	option, found := tt.AccessByRole(gui.RoleOption, "7")
	if !found || option.Disabled || option.Offscreen {
		t.Fatal("data-bits option 7 is unavailable")
	}
	tt.ClickAt(option.Bounds.X+option.Bounds.W/2, option.Bounds.Y+option.Bounds.H/2)
	if s.settings.Port != original || s.portDraft.DataBits != "7" || s.app.SettingsDirty {
		t.Fatalf("framing draft was committed before confirmation: port=%+v draft=%+v dirty=%v", s.settings.Port, s.portDraft, s.app.SettingsDirty)
	}
	if _, err := os.Stat(s.app.SettingsPath); !os.IsNotExist(err) {
		t.Fatalf("editing framing created a configuration before confirmation: %v", err)
	}
	if err := tt.Click("OK"); err != nil {
		t.Fatal(err)
	}
	want := original
	want.DataBits = "7"
	if s.settings.Port != want || s.portDraft.DataBits != "7" || !s.app.SettingsDirty {
		t.Fatalf("confirmed framing selection was not applied: port=%+v draft=%+v dirty=%v", s.settings.Port, s.portDraft, s.app.SettingsDirty)
	}
	completeSettingsSave(t, s, notified)
	if stored := readSavedSettings(t, s.app.SettingsPath); stored.Port != want || s.app.SettingsDirty {
		t.Fatalf("confirmed framing selection was not persisted: port=%+v dirty=%v", stored.Port, s.app.SettingsDirty)
	}
}

func TestPortSettingDropdownValuesAreCentered(t *testing.T) {
	s, _ := portSettingsFixture(t)
	s.configureLog()
	var expectedBaudWidth float32
	var expectedDialogContentWidth float32
	tt := gui.NewTester(func(c *gui.Context) {
		s.view(c)
		standardWidth, _ := c.MeasureText(0, gui.Span{Text: "1000000"})
		selectedWidth, _ := c.MeasureText(0, gui.Span{Text: s.settings.Port.Baud})
		expectedBaudWidth = max(standardWidth, selectedWidth) + baudPickerChromeWidth
		fieldRowWidth := portSettingsFieldLabelWidth + sectionGap + expectedBaudWidth
		actionRowWidth := 2*portSettingsActionButtonWidth + wideGap
		expectedDialogContentWidth = max(fieldRowWidth, actionRowWidth)
	}, 800, 600)
	if err := tt.Click("Port settings"); err != nil {
		t.Fatal(err)
	}
	tt.SetScale(2)
	dialog, dialogFound := tt.AccessByRole(gui.RoleDialog, "Port settings")
	if !dialogFound {
		t.Fatal("port settings dialog is missing")
	}
	if dialog.Bounds.W < expectedDialogContentWidth-0.01 || dialog.Bounds.W >= 340 {
		t.Fatalf("port settings dialog width=%v, want content width %v and narrower than 340", dialog.Bounds.W, expectedDialogContentWidth)
	}
	dialogCenter := dialog.Bounds.X + dialog.Bounds.W/2
	if dialogCenter < 399.5 || dialogCenter > 400.5 {
		t.Fatalf("port settings dialog center=%v, want 400 for symmetric side padding", dialogCenter)
	}
	baudPicker, baudFound := tt.AccessByRole(gui.RolePopUpButton, "Baud rate")
	if !baudFound {
		t.Fatal("baud selector is missing from Port settings")
	}
	if baudPicker.Bounds.W < expectedBaudWidth-0.01 || baudPicker.Bounds.W > expectedBaudWidth+0.01 {
		t.Fatalf("port-settings baud width=%v, want %v", baudPicker.Bounds.W, expectedBaudWidth)
	}
	selectors := []struct {
		label string
		value string
	}{
		{label: "Data bits", value: "8"},
		{label: "Stop bits", value: "1"},
		{label: "Parity", value: "None"},
	}
	pickers := make([]gui.AccessInfo, len(selectors))
	for i, test := range selectors {
		picker, found := tt.AccessByRole(gui.RolePopUpButton, test.label)
		if !found || picker.Value != test.value {
			t.Fatalf("%s dropdown value = %q, found=%v; want %q", test.label, picker.Value, found, test.value)
		}
		pickers[i] = picker
		if picker.Bounds.W < baudPicker.Bounds.W-0.01 || picker.Bounds.W > baudPicker.Bounds.W+0.01 {
			t.Errorf("%s selector width=%v, want baud selector width %v", test.label, picker.Bounds.W, baudPicker.Bounds.W)
		}
	}
	probe, probeFound := tt.Find("Port settings")
	if !probeFound {
		t.Fatal("port settings text render probe is missing")
	}
	skipIfTextPixelsUnavailable(t, tt, probe)
	img := tt.Image()
	for i, test := range selectors {
		picker := pickers[i]
		left := picker.Bounds.X + baudPickerBorderWidth + serialSelectorPaddingX
		right := picker.Bounds.X + picker.Bounds.W - baudPickerBorderWidth - serialSelectorPaddingX - serialSelectorArrowPaddingX - baudPickerArrowSize - microGap
		minX, maxX := img.Bounds().Max.X, -1
		yStart := int((picker.Bounds.Y + 4) * 2)
		yEnd := int((picker.Bounds.Y + picker.Bounds.H - 4) * 2)
		xStart := int(left * 2)
		xEnd := int(right * 2)
		for yOffset := range yEnd - yStart {
			y := yStart + yOffset
			for xOffset := range xEnd - xStart {
				x := xStart + xOffset
				pixel := img.RGBAAt(x, y)
				if pixel.R < 100 && pixel.G < 100 && pixel.B < 100 {
					minX, maxX = min(minX, x), max(maxX, x)
				}
			}
		}
		if maxX < minX {
			t.Fatalf("%s dropdown value %q did not render: picker=%+v textArea=%.2f..%.2f", test.label, test.value, picker.Bounds, left, right)
		}
		inkCenter := float32(minX+maxX+1) / 4
		textAreaCenter := (left + right) / 2
		if inkCenter < textAreaCenter-1 || inkCenter > textAreaCenter+1 {
			t.Errorf("%s value is not centered: inkCenter=%.2f textAreaCenter=%.2f", test.label, inkCenter, textAreaCenter)
		}
	}
}

func TestPortSettingsDraftDoesNotChangeSettings(t *testing.T) {
	s, notified := portSettingsFixture(t)
	original, revision := *s.settings, s.app.SettingsRevision
	s.portSettingsError = localizedText{zh: "旧错误", en: "Old error"}
	s.openPortSettings()
	if !s.portSettingsOpen || s.portDraft != original.Port || s.portSettingsError != (localizedText{}) {
		t.Fatal("opening properties did not snapshot the port and clear the previous error")
	}
	s.portDraft = serialport.Settings{
		Name: "display-only name", Baud: "9600", DataBits: "7", Parity: "Even", StopBits: "2", DTR: true, RTS: true,
	}
	if !reflect.DeepEqual(*s.settings, original) || s.app.SettingsDirty || s.app.SettingsRevision != revision {
		t.Fatal("editing the property draft changed settings before confirmation")
	}
	select {
	case <-notified:
		t.Fatal("editing the property draft invoked the settings writer")
	default:
	}
	if _, err := os.Stat(s.app.SettingsPath); !os.IsNotExist(err) {
		t.Fatalf("editing the property draft created a configuration: %v", err)
	}
}

func TestPortSettingsCancelAndEscapeDiscardDraft(t *testing.T) {
	for _, dismissal := range []string{"Cancel", "Escape"} {
		t.Run(dismissal, func(t *testing.T) {
			s, _ := portSettingsFixture(t)
			original, revision := *s.settings, s.app.SettingsRevision
			s.configureLog()
			tt := gui.NewTester(s.view, 800, 600)
			if err := tt.Click("Port settings"); err != nil {
				t.Fatal(err)
			}
			if !s.portSettingsOpen {
				t.Fatal("Settings did not open the property dialog")
			}
			for _, line := range []string{"RTS", "DTR"} {
				if err := tt.Click(line); err != nil {
					t.Fatal(err)
				}
			}
			if !s.portDraft.RTS || !s.portDraft.DTR {
				t.Fatal("the dialog did not edit its control-line draft")
			}
			s.portDraft.Baud = "9600"
			if dismissal == "Escape" {
				tt.Key(0, gui.KeyEscape)
			} else if err := tt.Click("Cancel"); err != nil {
				t.Fatal(err)
			}
			if s.portSettingsOpen || !reflect.DeepEqual(*s.settings, original) || s.portDraft != original.Port || s.app.SettingsDirty || s.app.SettingsRevision != revision {
				t.Fatal("dismissing the property dialog did not restore settings and framing controls")
			}
			if err := tt.Click("Port settings"); err != nil {
				t.Fatal(err)
			}
			if !s.portSettingsOpen || s.portDraft != original.Port || s.portSettingsError != (localizedText{}) {
				t.Fatal("reopening the dialog retained the canceled draft")
			}
			if _, err := os.Stat(s.app.SettingsPath); !os.IsNotExist(err) {
				t.Fatalf("canceling properties created a configuration: %v", err)
			}
		})
	}
}

func TestPortSettingsOKPersistsFieldsAndRawName(t *testing.T) {
	s, notified := portSettingsFixture(t)
	rawName := s.settings.Port.Name
	s.app.PortChoice = "COM17 - Friendly serial adapter"
	s.openPortSettings()
	s.portDraft = serialport.Settings{
		Name: s.app.PortChoice, Baud: "9600", DataBits: "7", Parity: "Even", StopBits: "2", DTR: true, RTS: true,
	}
	want := s.portDraft
	want.Name = rawName
	s.applyPortSettings()
	if s.portSettingsOpen || s.settings.Port != want {
		t.Fatalf("confirmation did not commit properties while preserving the raw device name: got %+v, want %+v", s.settings.Port, want)
	}
	completeSettingsSave(t, s, notified)
	stored := readSavedSettings(t, s.app.SettingsPath)
	if stored.Port != want || s.app.SettingsDirty {
		t.Fatalf("confirmed properties were not persisted: got %+v, want %+v, dirty=%v", stored.Port, want, s.app.SettingsDirty)
	}
}

func TestPortSettingsUnchangedOKDoesNotWrite(t *testing.T) {
	for _, dirty := range []bool{false, true} {
		name := "clean"
		if dirty {
			name = "unrelated draft"
		}
		t.Run(name, func(t *testing.T) {
			s, notified := portSettingsFixture(t)
			s.app.SettingsChanged()
			s.app.SaveUsedSettings()
			completeSettingsSave(t, s, notified)
			before, err := os.Stat(s.app.SettingsPath)
			if err != nil {
				t.Fatal(err)
			}
			if dirty {
				draftSettings(s, "unsaved send text")
			}
			original, revision := *s.settings, s.app.SettingsRevision
			s.openPortSettings()
			s.applyPortSettings()
			if s.portSettingsOpen || !reflect.DeepEqual(*s.settings, original) || s.app.SettingsDirty != dirty || s.app.SettingsRevision != revision {
				t.Fatal("unchanged confirmation altered settings or dirty state")
			}
			select {
			case <-notified:
				t.Fatal("unchanged confirmation invoked the settings writer")
			default:
			}
			after, err := os.Stat(s.app.SettingsPath)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("unchanged confirmation rewrote configuration: %v", err)
			}
			if stored := readSavedSettings(t, s.app.SettingsPath); stored.SendText != "" {
				t.Fatal("unchanged port confirmation persisted an unrelated draft")
			}
		})
	}
}
