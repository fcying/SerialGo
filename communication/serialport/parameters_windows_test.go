package serialport

import (
	"os"
	"testing"
	"unsafe"

	serial "github.com/albenik/go-serial/v2"
	"golang.org/x/sys/windows"
)

func TestWindowsSetParametersPreservesUnrelatedDCBFields(t *testing.T) {
	const parityCheck = 1 << 1
	for _, flags := range []uint32{windows.DTR_CONTROL_ENABLE, windows.RTS_CONTROL_ENABLE, 0} {
		before := windows.DCB{
			DCBlength: 28, BaudRate: 9600, ByteSize: 8, Parity: windows.NOPARITY,
			Flags: flags | 1, StopBits: windows.ONESTOPBIT, XonChar: 17, XoffChar: 19,
		}
		got := before
		setWindowsParameters(&got, parameters{baud: 19200, dataBits: 7, parity: serial.EvenParity, stopBits: serial.TwoStopBits})
		want := before
		want.BaudRate, want.ByteSize, want.Parity, want.StopBits = 19200, 7, windows.EVENPARITY, windows.TWOSTOPBITS
		want.Flags |= parityCheck
		if got != want {
			t.Fatalf("DCB with flags %#x = %+v; want %+v", flags, got, want)
		}
		setWindowsParameters(&got, parameters{baud: 9600, dataBits: 8, parity: serial.NoParity, stopBits: serial.OneStopBit})
		if got != before {
			t.Fatalf("DCB with parity disabled = %+v; want %+v", got, before)
		}
	}
}

// Set SERIALGO_TEST_COM_PORT to an unused virtual COM port to test the live
// Win32 configuration on the handle managed by serialport.Open.
func TestWindowsSetParametersOnCOMPort(t *testing.T) {
	name := os.Getenv("SERIALGO_TEST_COM_PORT")
	if name == "" {
		t.Skip("SERIALGO_TEST_COM_PORT is not set")
	}
	previous := Settings{Name: name, Baud: "9600", DataBits: "8", Parity: "none", StopBits: "1", DTR: true}
	p, err := Open(previous)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	handle := windows.Handle(backendHandle(p.conn).Uint())
	readState := func() (windows.DCB, windows.CommTimeouts) {
		t.Helper()
		dcb := windows.DCB{DCBlength: uint32(unsafe.Sizeof(windows.DCB{}))}
		if err := windows.GetCommState(handle, &dcb); err != nil {
			t.Fatal(err)
		}
		var timeouts windows.CommTimeouts
		if err := windows.GetCommTimeouts(handle, &timeouts); err != nil {
			t.Fatal(err)
		}
		return dcb, timeouts
	}
	before, timeouts := readState()
	if before.BaudRate != 9600 || before.StopBits != windows.ONESTOPBIT {
		t.Fatalf("unexpected initial DCB: %+v", before)
	}
	next := previous
	next.Baud, next.StopBits = "19200", "2"
	if err := p.SetParameters(next, previous); err != nil {
		t.Fatal(err)
	}
	after, afterTimeouts := readState()
	if after.BaudRate != 19200 || after.StopBits != windows.TWOSTOPBITS {
		t.Errorf("live parameters not applied: %+v", after)
	}
	const modemControls = 0x3030 // DCB.fDtrControl | DCB.fRtsControl
	if after.Flags&modemControls != before.Flags&modemControls || afterTimeouts != timeouts {
		t.Errorf("live update changed modem line configuration or I/O timeouts: before=%+v/%+v after=%+v/%+v", before, timeouts, after, afterTimeouts)
	}
	if err := p.SetParameters(previous, next); err != nil {
		t.Fatal(err)
	}
	after, _ = readState()
	if after.BaudRate != 9600 || after.StopBits != windows.ONESTOPBIT {
		t.Errorf("original parameters not reapplied: %+v", after)
	}
}

// The peer must be the other end of the virtual pair named by SERIALGO_TEST_COM_PORT.
func TestWindowsLiveControlLinesOnCOMPair(t *testing.T) {
	name, peerName := os.Getenv("SERIALGO_TEST_COM_PORT"), os.Getenv("SERIALGO_TEST_COM_PEER")
	if name == "" || peerName == "" {
		t.Skip("virtual COM pair is not configured")
	}
	previous := Settings{Name: name, Baud: "9600", DataBits: "8", Parity: "none", StopBits: "1"}
	p, err := Open(previous)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	path, err := windows.UTF16PtrFromString(`\\.\` + peerName)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(peer)
	check := func(dtr, rts bool) {
		t.Helper()
		if err := p.SetLines(dtr, rts); err != nil {
			t.Fatal(err)
		}
		var modem uint32
		if err := windows.GetCommModemStatus(peer, &modem); err != nil {
			t.Fatal(err)
		}
		if (modem&0x20 != 0) != dtr || (modem&0x10 != 0) != rts {
			t.Fatalf("peer modem status %#x; want DSR=%t CTS=%t", modem, dtr, rts)
		}
	}
	check(false, false)
	check(true, true)
	next := previous
	next.Baud, next.StopBits = "19200", "2"
	if err := p.SetParameters(next, previous); err != nil {
		t.Fatal(err)
	}
	var modem uint32
	if err := windows.GetCommModemStatus(peer, &modem); err != nil || modem&0x30 != 0x30 {
		t.Fatalf("live parameter update changed modem outputs: status=%#x err=%v", modem, err)
	}
	check(false, false)
}
