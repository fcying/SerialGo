package ui

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/fcying/SerialGo/communication/serialport"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"

	gui "github.com/egoist/mygo/ui"
)

func TestPortSelectionPersistsOnlyDeviceName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	s, notified := settingsSaveFixture(t, path, config.Save)
	s.settings.Port.Name = "COM12"
	s.setPorts([]serialport.Info{
		{Name: "COM12", Description: "USB-SERIAL CH340"},
		{Name: "COM29", Description: "USB Serial (COM12) 中文"},
		{Name: "COM99"},
	})
	for _, name := range []string{"COM29", "COM99"} {
		if name == "COM29" {
			s.portChoice = "COM29 USB Serial (COM12) 中文"
		} else {
			s.portChoice = "COM99"
		}
		s.selectPort()
		if s.settings.Port.Name != name || !s.settingsDirty {
			t.Fatalf("selection %q did not retain raw device identity %q", s.portChoice, name)
		}
		s.saveUsedSettings()
		completeSettingsSave(t, s, notified)
		stored := readSavedSettings(t, path)
		if stored.Port.Name != name {
			t.Fatalf("saved port = %q; want raw name %q", stored.Port.Name, name)
		}
	}
}

func TestPortMetadataRefreshPreservesSelection(t *testing.T) {
	s := &controller{}
	s.settings.Port.Name = "COM29"
	s.setPorts([]serialport.Info{
		{Name: "COM12", Description: "USB-SERIAL CH340"},
		{Name: "COM29", Description: "Original device name"},
	})
	s.setPorts([]serialport.Info{
		{Name: "COM29", Description: "Updated device name"},
		{Name: "COM12", Description: "USB-SERIAL CH340"},
	})
	if s.settings.Port.Name != "COM29" || s.portChoice != "COM29 Updated device name" || s.settingsDirty {
		t.Fatal("metadata-only refresh changed or dirtied the selected device")
	}
	s.setPorts([]serialport.Info{{Name: "COM99"}})
	if s.settings.Port.Name != "COM99" || s.portChoice != "COM99" || !s.settingsDirty {
		t.Fatal("removing the selected device did not select the remaining bare-name port")
	}
	s.settingsDirty = false
	s.setPorts(nil)
	if s.settings.Port.Name != "" || s.portChoice != "" || !s.settingsDirty {
		t.Fatal("an empty refresh retained a removed device")
	}
}

func portPickerTrigger(t *testing.T, tt *gui.Tester) gui.AccessInfo {
	t.Helper()
	trigger, ok := tt.AccessByRole(gui.RolePopUpButton, "Port")
	if !ok {
		t.Fatal("port picker trigger is missing")
	}
	return trigger
}

func clickPortPickerTrigger(t *testing.T, tt *gui.Tester) {
	t.Helper()
	bounds := portPickerTrigger(t, tt).Bounds
	tt.ClickAt(bounds.X+bounds.W/2, bounds.Y+bounds.H/2)
}

func requirePortPickerOption(t *testing.T, tt *gui.Tester, label string) gui.AccessInfo {
	t.Helper()
	option, ok := tt.AccessByRole(gui.RoleOption, label)
	if !ok || option.Disabled || option.Offscreen {
		t.Fatalf("available port option %q is missing or unavailable: %+v", label, option)
	}
	return option
}

func requirePortPickerSelection(t *testing.T, tt *gui.Tester, s *controller, name, label string) {
	t.Helper()
	trigger := portPickerTrigger(t, tt)
	if trigger.Value != label || s.settings.Port.Name != name {
		t.Fatalf("picker value %q, saved identity %q; want display %q and raw name %q", trigger.Value, s.settings.Port.Name, label, name)
	}
	if label != "" && trigger.Description != label {
		t.Fatalf("picker tooltip = %q; want full label %q", trigger.Description, label)
	}
}

func TestPortPickerRefreshesChoicesOncePerOpening(t *testing.T) {
	s := &controller{settings: config.Settings{Language: "en"}}
	discovered := []serialport.Info{
		{Name: "COM12", Description: "USB-SERIAL CH340"},
		{Name: "COM29", Description: "USB Serial (COM12) 中文"},
	}
	refreshes := 0
	tt := gui.NewTester(func(c *gui.Context) {
		gui.Row(c.Key("port-picker-test")).FillWidth().Children(func() {
			s.portPicker(c, func() {
				refreshes++
				s.setPorts(discovered)
			})
		})
	}, 400, 300)
	if trigger := portPickerTrigger(t, tt); trigger.Disabled || trigger.Expanded || trigger.Value != "" || refreshes != 0 {
		t.Fatalf("initial empty picker should be enabled and closed without enumerating: %+v, refreshes=%d", trigger, refreshes)
	}
	clickPortPickerTrigger(t, tt)
	if !portPickerTrigger(t, tt).Expanded {
		t.Fatal("empty picker did not open to discover newly attached devices")
	}
	requirePortPickerOption(t, tt, "COM12 USB-SERIAL CH340")
	chosen := requirePortPickerOption(t, tt, "COM29 USB Serial (COM12) 中文")
	requirePortPickerSelection(t, tt, s, "COM12", "COM12 USB-SERIAL CH340")

	// A changed discovery result must not replace the already-open choices.
	discovered = []serialport.Info{
		{Name: "COM44", Description: "New adapter"},
		{Name: "COM29", Description: "Updated adapter name"},
	}
	for range 3 {
		tt.Frame()
	}
	requirePortPickerOption(t, tt, "COM12 USB-SERIAL CH340")
	requirePortPickerOption(t, tt, "COM29 USB Serial (COM12) 中文")
	if _, ok := tt.AccessByRole(gui.RoleOption, "COM44 New adapter"); ok {
		t.Fatal("an ordinary open frame replaced choices without reopening")
	}
	tt.ClickAt(chosen.Bounds.X+chosen.Bounds.W/2, chosen.Bounds.Y+chosen.Bounds.H/2)
	tt.Frame()
	requirePortPickerSelection(t, tt, s, "COM29", "COM29 USB Serial (COM12) 中文")
	if portPickerTrigger(t, tt).Expanded || refreshes != 1 {
		t.Fatalf("selecting should close without re-enumerating; refreshes=%d", refreshes)
	}

	clickPortPickerTrigger(t, tt)
	requirePortPickerOption(t, tt, "COM44 New adapter")
	requirePortPickerOption(t, tt, "COM29 Updated adapter name")
	if _, ok := tt.AccessByRole(gui.RoleOption, "COM12 USB-SERIAL CH340"); ok {
		t.Fatal("reopening retained a removed device")
	}
	requirePortPickerSelection(t, tt, s, "COM29", "COM29 Updated adapter name")
	tt.Key(0, gui.KeyEscape)
	tt.Frame()
	requirePortPickerSelection(t, tt, s, "COM29", "COM29 Updated adapter name")
	if portPickerTrigger(t, tt).Expanded || refreshes != 2 {
		t.Fatalf("closing should preserve identity without re-enumerating; refreshes=%d", refreshes)
	}

	discovered = []serialport.Info{{Name: "COM44", Description: "Remaining adapter"}, {Name: "COM55"}}
	clickPortPickerTrigger(t, tt)
	requirePortPickerOption(t, tt, "COM44 Remaining adapter")
	requirePortPickerOption(t, tt, "COM55")
	if _, ok := tt.AccessByRole(gui.RoleOption, "COM29 Updated adapter name"); ok {
		t.Fatal("removing the selected port left its old option visible")
	}
	requirePortPickerSelection(t, tt, s, "COM44", "COM44 Remaining adapter")
	tt.Key(0, gui.KeyEscape)
	if refreshes != 3 {
		t.Fatalf("expected one enumeration per opening, got %d", refreshes)
	}

	discovered = nil
	clickPortPickerTrigger(t, tt)
	requirePortPickerSelection(t, tt, s, "", "")
	if trigger := portPickerTrigger(t, tt); !trigger.Expanded || trigger.Disabled {
		t.Fatalf("no-device result should leave the empty popup open and enabled: %+v", trigger)
	}
	if _, ok := tt.AccessByRole(gui.RoleOption, ""); ok {
		t.Fatal("empty discovery left a selectable port in the popup")
	}
	tt.Frame()
	tt.Key(0, gui.KeyEscape)
	discovered = []serialport.Info{{Name: "COM77", Description: "Hot-plugged adapter"}}
	clickPortPickerTrigger(t, tt)
	requirePortPickerOption(t, tt, "COM77 Hot-plugged adapter")
	requirePortPickerSelection(t, tt, s, "COM77", "COM77 Hot-plugged adapter")
	if refreshes != 5 {
		t.Fatalf("empty discovery or closing repeated enumeration; got %d refreshes", refreshes)
	}
}

func TestPortPickerKeyboardExpansionRefreshesChoices(t *testing.T) {
	for _, key := range []struct {
		name string
		key  gui.Key
	}{{"Down", gui.KeyDown}, {"Enter", gui.KeyEnter}, {"Space", gui.KeySpace}} {
		t.Run(key.name, func(t *testing.T) {
			s := &controller{settings: config.Settings{Language: "en"}}
			discovered := []serialport.Info{{Name: "COM12", Description: "First adapter"}, {Name: "COM29", Description: "USB Serial (COM12) 中文"}}
			refreshes := 0
			tt := gui.NewTester(func(c *gui.Context) {
				gui.Row(c.Key("port-picker-test")).FillWidth().Children(func() {
					s.portPicker(c, func() {
						refreshes++
						s.setPorts(discovered)
					})
				})
			}, 400, 300)
			tt.Key(0, gui.KeyTab)
			if !portPickerTrigger(t, tt).Focused {
				t.Fatal("Tab did not focus the port picker")
			}
			tt.Key(0, key.key)
			if !portPickerTrigger(t, tt).Expanded {
				t.Fatal("keyboard activation did not expand the initially empty picker")
			}
			requirePortPickerOption(t, tt, "COM12 First adapter")
			requirePortPickerOption(t, tt, "COM29 USB Serial (COM12) 中文")
			tt.Key(0, gui.KeyEnd)
			tt.Key(0, gui.KeyEnter)
			requirePortPickerSelection(t, tt, s, "COM29", "COM29 USB Serial (COM12) 中文")
			if portPickerTrigger(t, tt).Expanded || refreshes != 1 {
				t.Fatalf("keyboard selection should close without enumerating; refreshes=%d", refreshes)
			}
			discovered = []serialport.Info{{Name: "COM29", Description: "Renamed adapter"}}
			tt.Key(0, key.key)
			requirePortPickerOption(t, tt, "COM29 Renamed adapter")
			requirePortPickerSelection(t, tt, s, "COM29", "COM29 Renamed adapter")
			tt.Frame()
			if !portPickerTrigger(t, tt).Expanded || refreshes != 2 {
				t.Fatalf("keyboard reopening should enumerate exactly once; refreshes=%d", refreshes)
			}
		})
	}
}

func TestPortPickerConnectedDisablesExpansionAndEnumeration(t *testing.T) {
	for _, empty := range []bool{false, true} {
		name := "with devices"
		if empty {
			name = "without devices"
		}
		t.Run(name, func(t *testing.T) {
			s := &controller{settings: config.Settings{Language: "en"}}
			if !empty {
				s.setPorts([]serialport.Info{{Name: "COM12", Description: "Connected adapter"}})
			}
			// A closed session still represents the controller's connected state,
			// without touching a physical serial port or leaving a receive loop.
			s.session = internal.NewSession(&serialport.Port{}, 115200)
			if err := s.session.Close(); err != nil {
				t.Fatal(err)
			}
			originalName, originalLabel := s.settings.Port.Name, s.portChoice
			refreshes := 0
			tt := gui.NewTester(func(c *gui.Context) {
				gui.Row(c.Key("port-picker-test")).FillWidth().Children(func() {
					s.portPicker(c, func() {
						refreshes++
						s.setPorts([]serialport.Info{{Name: "COM99", Description: "Must not be discovered"}})
					})
				})
			}, 400, 300)
			if !portPickerTrigger(t, tt).Disabled {
				t.Fatal("connected picker is not disabled")
			}
			if !empty {
				bounds := portPickerTrigger(t, tt).Bounds
				tt.Move(bounds.X+bounds.W/2, bounds.Y+bounds.H/2)
				tt.Advance(time.Second)
				tip, ok := tt.AccessByRole(gui.RoleTooltip, "")
				if !ok || tip.Name != originalLabel {
					t.Fatalf("locked picker hover did not show the complete port name: %+v", tip)
				}
				tt.Move(399, 299)
				tt.Advance(time.Second)
				if _, ok := tt.AccessByRole(gui.RoleTooltip, ""); ok {
					t.Fatal("port tooltip remained after the pointer left")
				}
			}
			clickPortPickerTrigger(t, tt)
			tt.Key(0, gui.KeyTab)
			for _, key := range []gui.Key{gui.KeyDown, gui.KeyEnter, gui.KeySpace} {
				tt.Key(0, key)
			}
			tt.Frame()
			requirePortPickerSelection(t, tt, s, originalName, originalLabel)
			if portPickerTrigger(t, tt).Expanded || refreshes != 0 {
				t.Fatalf("connected picker expanded or enumerated: refreshes=%d", refreshes)
			}
			if _, ok := tt.AccessByRole(gui.RoleOption, ""); ok {
				t.Fatal("connected picker exposed selectable options")
			}
		})
	}
}

func TestMainViewHasNoStandalonePortRefreshButton(t *testing.T) {
	s := &controller{settings: config.Settings{Language: "en"}}
	s.configureLog()
	tt := gui.NewTester(s.view, 800, 600)
	if trigger := portPickerTrigger(t, tt); trigger.Disabled || trigger.Expanded {
		t.Fatalf("main view must offer the empty, closed port picker: %+v", trigger)
	}
	if _, ok := tt.AccessByRole(gui.RoleButton, "Refresh"); ok {
		t.Fatal("main view still exposes a standalone port refresh button")
	}
}
