// Package serialport opens and operates UART devices on Windows and Unix.
package serialport

import (
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	serial "github.com/albenik/go-serial/v2"
)

// Info describes an available UART. Description is optional display metadata;
// Name alone identifies the device to open.
type Info struct {
	Name, Description string
}

// Settings describes a UART and the initial state of its modem output lines.
type Settings struct {
	Name, Baud, DataBits, Parity, StopBits string
	DTR, RTS                               bool
}

const (
	readTimeoutMS  = 10
	writeTimeoutMS = 250
	retryInterval  = 5 * time.Millisecond
)

// Port is an open UART. It supports simultaneous reads and writes and must not
// be copied. Its zero value is closed.
type Port struct {
	readMu  sync.Mutex
	writeMu sync.Mutex

	// Keep the library handle alive during each operation. The backend's
	// Close must not race with I/O or with changes to modem lines.
	mu   sync.RWMutex
	conn *serial.Port
	fd   int // borrowed Unix descriptor; conn owns it
}

// Open configures the named device and sets its initial modem output lines.
func Open(s Settings) (*Port, error) {
	name := strings.TrimSpace(s.Name)
	if name == "" {
		return nil, errors.New("serial port name is required")
	}
	baud, err := strconv.Atoi(strings.TrimSpace(s.Baud))
	if err != nil || baud <= 0 || uint64(baud) > 1<<32-1 {
		return nil, fmt.Errorf("invalid baud rate %q", s.Baud)
	}
	dataBits, err := strconv.Atoi(strings.TrimSpace(s.DataBits))
	if err != nil || dataBits < 5 || dataBits > 8 {
		return nil, fmt.Errorf("invalid data bits %q (want 5 through 8)", s.DataBits)
	}
	var parity serial.Parity
	switch strings.ToLower(strings.TrimSpace(s.Parity)) {
	case "none":
		parity = serial.NoParity
	case "odd":
		parity = serial.OddParity
	case "even":
		parity = serial.EvenParity
	case "mark":
		parity = serial.MarkParity
	case "space":
		parity = serial.SpaceParity
	default:
		return nil, fmt.Errorf("invalid parity %q", s.Parity)
	}
	var stopBits serial.StopBits
	switch strings.TrimSpace(s.StopBits) {
	case "1":
		stopBits = serial.OneStopBit
	case "1.5":
		stopBits = serial.OnePointFiveStopBits
		if runtime.GOOS != "windows" {
			if dataBits != 5 {
				return nil, errors.New("1.5 stop bits require 5 data bits on Unix")
			}
			// Termios exposes only CSTOPB. With a 5-bit word the
			// long-stop setting selects 1.5 stop bits on UARTs.
			stopBits = serial.TwoStopBits
		}
	case "2":
		stopBits = serial.TwoStopBits
	default:
		return nil, fmt.Errorf("invalid stop bits %q", s.StopBits)
	}

	conn, err := serial.Open(name,
		serial.WithBaudrate(baud), serial.WithDataBits(dataBits),
		serial.WithParity(parity), serial.WithStopBits(stopBits),
		serial.WithWriteTimeout(writeTimeoutMS),
	)
	if err != nil {
		return nil, fmt.Errorf("open serial port %q: %w", name, err)
	}
	p := &Port{conn: conn}
	if runtime.GOOS == "windows" {
		err = conn.SetFirstByteReadTimeout(readTimeoutMS)
	} else {
		p.fd, err = nonblockingTTY(conn, syscall.SetNonblock)
	}
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("configure serial port %q: %w", name, err)
	}
	// Some valid TTYs (e.g. PTYs) do not have modem outputs. They can
	// still be opened with both lines off, but not with a line asserted.
	if err := setLines(conn, s.DTR, s.RTS, !s.DTR && !s.RTS); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("configure serial port %q: %w", name, err)
	}
	return p, nil
}

// v2.6.1 puts Unix descriptors in blocking mode, and its Write calls write(2)
// before checking any timeout. The package has no public descriptor accessor.
// Read the pinned backend's integer descriptor without unsafe or taking over
// ownership; fail closed if its layout changes. Nonblocking syscalls also
// avoid the backend's fixed-size select sets on Unix.
func nonblockingTTY[FD ~int | ~uintptr](conn *serial.Port, setNonblock func(FD, bool) error) (int, error) {
	internal := reflect.ValueOf(conn).Elem().FieldByName("internal")
	if internal.Kind() != reflect.Pointer || internal.IsNil() || internal.Elem().Kind() != reflect.Struct {
		return 0, errors.New("UART backend has no Unix descriptor")
	}
	handle := internal.Elem().FieldByName("handle")
	if handle.Kind() != reflect.Int || handle.Int() < 0 {
		return 0, errors.New("UART backend has no Unix descriptor")
	}
	fd := int(handle.Int())
	return fd, setNonblock(FD(fd), true)
}

// syscall uses different descriptor types on Unix and Windows. Keep the Unix
// call sites in this file without a Windows-only dependency or another file.
func ttyIO[FD ~int | ~uintptr](call func(FD, []byte) (int, error), fd int, b []byte) (int, error) {
	return call(FD(fd), b)
}

func wouldBlock(err error) bool {
	return errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK)
}

// Read returns available data, or (0, nil) after a brief idle wait.
func (p *Port) Read(b []byte) (int, error) {
	p.readMu.Lock()
	defer p.readMu.Unlock()

	if runtime.GOOS == "windows" || len(b) == 0 {
		return p.read(b)
	}
	deadline := time.Now().Add(readTimeoutMS * time.Millisecond)
	for {
		n, err := p.read(b)
		if !wouldBlock(err) && !errors.Is(err, syscall.EINTR) {
			return n, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, nil
		}
		time.Sleep(min(remaining, retryInterval))
	}
}

func (p *Port) read(b []byte) (int, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.conn == nil {
		return 0, os.ErrClosed
	}
	if len(b) == 0 {
		return 0, nil
	}
	if runtime.GOOS == "windows" {
		return p.conn.Read(b)
	}
	n, err := ttyIO(syscall.Read, p.fd, b)
	if err != nil {
		return max(n, 0), err
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

// Write returns the number of bytes accepted by the device. A write that
// cannot finish within the write timeout returns a partial count and an error.
func (p *Port) Write(b []byte) (int, error) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	if runtime.GOOS == "windows" || len(b) == 0 {
		n, err := p.write(b)
		if n < len(b) && err == nil {
			err = io.ErrShortWrite
		}
		return n, err
	}
	deadline := time.Now().Add(writeTimeoutMS * time.Millisecond)
	written := 0
	for {
		n, err := p.write(b[written:])
		written += n
		if written == len(b) || (err != nil && !wouldBlock(err) && !errors.Is(err, syscall.EINTR)) {
			return written, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return written, os.ErrDeadlineExceeded
		}
		if n == 0 {
			time.Sleep(min(remaining, retryInterval))
		}
	}
}

func (p *Port) write(b []byte) (int, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.conn == nil {
		return 0, os.ErrClosed
	}
	if len(b) == 0 {
		return 0, nil
	}
	if runtime.GOOS == "windows" {
		return p.conn.Write(b)
	}
	n, err := ttyIO(syscall.Write, p.fd, b)
	return max(n, 0), err
}

// SetLines sets both modem output lines and reports any driver errors.
func (p *Port) SetLines(dtr, rts bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn == nil {
		return os.ErrClosed
	}
	return setLines(p.conn, dtr, rts, false)
}

func setLines(conn *serial.Port, dtr, rts, allowUnsupported bool) error {
	return errors.Join(
		lineError("DTR", conn.SetDTR(dtr), allowUnsupported),
		lineError("RTS", conn.SetRTS(rts), allowUnsupported),
	)
}

func lineError(name string, err error, allowUnsupported bool) error {
	if err == nil || (allowUnsupported && runtime.GOOS != "windows" && errors.Is(err, syscall.ENOTTY)) {
		return nil
	}
	return fmt.Errorf("set %s: %w", name, err)
}

// Close releases the port. It is safe to call concurrently and more than once.
func (p *Port) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn == nil {
		return nil
	}
	err := p.conn.Close()
	p.conn = nil
	return err
}
