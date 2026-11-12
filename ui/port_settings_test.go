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
	return s, notified
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
			if s.portSettingsOpen || !reflect.DeepEqual(*s.settings, original) || s.app.SettingsDirty || s.app.SettingsRevision != revision {
				t.Fatal("dismissing the property dialog applied draft settings")
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
