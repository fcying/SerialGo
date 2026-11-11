package internal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/fcying/SerialGo/communication/serialport"
)

type Event struct {
	At         time.Time
	Data       []byte
	Sent       bool
	Err        error
	ReadFailed bool
}

type Session struct {
	port       *serialport.Port
	ctx        context.Context
	cancel     context.CancelFunc
	events     chan Event
	writeChunk int
	readDone   chan struct{}
	captureMu  sync.Mutex
	capture    *os.File
}

func NewSession(port *serialport.Port, baud int) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{port: port, ctx: ctx, cancel: cancel, events: make(chan Event, 256), readDone: make(chan struct{})}
	// A Windows driver can wait for every byte to leave the UART. Keep each
	// write comfortably within the backend's deadline, even at low baud.
	s.writeChunk = min(4096, max(1, baud/100))
	go s.receive()
	return s
}

func (s *Session) Events() <-chan Event { return s.events }

func (s *Session) emit(event Event) {
	select {
	case s.events <- event:
	case <-s.ctx.Done():
	}
}

func (s *Session) receive() {
	defer close(s.readDone)
	buf := make([]byte, 4096)
	for s.ctx.Err() == nil {
		n, err := s.port.Read(buf)
		if n > 0 {
			at := time.Now()
			s.captureMu.Lock()
			var captureErr error
			if s.capture != nil {
				_, captureErr = s.capture.Write(buf[:n])
				if captureErr != nil {
					captureErr = errors.Join(captureErr, s.capture.Close())
					s.capture = nil
				}
			}
			s.captureMu.Unlock()
			s.emit(Event{At: at, Data: append([]byte(nil), buf[:n]...)})
			if captureErr != nil {
				s.emit(Event{Err: fmt.Errorf("接收文件写入失败: %w", captureErr)})
			}
		}
		if err != nil {
			s.emit(Event{Err: fmt.Errorf("接收失败: %w", err), ReadFailed: true})
			return
		}
	}
}

func (s *Session) SetLines(dtr, rts bool) error { return s.port.SetLines(dtr, rts) }

// SetCapture takes ownership of file only on success. Passing nil stops capture.
func (s *Session) SetCapture(file *os.File) error {
	s.captureMu.Lock()
	defer s.captureMu.Unlock()
	if s.ctx.Err() != nil {
		return s.ctx.Err()
	}
	if s.capture != nil {
		err := s.capture.Close()
		s.capture = nil
		if err != nil {
			return err
		}
	}
	s.capture = file
	return nil
}

func (s *Session) Close() error {
	s.cancel()
	err := s.port.Close()
	<-s.readDone
	s.captureMu.Lock()
	defer s.captureMu.Unlock()
	if s.capture != nil {
		err = errors.Join(err, s.capture.Close())
		s.capture = nil
	}
	return err
}

// Send is called by one job at a time; partial writes count as sent bytes.
func (s *Session) Send(ctx context.Context, data []byte) error {
	for len(data) != 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.ctx.Err(); err != nil {
			return err
		}
		at := time.Now()
		n, err := s.port.Write(data[:min(s.writeChunk, len(data))])
		if n > 0 {
			s.emit(Event{At: at, Data: append([]byte(nil), data[:n]...), Sent: true})
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func (s *Session) SendFile(ctx context.Context, path string, delay time.Duration) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	buf := make([]byte, 256)
	for {
		n, readErr := file.Read(buf)
		if n > 0 {
			err = s.Send(ctx, buf[:n])
			if err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
		if err = Wait(ctx, delay); err != nil {
			return err
		}
	}
}

func Wait(ctx context.Context, delay time.Duration) error {
	if delay == 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}
