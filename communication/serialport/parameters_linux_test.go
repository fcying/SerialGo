package serialport

import (
	"strconv"
	"strings"
	"testing"

	serial "github.com/albenik/go-serial/v2"
	"github.com/fcying/SerialGo/internal/i18n"
	"golang.org/x/sys/unix"
)

func TestLinuxSetParametersChangesBaudAndStopBitsOnOpenTTY(t *testing.T) {
	master, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Skipf("pseudo-terminals unavailable: %v", err)
	}
	defer unix.Close(master)
	if err := unix.IoctlSetPointerInt(master, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(master, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	previous := Settings{Name: "/dev/pts/" + strconv.Itoa(number), Baud: "9600", DataBits: "8", Parity: "none", StopBits: "1"}
	p, err := Open(previous)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	conn, fd := p.conn, p.fd
	before, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}

	// Reject invalid framing before applying any of the proposed changes.
	next := Settings{Baud: " 19200 ", DataBits: "5", Parity: " NONE ", StopBits: "2"}
	err = p.SetParameters(next, previous)
	if got := i18n.ErrorText(err, true); !strings.Contains(got, "5 data bits with 2 stop bits") {
		t.Fatalf("SetParameters(5 data bits) error = %q; want Linux stop-bit validation", got)
	}
	after, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	if *after != *before {
		t.Fatalf("invalid parameters changed the terminal: before=%+v, after=%+v", before, after)
	}

	next.Name = "ignored"
	next.DataBits = " 8 "
	next.DTR, next.RTS = true, true // PTYs do not have these lines.
	if err := p.SetParameters(next, previous); err != nil {
		t.Fatal(err)
	}
	if p.conn != conn || p.fd != fd {
		t.Fatal("SetParameters replaced the open device")
	}
	after, err = unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := after.Cflag&(unix.CBAUD|unix.CSTOPB), uint32(unix.B19200|unix.CSTOPB); got != want {
		t.Errorf("baud/stop flags = %#x; want %#x", got, want)
	}
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if flags&unix.O_NONBLOCK == 0 {
		t.Fatal("SetParameters made the descriptor blocking")
	}
	if err := p.SetParameters(previous, next); err != nil {
		t.Fatal(err)
	}
	after, err = unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := after.Cflag&(unix.CBAUD|unix.CSTOPB), before.Cflag&(unix.CBAUD|unix.CSTOPB); got != want {
		t.Errorf("reapplied original baud/stop flags = %#x; want %#x", got, want)
	}
}

func TestLinuxLiveOnePointFiveStopBitsRequireFiveDataBits(t *testing.T) {
	s := Settings{Baud: "9600", Parity: "none", DataBits: "5", StopBits: "1.5"}
	if mode, err := parseParameters(s); err != nil || mode.stopBits != serial.TwoStopBits {
		t.Fatalf("five data bits with 1.5 stop bits: %v", err)
	}
	s.DataBits = "6"
	if _, err := parseParameters(s); err == nil || !strings.Contains(err.Error(), "1.5 stop bits require 5 data bits") {
		t.Fatalf("six data bits with 1.5 stop bits: %v", err)
	}
}
