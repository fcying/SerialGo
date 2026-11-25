//go:build linux

package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	gui "github.com/egoist/mygo/ui"
	"github.com/fcying/SerialGo/communication/serialport"
)

func livePortSettingsFixture(t *testing.T) (*controller, *gui.Tester, <-chan func()) {
	t.Helper()
	_, port := openCurvePerfPTY(t)
	s, callbacks := portSettingsFixture(t)
	s.settings.Port.Name = port
	s.configureLog()
	s.app.SetPorts([]serialport.Info{{Name: port}})
	s.app.TogglePort()
	completePortOperation(t, s, callbacks)
	if !s.app.Connected() {
		t.Fatalf("could not connect to the test port: %s", s.statusText())
	}
	return s, gui.NewTester(s.view, 1280, 600), callbacks
}

func completePortOperation(t *testing.T, s *controller, callbacks <-chan func()) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for s.app.PortBusy {
		select {
		case fn := <-callbacks:
			fn()
		case <-deadline:
			t.Fatal("serial port operation did not complete")
		}
	}
}

func requirePortControl(t *testing.T, tt *gui.Tester, role gui.Role, label string, disabled bool) {
	t.Helper()
	control, found := tt.AccessByRole(role, label)
	if !found || control.Disabled != disabled || control.Expanded {
		t.Fatalf("%q: want disabled=%t and collapsed, got %+v (found=%t)", label, disabled, control, found)
	}
}

func chooseLivePortOption(t *testing.T, tt *gui.Tester, label, value string) {
	t.Helper()
	trigger, found := tt.AccessByRole(gui.RolePopUpButton, label)
	if !found || trigger.Disabled || trigger.Offscreen {
		t.Fatalf("selector %q is unavailable", label)
	}
	tt.ClickAt(trigger.Bounds.X+trigger.Bounds.W/2, trigger.Bounds.Y+trigger.Bounds.H/2)
	option, found := tt.AccessByRole(gui.RoleOption, value)
	if !found || option.Disabled || option.Offscreen {
		t.Fatalf("option %q in %q is unavailable", value, label)
	}
	tt.ClickAt(option.Bounds.X+option.Bounds.W/2, option.Bounds.Y+option.Bounds.H/2)
}

func clickPortControl(t *testing.T, tt *gui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
}

func TestConnectedBaudControlsApplyLiveOrStageForDialog(t *testing.T) {
	s, tt, callbacks := livePortSettingsFixture(t)
	requirePortControl(t, tt, gui.RolePopUpButton, "Port", true)
	chooseLivePortOption(t, tt, "Baud rate", "9600")
	if !s.app.PortBusy || s.settings.Port.Baud != "115200" {
		t.Fatal("main baud selection did not keep the confirmed value while pending")
	}
	requirePortControl(t, tt, gui.RolePopUpButton, "Baud rate", true)
	completePortOperation(t, s, callbacks)
	tt.Frame()
	if s.settings.Port.Baud != "9600" || s.app.StatusError {
		t.Fatalf("main baud change did not complete: %s", s.statusText())
	}

	clickBaudCustomAction(t, tt, s)
	typeCustomBaud(t, tt, s, "38400")
	clickPortControl(t, tt, "OK")
	if s.customBaudOpen || !s.app.PortBusy || s.settings.Port.Baud != "9600" {
		t.Fatal("connected custom baud did not submit the live change")
	}
	completePortOperation(t, s, callbacks)
	tt.Frame()
	if s.settings.Port.Baud != "38400" || !slices.Contains(s.settings.BaudRates, "38400") || s.app.StatusError {
		t.Fatalf("custom baud did not apply and append the rate: %s", s.statusText())
	}

	clickPortControl(t, tt, "Port settings")
	requirePortControl(t, tt, gui.RolePopUpButton, "Stop bits", false)
	chooseLivePortOption(t, tt, "Baud rate", "115200")
	chooseLivePortOption(t, tt, "Data bits", "7")
	chooseLivePortOption(t, tt, "Stop bits", "2")
	chooseLivePortOption(t, tt, "Parity", "Even")
	clickBaudCustomAction(t, tt, s)
	typeCustomBaud(t, tt, s, "19200")
	// Keep confirmation scoped to the inner dialog.
	tt.Key(0, gui.KeyTab)
	tt.Key(0, gui.KeyEnter)
	if s.customBaudOpen || !s.portSettingsOpen || s.app.PortBusy || s.portDraft.Baud != "19200" || s.portDraft.DataBits != "7" || s.portDraft.StopBits != "2" || s.portDraft.Parity != "Even" || !slices.Contains(s.portBaudRatesDraft, "19200") {
		t.Fatalf("connected settings edits were not staged: %+v", s.portDraft)
	}
	if s.settings.Port.Baud != "38400" || s.settings.Port.DataBits != "8" || s.settings.Port.Parity != "None" || slices.Contains(s.settings.BaudRates, "19200") {
		t.Fatalf("connected draft changed the port before confirmation: %+v", s.settings.Port)
	}
}

func TestConnectedPortSettingsCompletionKeepsCorrectDialog(t *testing.T) {
	for _, tc := range []struct {
		name        string
		fail, close bool
	}{
		{name: "success"},
		{name: "driver error", fail: true},
		{name: "closed during success", close: true},
		{name: "closed during failure", fail: true, close: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, tt, callbacks := livePortSettingsFixture(t)
			original := s.settings.Port
			clickPortControl(t, tt, "Port settings")
			if tc.fail {
				// PTYs have no modem lines; asserting DTR returns a driver error.
				clickPortControl(t, tt, "DTR")
			} else {
				chooseLivePortOption(t, tt, "Baud rate", "9600")
			}
			draft := s.portDraft
			clickPortControl(t, tt, "OK")
			if !s.app.PortBusy || !s.portSettingsOpen || s.settings.Port != original {
				t.Fatal("confirmation did not leave the dialog open and settings unchanged while pending")
			}
			for _, label := range []string{"Baud rate", "Data bits", "Parity", "Stop bits"} {
				requirePortControl(t, tt, gui.RolePopUpButton, label, true)
			}
			requirePortControl(t, tt, gui.RoleButton, "OK", true)
			if tc.close {
				clickPortControl(t, tt, "Cancel")
				requirePortControl(t, tt, gui.RoleButton, "Port settings", true)
				s.openPortSettings()
				if s.portSettingsOpen {
					t.Fatal("settings reopened while the driver operation was pending")
				}
			}
			completePortOperation(t, s, callbacks)
			tt.Frame()

			want := draft
			if tc.fail {
				want = original
			}
			if s.settings.Port != want {
				t.Fatalf("completed port settings = %+v, want %+v", s.settings.Port, want)
			}
			switch {
			case tc.close:
				if s.portSettingsOpen {
					t.Fatal("completion reopened the closed dialog")
				}
				if tc.fail && (!s.app.StatusError || !strings.Contains(s.app.Status.EN, "Failed to set control lines") || !strings.Contains(s.app.Status.EN, "hardware state may not match configuration")) {
					t.Fatalf("closed dialog lost the driver or rollback failure: %+v", s.app.Status)
				}
				requirePortControl(t, tt, gui.RoleButton, "Port settings", false)
				clickPortControl(t, tt, "Port settings")
				if !s.portSettingsOpen || s.portDraft != want {
					t.Fatalf("reopened draft = %+v; want committed settings %+v", s.portDraft, want)
				}
			case tc.fail:
				if !s.portSettingsOpen || s.portDraft != draft || s.portSettingsError.en == "" {
					t.Fatal("driver error did not show an error and preserve the open draft")
				}
				requirePortControl(t, tt, gui.RoleButton, "OK", false)
			default:
				if s.portSettingsOpen {
					t.Fatal("successful completion did not close the dialog")
				}
			}
		})
	}
}
