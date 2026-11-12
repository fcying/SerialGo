package ui

import (
	"testing"
	"time"

	"github.com/fcying/SerialGo/communication/serialport"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"

	gui "github.com/egoist/mygo/ui"
)

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
	s := newController(&internal.App{Settings: config.Settings{Language: "en"}})
	discovered := []serialport.Info{
		{Name: "COM12", Description: "USB-SERIAL CH340"},
		{Name: "COM29", Description: "USB Serial (COM12) 中文"},
	}
	refreshes := 0
	tt := gui.NewTester(func(c *gui.Context) {
		gui.Row(c.Key("port-picker-test")).FillWidth().Children(func() {
			s.portPicker(c, func() {
				refreshes++
				s.app.SetPorts(discovered)
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
			s := newController(&internal.App{Settings: config.Settings{Language: "en"}})
			discovered := []serialport.Info{{Name: "COM12", Description: "First adapter"}, {Name: "COM29", Description: "USB Serial (COM12) 中文"}}
			refreshes := 0
			tt := gui.NewTester(func(c *gui.Context) {
				gui.Row(c.Key("port-picker-test")).FillWidth().Children(func() {
					s.portPicker(c, func() {
						refreshes++
						s.app.SetPorts(discovered)
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

func TestPortPickerRemainsOpenWhileAsyncRefreshCompletes(t *testing.T) {
	s, callbacks := settingsSaveFixture(t, t.TempDir()+"/config.toml")
	tt := gui.NewTester(func(c *gui.Context) {
		gui.Row(c).FillWidth().Children(func() { s.portPicker(c, s.app.RefreshPorts) })
	}, 400, 300)
	clickPortPickerTrigger(t, tt)
	tt.Frame()
	trigger := portPickerTrigger(t, tt)
	if !trigger.Expanded || trigger.Disabled || !s.app.RefreshingPorts {
		t.Fatalf("refresh closed or locked the popup before its result: %+v", trigger)
	}
	select {
	case complete := <-callbacks:
		complete()
	case <-time.After(5 * time.Second):
		t.Fatal("port enumeration did not complete")
	}
	tt.Frame()
	trigger = portPickerTrigger(t, tt)
	if !trigger.Expanded || trigger.Disabled || s.app.PortBusy || s.app.RefreshingPorts {
		t.Fatalf("refresh result did not leave the popup available: %+v", trigger)
	}
}
