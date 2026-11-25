package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	gui "github.com/egoist/mygo/ui"
	"github.com/fcying/SerialGo/communication/serialport"
	"golang.org/x/sys/windows"
)

// This integration test uses an explicitly supplied com0com pair, never a real device.
func TestWindowsLivePortSettingsGUI(t *testing.T) {
	name, peerName := os.Getenv("SERIALGO_TEST_COM_PORT"), os.Getenv("SERIALGO_TEST_COM_PEER")
	if name == "" || peerName == "" {
		t.Skip("virtual COM pair is not configured")
	}
	peerPath, err := windows.UTF16PtrFromString(`\\.\` + peerName)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := windows.CreateFile(peerPath, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(peer)
	s, callbacks := settingsSaveFixture(t, filepath.Join(t.TempDir(), "config.toml"))
	s.settings.Port = serialport.Settings{Name: name, Baud: "115200", DataBits: "8", Parity: "None", StopBits: "1"}
	s.settings.BaudRates = []string{"9600", "115200"}
	s.configureLog()
	s.app.SetPorts([]serialport.Info{{Name: name}})
	waitPort := func() {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for s.app.PortBusy {
			select {
			case fn := <-callbacks:
				fn()
			case <-timer.C:
				t.Fatal("serial operation timed out")
			}
		}
	}
	s.app.TogglePort()
	waitPort()
	if !s.app.Connected() {
		t.Fatalf("open failed: %s", s.statusText())
	}
	tt := gui.NewTester(s.view, 1280, 600)
	choose := func(label, value string) {
		t.Helper()
		trigger, ok := tt.AccessByRole(gui.RolePopUpButton, label)
		if !ok || trigger.Disabled || trigger.Offscreen {
			t.Fatalf("%s selector unavailable", label)
		}
		tt.ClickAt(trigger.Bounds.X+trigger.Bounds.W/2, trigger.Bounds.Y+trigger.Bounds.H/2)
		item, ok := tt.AccessByRole(gui.RoleOption, value)
		if !ok || item.Disabled || item.Offscreen {
			t.Fatalf("%s option %s unavailable", label, value)
		}
		tt.ClickAt(item.Bounds.X+item.Bounds.W/2, item.Bounds.Y+item.Bounds.H/2)
	}
	getPeer := func(option byte, size int) []byte {
		t.Helper()
		input := []byte{255, 'c', '0', 'c', 0, 0, option, 0, 1}
		output := make([]byte, size)
		var count uint32
		if err := windows.DeviceIoControl(peer, 0x1b007c, &input[0], uint32(len(input)), &output[0], uint32(len(output)), &count, nil); err != nil || count != uint32(size) {
			t.Fatalf("read peer parameters: %x count=%d err=%v", output, count, err)
		}
		return output
	}
	if port, ok := tt.AccessByRole(gui.RolePopUpButton, "Port"); !ok || !port.Disabled {
		t.Fatal("port selector must remain locked while connected")
	}
	choose("Baud rate", "9600")
	waitPort()
	if s.settings.Port.Baud != "9600" || s.app.StatusError {
		t.Fatalf("live baud not applied: %s", s.statusText())
	}
	if got := getPeer(1, 6); got[0] != 255 || got[1] != 16 || uint32(got[2])|uint32(got[3])<<8|uint32(got[4])<<16|uint32(got[5])<<24 != 9600 {
		t.Fatalf("peer baud = %x", got)
	}
	if err := tt.Click("Port settings"); err != nil {
		t.Fatal(err)
	}
	choose("Data bits", "7")
	choose("Parity", "Even")
	choose("Stop bits", "2")
	if err := tt.Click("RTS"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("DTR"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("OK"); err != nil {
		t.Fatal(err)
	}
	waitPort()
	if s.portSettingsOpen || s.app.StatusError {
		t.Fatalf("settings were not applied: %s", s.statusText())
	}
	if got := getPeer(2, 5); got[0] != 255 || got[1] != 17 || got[2] != 7 || got[3] != windows.EVENPARITY || got[4] != windows.TWOSTOPBITS {
		t.Fatalf("peer framing = %x, want 7E2", got)
	}
	var modem uint32
	if err := windows.GetCommModemStatus(peer, &modem); err != nil || modem&0x30 != 0x30 {
		t.Fatalf("peer modem status=%#x, want DSR and CTS set: %v", modem, err)
	}
}
