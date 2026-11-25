package serialport

import (
	"errors"
	"reflect"
	"unsafe"

	serial "github.com/albenik/go-serial/v2"
	"github.com/fcying/SerialGo/internal/i18n"
	"golang.org/x/sys/windows"
)

func (p *Port) reconfigure(next, _ parameters) error {
	// The backend's Windows Reconfigure forces RTS on, reapplies timeouts
	// and the event mask, and closes the handle if any operation fails.
	// Borrow its handle under mu and update only the relevant DCB fields.
	h := backendHandle(p.conn)
	var handle windows.Handle
	if h.Kind() == reflect.Uintptr {
		handle = windows.Handle(h.Uint())
	}
	if handle == 0 || handle == windows.InvalidHandle {
		return errors.New("UART backend has no Windows handle")
	}
	previous := windows.DCB{DCBlength: uint32(unsafe.Sizeof(windows.DCB{}))}
	if err := windows.GetCommState(handle, &previous); err != nil {
		return i18n.WrapError("读取当前串口参数失败", "read current serial port parameters", err)
	}
	updated := previous
	setWindowsParameters(&updated, next)
	if err := applyWithRestore(
		func() error { return windows.SetCommState(handle, &updated) },
		func() error { return windows.SetCommState(handle, &previous) },
	); err != nil {
		return err
	}
	// Synchronize the backend cache only after the driver accepts the DCB.
	for _, option := range next.options() {
		option(p.conn)
	}
	return nil
}

func setWindowsParameters(dcb *windows.DCB, mode parameters) {
	const parityCheck = 1 << 1 // DCB.fParity
	parity := [...]uint8{
		serial.NoParity:    windows.NOPARITY,
		serial.OddParity:   windows.ODDPARITY,
		serial.EvenParity:  windows.EVENPARITY,
		serial.MarkParity:  windows.MARKPARITY,
		serial.SpaceParity: windows.SPACEPARITY,
	}
	stopBits := [...]uint8{
		serial.OneStopBit:           windows.ONESTOPBIT,
		serial.OnePointFiveStopBits: windows.ONE5STOPBITS,
		serial.TwoStopBits:          windows.TWOSTOPBITS,
	}
	dcb.BaudRate = uint32(mode.baud)
	dcb.ByteSize = uint8(mode.dataBits)
	dcb.Parity = parity[mode.parity]
	dcb.StopBits = stopBits[mode.stopBits]
	if dcb.Parity == windows.NOPARITY {
		dcb.Flags &^= parityCheck
	} else {
		dcb.Flags |= parityCheck
	}
}
