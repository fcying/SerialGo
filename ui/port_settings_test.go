package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fcying/SerialGo/communication/serialport"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"

	gui "github.com/egoist/mygo/ui"
)

func portSettingsFixture(t *testing.T) (*controller, <-chan struct{}) {
	t.Helper()
	s, notified := settingsSaveFixture(t, filepath.Join(t.TempDir(), "config.toml"), config.Save)
	s.settings.Port = serialport.Settings{
		Name: `\\.\COM17`, Baud: "115200", DataBits: "8", Parity: "None", StopBits: "1",
	}
	s.settings.BaudRates = []string{"9600", "115200"}
	return s, notified
}

func closedPortSettingsSession(t *testing.T, s *controller) {
	t.Helper()
	s.session = internal.NewSession(&serialport.Port{}, 115200)
	t.Cleanup(func() {
		if err := s.session.Close(); err != nil {
			t.Error(err)
		}
	})
}

func TestPortSettingsDraftDoesNotChangeSettings(t *testing.T) {
	s, notified := portSettingsFixture(t)
	original, revision := s.settings, s.settingsRevision
	s.portSettingsError = localizedText{zh: "旧错误", en: "Old error"}
	s.openPortSettings()
	if !s.portSettingsOpen || s.portDraft != original.Port || s.portSettingsError != (localizedText{}) {
		t.Fatal("opening properties did not snapshot the port and clear the previous error")
	}
	s.portDraft = serialport.Settings{
		Name: "display-only name", Baud: "9600", DataBits: "7", Parity: "Even", StopBits: "2", DTR: true, RTS: true,
	}
	if !reflect.DeepEqual(s.settings, original) || s.settingsDirty || s.settingsRevision != revision {
		t.Fatal("editing the property draft changed settings before confirmation")
	}
	if err := s.flushSettings(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-notified:
		t.Fatal("editing the property draft invoked the settings writer")
	default:
	}
	if _, err := os.Stat(s.path); !os.IsNotExist(err) {
		t.Fatalf("editing the property draft created a configuration: %v", err)
	}
}

func TestPortSettingsCancelAndEscapeDiscardDraft(t *testing.T) {
	for _, dismissal := range []string{"Cancel", "Escape"} {
		t.Run(dismissal, func(t *testing.T) {
			s, _ := portSettingsFixture(t)
			original, revision := s.settings, s.settingsRevision
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
			if s.portSettingsOpen || !reflect.DeepEqual(s.settings, original) || s.settingsDirty || s.settingsRevision != revision {
				t.Fatal("dismissing the property dialog applied draft settings")
			}
			if err := tt.Click("Port settings"); err != nil {
				t.Fatal(err)
			}
			if !s.portSettingsOpen || s.portDraft != original.Port || s.portSettingsError != (localizedText{}) {
				t.Fatal("reopening the dialog retained the canceled draft")
			}
			if err := s.flushSettings(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(s.path); !os.IsNotExist(err) {
				t.Fatalf("canceling properties created a configuration: %v", err)
			}
		})
	}
}

func TestPortSettingsOKPersistsFieldsAndRawName(t *testing.T) {
	s, notified := portSettingsFixture(t)
	rawName := s.settings.Port.Name
	s.portChoice = "COM17 - Friendly serial adapter"
	s.openPortSettings()
	s.portDraft = serialport.Settings{
		Name: s.portChoice, Baud: "9600", DataBits: "7", Parity: "Even", StopBits: "2", DTR: true, RTS: true,
	}
	want := s.portDraft
	want.Name = rawName
	s.applyPortSettings()
	if s.portSettingsOpen || s.settings.Port != want {
		t.Fatalf("confirmation did not commit properties while preserving the raw device name: got %+v, want %+v", s.settings.Port, want)
	}
	completeSettingsSave(t, s, notified)
	stored := readSavedSettings(t, s.path)
	if stored.Port != want || s.settingsDirty {
		t.Fatalf("confirmed properties were not persisted: got %+v, want %+v, dirty=%v", stored.Port, want, s.settingsDirty)
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
			s.settingsChanged()
			s.saveUsedSettings()
			completeSettingsSave(t, s, notified)
			before, err := os.Stat(s.path)
			if err != nil {
				t.Fatal(err)
			}
			if dirty {
				draftSettings(s, "unsaved send text")
			}
			original, revision := s.settings, s.settingsRevision
			s.openPortSettings()
			s.applyPortSettings()
			if s.portSettingsOpen || !reflect.DeepEqual(s.settings, original) || s.settingsDirty != dirty || s.settingsRevision != revision {
				t.Fatal("unchanged confirmation altered settings or dirty state")
			}
			s.stopSettingsSaver()
			select {
			case <-notified:
				t.Fatal("unchanged confirmation invoked the settings writer")
			default:
			}
			after, err := os.Stat(s.path)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("unchanged confirmation rewrote configuration: %v", err)
			}
			if stored := readSavedSettings(t, s.path); stored.SendText != "" {
				t.Fatal("unchanged port confirmation persisted an unrelated draft")
			}
		})
	}
}

func TestPortSettingsConnectedFramingRemainsUnchanged(t *testing.T) {
	s, notified := portSettingsFixture(t)
	closedPortSettingsSession(t, s)
	original, revision := s.settings, s.settingsRevision
	s.openPortSettings()
	s.configureLog()
	tt := gui.NewTester(s.view, 800, 600)
	for _, label := range []string{"Baud rate", "Data bits", "Parity", "Stop bits"} {
		field, found := tt.AccessByRole(gui.RolePopUpButton, label)
		if !found || !field.Disabled {
			t.Fatalf("connected framing control %q is missing or enabled", label)
		}
	}
	for _, label := range []string{"RTS", "DTR"} {
		field, found := tt.AccessByRole(gui.RoleCheckBox, label)
		if !found || field.Disabled {
			t.Fatalf("connected control line %q is missing or disabled", label)
		}
	}
	s.portDraft.Name = "not the raw device name"
	s.portDraft.Baud = "9600"
	s.portDraft.DataBits = "7"
	s.portDraft.Parity = "Odd"
	s.portDraft.StopBits = "2"
	s.applyPortSettings()
	if s.portSettingsOpen || !reflect.DeepEqual(s.settings, original) || s.settingsDirty || s.settingsRevision != revision {
		t.Fatal("connected confirmation changed the active framing")
	}
	if s.portSettingsError != (localizedText{}) {
		t.Fatal("unchanged connected control lines unnecessarily accessed the closed driver")
	}
	s.stopSettingsSaver()
	select {
	case <-notified:
		t.Fatal("ignored connected framing changes invoked the settings writer")
	default:
	}
}

func TestPortSettingsLineFailureKeepsDialogAndSettings(t *testing.T) {
	for _, test := range []struct {
		name, line string
		dirty      bool
	}{
		{"DTR/clean", "DTR", false},
		{"DTR/dirty", "DTR", true},
		{"RTS/clean", "RTS", false},
		{"RTS/dirty", "RTS", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, notified := portSettingsFixture(t)
			closedPortSettingsSession(t, s)
			if test.dirty {
				draftSettings(s, "unrelated unsaved draft")
			}
			s.status = localizedText{zh: "原状态", en: "Original status"}
			original, revision, status := s.settings, s.settingsRevision, s.status
			s.openPortSettings()
			if test.line == "DTR" {
				s.portDraft.DTR = true
			} else {
				s.portDraft.RTS = true
			}
			draft := s.portDraft
			for range 2 {
				s.applyPortSettings()
				if !s.portSettingsOpen || !reflect.DeepEqual(s.settings, original) || s.settingsDirty != test.dirty || s.settingsRevision != revision || s.portDraft != draft {
					t.Fatal("a failed control-line application changed settings or discarded the retryable draft")
				}
				if s.portSettingsError.zh == "" || s.portSettingsError.en == "" || s.portSettingsError.zh == s.portSettingsError.en || !strings.Contains(s.portSettingsError.en, os.ErrClosed.Error()) {
					t.Fatalf("driver failure did not remain as a localized dialog error: %+v", s.portSettingsError)
				}
				if s.status != status || s.statusError {
					t.Fatal("the dialog driver error replaced the main operation status")
				}
			}
			s.configureLog()
			tt := gui.NewTester(s.view, 800, 600)
			if _, found := tt.Find(s.portSettingsError.en); !found {
				t.Fatal("the driver error is not displayed inside the property dialog")
			}
			if err := tt.Click("Cancel"); err != nil {
				t.Fatal(err)
			}
			if s.portSettingsOpen || !reflect.DeepEqual(s.settings, original) || s.settingsDirty != test.dirty || s.settingsRevision != revision {
				t.Fatal("canceling after a driver failure changed settings")
			}
			s.stopSettingsSaver()
			select {
			case <-notified:
				t.Fatal("failed control-line application invoked the settings writer")
			default:
			}
			if _, err := os.Stat(s.path); !os.IsNotExist(err) {
				t.Fatalf("failed control-line application created a configuration: %v", err)
			}
		})
	}
}
