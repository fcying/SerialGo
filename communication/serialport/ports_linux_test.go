package serialport

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLinuxEnumerationDoesNotReconfigureTTY(t *testing.T) {
	master, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Skipf("pseudo-terminals unavailable: %v", err)
	}
	defer unix.Close(master)
	err = unix.IoctlSetPointerInt(master, unix.TIOCSPTLCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(master, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	device := "/dev/pts/" + strconv.Itoa(number)
	slave, err := unix.Open(device, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(slave)
	settings, err := unix.IoctlGetTermios(slave, unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	settings.Cflag = settings.Cflag&^unix.CBAUD | unix.B19200
	settings.Lflag |= unix.ICANON | unix.ECHO
	err = unix.IoctlSetTermios(slave, unix.TCSETS, settings)
	if err != nil {
		t.Fatal(err)
	}
	before, err := unix.IoctlGetTermios(slave, unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "ttyS123")
	err = os.Symlink(device, path)
	if err != nil {
		t.Fatal(err)
	}
	ports, err := listLinuxPorts(directory)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ports, []Info{{Name: path}}) {
		t.Fatalf("open terminal was not discovered: %+v", ports)
	}
	after, err := unix.IoctlGetTermios(slave, unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("enumeration changed another owner's terminal: before=%+v after=%+v", before, after)
	}
}

func TestLinuxEnumerationFiltersDeviceNodes(t *testing.T) {
	directory := t.TempDir()
	names := []string{
		"rfcomm4", "ttyACM0", "ttyAMA1", "ttyLP0", "ttyO2", "ttyPS0",
		"ttyS2", "ttySAC0", "ttySTM0", "ttyTHS0", "ttyUSB1234", "ttymxc0",
	}
	want := make([]Info, len(names))
	for i, name := range names {
		path := filepath.Join(directory, name)
		err := os.Symlink("/dev/null", path)
		if err != nil {
			t.Fatal(err)
		}
		want[i] = Info{Name: path}
	}
	for _, name := range []string{
		"ttyUSB", "ttySx", "prefix-ttyS1", "ttyACM1-backup",
		"ttyTHS", "ttyTHS-1", "ttyTHS0-backup", "ttyPSx", "ttyPS0x",
		"tty", "tty0", "tty63", "console", "ptmx", "pts0",
	} {
		err := os.Symlink("/dev/null", filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"ttyS3", "ttyTHS1", "ttyPS1"} {
		err := os.WriteFile(filepath.Join(directory, name), []byte("not a device"), 0600)
		if err != nil {
			t.Fatal(err)
		}
	}
	err := os.Mkdir(filepath.Join(directory, "ttyS4"), 0700)
	if err != nil {
		t.Fatal(err)
	}
	err = os.Symlink(filepath.Join(directory, "absent"), filepath.Join(directory, "ttyS5"))
	if err != nil {
		t.Fatal(err)
	}
	ports, err := listLinuxPorts(directory)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ports, want) {
		t.Fatalf("enumerated paths = %+v, want %+v", ports, want)
	}
	_, err = listLinuxPorts(filepath.Join(directory, "absent"))
	if !os.IsNotExist(err) {
		t.Fatalf("missing device directory error was lost: %v", err)
	}
}
