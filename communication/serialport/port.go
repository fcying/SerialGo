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
	"github.com/fcying/SerialGo/internal/i18n"
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

	// Keep the handle alive and its configuration stable during each
	// backend operation. Close and configuration changes take mu exclusively.
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
	mode, err := parseParameters(s)
	if err != nil {
		return nil, err
	}

	conn, err := serial.Open(name,
		serial.WithBaudrate(mode.baud), serial.WithDataBits(mode.dataBits), serial.WithParity(mode.parity),
		serial.WithStopBits(mode.stopBits), serial.WithWriteTimeout(writeTimeoutMS),
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

type parameters struct {
	baud, dataBits int
	parity         serial.Parity
	stopBits       serial.StopBits
}

func (p parameters) options() [4]serial.Option {
	return [4]serial.Option{
		serial.WithBaudrate(p.baud), serial.WithDataBits(p.dataBits),
		serial.WithParity(p.parity), serial.WithStopBits(p.stopBits),
	}
}

func parseParameters(s Settings) (parameters, error) {
	baud, err := strconv.Atoi(strings.TrimSpace(s.Baud))
	if err != nil || baud <= 0 || uint64(baud) > 1<<32-1 {
		return parameters{}, fmt.Errorf("invalid baud rate %q", s.Baud)
	}
	dataBits, err := strconv.Atoi(strings.TrimSpace(s.DataBits))
	if err != nil || dataBits < 5 || dataBits > 8 {
		return parameters{}, fmt.Errorf("invalid data bits %q (want 5 through 8)", s.DataBits)
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
		return parameters{}, fmt.Errorf("invalid parity %q", s.Parity)
	}
	stopBits, err := parseStopBits(s.StopBits, dataBits)
	if err != nil {
		return parameters{}, err
	}
	return parameters{baud: baud, dataBits: dataBits, parity: parity, stopBits: stopBits}, nil
}

func parseStopBits(value string, dataBits int) (serial.StopBits, error) {
	var stopBits serial.StopBits
	switch strings.TrimSpace(value) {
	case "1":
		stopBits = serial.OneStopBit
	case "1.5":
		stopBits = serial.OnePointFiveStopBits
		if runtime.GOOS != "windows" {
			if dataBits != 5 {
				return 0, errors.New("1.5 stop bits require 5 data bits on Unix")
			}
			// Termios exposes only CSTOPB. With a 5-bit word the
			// long-stop setting selects 1.5 stop bits on UARTs.
			stopBits = serial.TwoStopBits
		}
	case "2":
		if runtime.GOOS == "linux" && dataBits == 5 {
			return 0, i18n.NewError(
				"Linux 不支持 5 数据位和 2 停止位的组合；该设置可能产生 1.5 停止位",
				"Linux does not support 5 data bits with 2 stop bits; this setting can produce 1.5 stop bits",
			)
		}
		stopBits = serial.TwoStopBits
	default:
		return 0, fmt.Errorf("invalid stop bits %q", value)
	}

	return stopBits, nil
}

// v2.6.1 puts Unix descriptors in blocking mode, and its Write calls write(2)
// before checking any timeout. The package has no public descriptor accessor.
// Read the pinned backend's integer descriptor without unsafe or taking over
// ownership; fail closed if its layout changes. Nonblocking syscalls also
// avoid the backend's fixed-size select sets on Unix.
func nonblockingTTY[FD ~int | ~uintptr](conn *serial.Port, setNonblock func(FD, bool) error) (int, error) {
	handle := backendHandle(conn)
	if handle.Kind() != reflect.Int || handle.Int() < 0 {
		return 0, errors.New("UART backend has no Unix descriptor")
	}
	fd := int(handle.Int())
	return fd, setNonblock(FD(fd), true)
}

func backendHandle(conn *serial.Port) reflect.Value {
	internal := reflect.ValueOf(conn).Elem().FieldByName("internal")
	if internal.Kind() != reflect.Pointer || internal.IsNil() || internal.Elem().Kind() != reflect.Struct {
		return reflect.Value{}
	}
	return internal.Elem().FieldByName("handle")
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

// SetParameters changes baud, data bits, parity and stop bits without reopening
// the port or changing its modem line settings. previous must describe the last
// applied parameters so a failed change can be rolled back. The device name and
// modem line fields in next and previous are ignored.
//
// This does not drain driver queues; callers should quiesce the link if bytes
// must not cross a framing change.
func (p *Port) SetParameters(next, previous Settings) error {
	// Wait for whole I/O calls, not just individual syscalls: in particular,
	// a Linux Write may retry and must not submit bytes in two modes.
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	p.readMu.Lock()
	defer p.readMu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn == nil {
		return os.ErrClosed
	}
	nextMode, err := parseParameters(next)
	if err != nil {
		return err
	}
	previousMode, err := parseParameters(previous)
	if err != nil {
		return i18n.WrapError("原串口参数无效", "invalid previous serial port parameters", err)
	}
	return p.reconfigure(nextMode, previousMode)
}

// ParametersRestoreError reports that recovery failed after a parameter change
// was rejected. The live parameters may not match the previous settings.
type ParametersRestoreError struct {
	Err error
}

func (e *ParametersRestoreError) Error() string { return e.Err.Error() }
func (e *ParametersRestoreError) Unwrap() error { return e.Err }

func applyWithRestore(apply, restore func() error) error {
	err := apply()
	if err == nil {
		return nil
	}
	err = i18n.WrapError("应用串口参数失败", "apply serial port parameters", err)
	if restoreErr := restore(); restoreErr != nil {
		restoreErr = i18n.WrapError("恢复原串口参数失败", "restore previous serial port parameters", restoreErr)
		return errors.Join(err, &ParametersRestoreError{Err: restoreErr})
	}
	return err
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
