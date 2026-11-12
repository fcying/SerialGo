package internal

import (
	"github.com/BurntSushi/toml"
	"os"
	"path/filepath"
	"slices"
	"github.com/fcying/SerialGo/communication/serialport"
	"github.com/fcying/SerialGo/config"
	"testing"
)

func TestPortSelectionPersistsOnlyDeviceName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	s, notified := settingsSaveFixture(t, path, config.Save)
	s.Settings.Port.Name = "COM12"
	s.SetPorts([]serialport.Info{
		{Name: "COM12", Description: "USB-SERIAL CH340"},
		{Name: "COM29", Description: "USB Serial (COM12) 中文"},
		{Name: "COM99"},
	})
	for _, name := range []string{"COM29", "COM99"} {
		if name == "COM29" {
			s.PortChoice = "COM29 USB Serial (COM12) 中文"
		} else {
			s.PortChoice = "COM99"
		}
		s.SelectPort()
		if s.Settings.Port.Name != name || !s.SettingsDirty {
			t.Fatalf("selection %q did not retain raw device identity %q", s.PortChoice, name)
		}
		s.SaveUsedSettings()
		completeSettingsSave(t, s, notified)
		stored := readSavedSettings(t, path)
		if stored.Port.Name != name {
			t.Fatalf("saved port = %q; want raw name %q", stored.Port.Name, name)
		}
	}
}

func TestPortMetadataRefreshPreservesSelection(t *testing.T) {
	s := &App{}
	s.Settings.Port.Name = "COM29"
	s.SetPorts([]serialport.Info{
		{Name: "COM12", Description: "USB-SERIAL CH340"},
		{Name: "COM29", Description: "Original device name"},
	})
	s.SetPorts([]serialport.Info{
		{Name: "COM29", Description: "Updated device name"},
		{Name: "COM12", Description: "USB-SERIAL CH340"},
	})
	if s.Settings.Port.Name != "COM29" || s.PortChoice != "COM29 Updated device name" || s.SettingsDirty {
		t.Fatal("metadata-only refresh changed or dirtied the selected device")
	}
	s.SetPorts([]serialport.Info{{Name: "COM99"}})
	if s.Settings.Port.Name != "COM99" || s.PortChoice != "COM99" || !s.SettingsDirty {
		t.Fatal("removing the selected device did not select the remaining bare-name port")
	}
	s.SettingsDirty = false
	s.SetPorts(nil)
	if s.Settings.Port.Name != "" || s.PortChoice != "" || !s.SettingsDirty {
		t.Fatal("an empty refresh retained a removed device")
	}
}

func TestBaudRateChoicesNormalizeOnlyConfiguredValues(t *testing.T) {
	for _, test := range []struct {
		name  string
		rates []string
		want  []string
	}{
		{"absent", nil, nil},
		{"explicit empty", []string{}, nil},
		{"numeric sorting", []string{"500000", "38400", "250000"}, []string{"38400", "250000", "500000"}},
		{"canonical duplicates", []string{"00115200", "0250000", "250000", "250000"}, []string{"115200", "250000"}},
		{"invalid values removed", []string{"0", "-1", "4294967296", "invalid", "250000"}, []string{"250000"}},
		{"maximum", []string{"4294967295"}, []string{"4294967295"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := baudRateChoices(test.rates); !slices.Equal(got, test.want) {
				t.Fatalf("baud choices = %v, want %v", got, test.want)
			}
		})
	}
}

func TestInitializeBaudRatesNormalizesAndHonorsDeletedValues(t *testing.T) {
	for _, test := range []struct {
		name, current, wantCurrent string
		rates, wantRates           []string
	}{
		{"retained selection", "38400", "38400", []string{"250000", "38400"}, []string{"38400", "250000"}},
		{"deleted current selects first", "38400", "250000", []string{"500000", "250000"}, []string{"250000", "500000"}},
		{"explicit empty stays empty", "38400", "", []string{}, nil},
		{"absent stays empty", "115200", "", nil, nil},
		{"canonical deduplication", "0038400", "38400", []string{"0038400", "38400", "0250000"}, []string{"38400", "250000"}},
		{"invalid selection", "Custom...", "250000", []string{"250000"}, []string{"250000"}},
		{"invalid values removed", "38400", "250000", []string{"0", "-1", "4294967296", "invalid", "250000"}, []string{"250000"}},
		{"maximum selected", "4294967295", "4294967295", []string{"4294967295"}, []string{"4294967295"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := &App{Settings: config.Settings{BaudRates: slices.Clone(test.rates)}}
			s.Settings.Port.Baud = test.current
			s.InitializeBaudRates()
			if s.Settings.Port.Baud != test.wantCurrent {
				t.Fatalf("startup selected baud = %q, want %q", s.Settings.Port.Baud, test.wantCurrent)
			}
			requireBaudRates(t, s.Settings.BaudRates, test.wantRates)
		})
	}
}

func TestDeletedBaudRatesDoNotReturnOnRestart(t *testing.T) {
	for _, test := range []struct {
		list, selected string
	}{
		{"[]", ""},
		{"[\"250000\"]", "250000"},
	} {
		t.Run(test.list, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			source := "BaudRates = " + test.list + "\n[Port]\nBaud = \"115200\"\n"
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			s := &App{}
			if _, err := toml.DecodeFile(path, &s.Settings); err != nil {
				t.Fatal(err)
			}
			s.InitializeBaudRates()
			if s.Settings.Port.Baud != test.selected || slices.Contains(s.Settings.BaudRates, "115200") || slices.Contains(baudRateChoices(s.Settings.BaudRates), "115200") {
				t.Fatal("startup revived a deleted baud")
			}
			if err := config.Save(path, s.Settings); err != nil {
				t.Fatal(err)
			}
			restarted := &App{Settings: readSavedSettings(t, path)}
			restarted.InitializeBaudRates()
			if restarted.Settings.Port.Baud != test.selected || !slices.Equal(restarted.Settings.BaudRates, s.Settings.BaudRates) {
				t.Fatal("deletion did not survive persistence and a second restart")
			}
		})
	}
}

func requireBaudRates(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("baud rates = %v, want %v", got, want)
	}
}
