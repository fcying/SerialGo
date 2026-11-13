package internal

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"github.com/fcying/SerialGo/communication/serialport"
	"github.com/fcying/SerialGo/internal/i18n"
)

// Bound queued payload so slow storage cannot block serial reads or grow memory without limit.
const (
	receiveBufferSize      = 4 * 1024
	captureBufferSize      = 128 * 1024
	captureQueueBufferSize = 512 * 1024
	captureFlushInterval   = time.Second
	displayBufferSize      = 512 * 1024
	displayEventLimit      = 512
)

var errCaptureQueueFull = errors.New("receive recording queue is full")

type Event struct {
	At         time.Time
	Data       []byte
	Sent       bool
	Err        error
	ReadFailed bool
}

type captureWriter struct {
	file      io.WriteCloser
	queue     []byte
	queueHead int
	queueSize int
	wake      chan struct{}
	stop      chan struct{}
	done      chan struct{}
	failure   error
	result    error
	stopped   bool
	reported  bool
}

type Session struct {
	port          *serialport.Port
	ctx           context.Context
	cancel        context.CancelFunc
	events        chan Event
	writeChunk    int
	readDone      chan struct{}
	captureMu     sync.Mutex
	captureOps    sync.Mutex
	capture       *captureWriter
	captureWorker *captureWriter
	captureError  error
	displayMu     sync.Mutex
	displayEvents [displayEventLimit]Event
	displayHead   int
	displayCount  int
	displayBytes  int
	displayRx     uint64
	displayTx     uint64
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
	if event.Err == nil {
		s.enqueueDisplay(event)
		return
	}
	select {
	case s.events <- event:
	case <-s.ctx.Done():
	}
}

func (s *Session) enqueueDisplay(event Event) {
	dataBytes := len(event.Data)
	if dataBytes == 0 {
		return
	}
	s.displayMu.Lock()
	if event.Sent {
		s.displayTx += uint64(dataBytes)
	} else {
		s.displayRx += uint64(dataBytes)
	}
	if dataBytes > displayBufferSize {
		event.Data = append([]byte(nil), event.Data[dataBytes-displayBufferSize:]...)
		dataBytes = displayBufferSize
	}
	for s.displayCount > 0 && (s.displayCount >= displayEventLimit || s.displayBytes+dataBytes > displayBufferSize) {
		s.dropOldestDisplayLocked()
	}
	tail := (s.displayHead + s.displayCount) % displayEventLimit
	s.displayEvents[tail] = event
	s.displayCount++
	s.displayBytes += dataBytes
	s.displayMu.Unlock()
}

func (s *Session) dropOldestDisplayLocked() {
	event := &s.displayEvents[s.displayHead]
	s.displayBytes -= len(event.Data)
	*event = Event{}
	s.displayHead = (s.displayHead + 1) % displayEventLimit
	s.displayCount--
}

func (s *Session) drainDisplay() ([]Event, uint64, uint64) {
	s.displayMu.Lock()
	events := make([]Event, s.displayCount)
	for i := range events {
		index := (s.displayHead + i) % displayEventLimit
		events[i] = s.displayEvents[index]
		s.displayEvents[index] = Event{}
	}
	rx, tx := s.displayRx, s.displayTx
	s.displayHead, s.displayCount, s.displayBytes = 0, 0, 0
	s.displayRx, s.displayTx = 0, 0
	s.displayMu.Unlock()
	return events, rx, tx
}

func (s *Session) hasDisplay() bool {
	s.displayMu.Lock()
	defer s.displayMu.Unlock()
	return s.displayCount != 0 || s.displayRx != 0 || s.displayTx != 0
}

func (s *Session) clearDisplayCounters() {
	s.displayMu.Lock()
	s.displayRx, s.displayTx = 0, 0
	s.displayMu.Unlock()
}

func (s *Session) receive() {
	defer close(s.readDone)
	buf := make([]byte, receiveBufferSize)
	for s.ctx.Err() == nil {
		n, err := s.port.Read(buf)
		if n > 0 {
			at := time.Now()
			data := append([]byte(nil), buf[:n]...)
			s.enqueueCapture(data)
			s.emit(Event{At: at, Data: data})
		}
		if err != nil {
			s.emit(Event{Err: i18n.WrapError("接收失败", "Receive failed", err), ReadFailed: true})
			return
		}
	}
}

func (s *Session) enqueueCapture(data []byte) {
	s.captureMu.Lock()
	capture := s.capture
	if capture == nil {
		s.captureMu.Unlock()
		return
	}
	available := len(capture.queue) - capture.queueSize
	accepted := min(len(data), available)
	tail := (capture.queueHead + capture.queueSize) % len(capture.queue)
	first := min(accepted, len(capture.queue)-tail)
	copy(capture.queue[tail:tail+first], data[:first])
	copy(capture.queue[:accepted-first], data[first:accepted])
	capture.queueSize += accepted
	if accepted != len(data) {
		s.capture = nil
		capture.failure = errCaptureQueueFull
		capture.stopped = true
		close(capture.stop)
		s.captureMu.Unlock()
		return
	}
	s.captureMu.Unlock()
	select {
	case capture.wake <- struct{}{}:
	default:
	}
}

func (s *Session) dequeueCapture(capture *captureWriter, dst []byte) int {
	s.captureMu.Lock()
	defer s.captureMu.Unlock()
	n := min(capture.queueSize, len(dst))
	if n == 0 {
		return 0
	}
	first := min(n, len(capture.queue)-capture.queueHead)
	copy(dst[:first], capture.queue[capture.queueHead:capture.queueHead+first])
	copy(dst[first:n], capture.queue[:n-first])
	capture.queueHead = (capture.queueHead + n) % len(capture.queue)
	capture.queueSize -= n
	return n
}

func (s *Session) runCapture(capture *captureWriter) {
	defer close(capture.done)
	ticker := time.NewTicker(captureFlushInterval)
	defer ticker.Stop()
	writer := bufio.NewWriterSize(capture.file, captureBufferSize)
	scratch := make([]byte, receiveBufferSize)
	for {
		n := s.dequeueCapture(capture, scratch)
		if n > 0 {
			if err := writeCaptureChunk(writer, scratch[:n]); err != nil {
				s.finishCapture(capture, writer, err)
				return
			}
			select {
			case <-ticker.C:
				if err := writer.Flush(); err != nil {
					s.finishCapture(capture, writer, err)
					return
				}
			default:
			}
			continue
		}
		select {
		case <-capture.wake:
		case <-ticker.C:
			if err := writer.Flush(); err != nil {
				s.finishCapture(capture, writer, err)
				return
			}
		case <-capture.stop:
			for {
				n := s.dequeueCapture(capture, scratch)
				if n == 0 {
					break
				}
				if err := writeCaptureChunk(writer, scratch[:n]); err != nil {
					s.finishCapture(capture, writer, err)
					return
				}
			}
			s.finishCapture(capture, writer, nil)
			return
		}
	}
}

func writeCaptureChunk(writer *bufio.Writer, data []byte) error {
	if _, err := writer.Write(data); err != nil {
		return err
	}
	if writer.Buffered() == writer.Size() {
		return writer.Flush()
	}
	return nil
}

func (s *Session) finishCapture(capture *captureWriter, writer *bufio.Writer, writeErr error) {
	s.captureMu.Lock()
	report := !capture.stopped || capture.failure != nil
	if s.capture == capture {
		s.capture = nil
	}
	if !capture.stopped {
		capture.stopped = true
		close(capture.stop)
	}
	failure := capture.failure
	s.captureMu.Unlock()

	if writeErr == nil {
		writeErr = writer.Flush()
	}
	result := errors.Join(failure, writeErr, capture.file.Close())
	var reportedErr error
	if report && result != nil {
		if errors.Is(failure, errCaptureQueueFull) {
			reportedErr = i18n.WrapError("接收文件写入队列已满, 已停止录制", "Receive recording queue is full; recording stopped", result)
		} else {
			reportedErr = i18n.WrapError("接收文件写入失败", "Receive file write failed", result)
		}
	}

	s.captureMu.Lock()
	capture.queue = nil
	capture.queueHead = 0
	capture.queueSize = 0
	capture.result = result
	if reportedErr != nil {
		capture.reported = true
		s.captureError = errors.Join(s.captureError, reportedErr)
	}
	s.captureMu.Unlock()
	if reportedErr != nil {
		s.emit(Event{Err: reportedErr})
	}
}

func (s *Session) SetLines(dtr, rts bool) error { return s.port.SetLines(dtr, rts) }

// SetCapture takes ownership of file only on success. Passing nil flushes and closes the active capture.
func (s *Session) SetCapture(file *os.File) error {
	if file == nil {
		return s.setCapture(nil)
	}
	return s.setCapture(file)
}

func (s *Session) setCapture(file io.WriteCloser) error {
	s.captureOps.Lock()
	defer s.captureOps.Unlock()
	if err := s.stopCaptureLocked(); err != nil {
		return err
	}
	if file == nil {
		return nil
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	capture := &captureWriter{
		file:  file,
		queue: make([]byte, captureQueueBufferSize),
		wake:  make(chan struct{}, 1),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	s.captureMu.Lock()
	if err := s.ctx.Err(); err != nil {
		s.captureMu.Unlock()
		return err
	}
	s.capture = capture
	s.captureWorker = capture
	s.captureMu.Unlock()
	go s.runCapture(capture)
	return nil
}

// stopCaptureLocked requires captureOps to serialize capture lifecycle changes.
func (s *Session) stopCaptureLocked() error {
	s.captureMu.Lock()
	capture := s.captureWorker
	if capture == nil {
		s.captureMu.Unlock()
		return nil
	}
	if s.capture == capture {
		s.capture = nil
	}
	if !capture.stopped {
		capture.stopped = true
		close(capture.stop)
	}
	s.captureMu.Unlock()

	<-capture.done

	s.captureMu.Lock()
	if s.captureWorker == capture {
		s.captureWorker = nil
	}
	result, reported := capture.result, capture.reported
	s.captureMu.Unlock()
	if reported {
		return nil
	}
	return result
}

func (s *Session) acknowledgeCaptureError() {
	s.captureMu.Lock()
	s.captureError = nil
	s.captureMu.Unlock()
}

func (s *Session) Close() error {
	s.cancel()
	err := s.port.Close()
	<-s.readDone
	err = errors.Join(err, s.SetCapture(nil))
	s.captureMu.Lock()
	captureErr := s.captureError
	s.captureError = nil
	s.captureMu.Unlock()
	return errors.Join(err, captureErr)
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

type fileSender interface {
	Send(context.Context, []byte) error
}

func (s *Session) SendFile(ctx context.Context, path string, delay time.Duration) error {
	return sendFile(ctx, path, delay, s)
}

func sendFile(ctx context.Context, path string, delay time.Duration, sender fileSender) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	buf := make([]byte, 256)
	next := make([]byte, 256)
	n, readErr := file.Read(buf)
	for {
		if n == 0 {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			if readErr != nil {
				return readErr
			}
			if err = Wait(ctx, delay); err != nil {
				return err
			}
			n, readErr = file.Read(buf)
			continue
		}
		if err := sender.Send(ctx, buf[:n]); err != nil {
			return err
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
		nextN, nextErr := file.Read(next)
		waited := false
		for nextN == 0 && nextErr == nil {
			if err = Wait(ctx, delay); err != nil {
				return err
			}
			waited = true
			nextN, nextErr = file.Read(next)
		}
		if nextN == 0 && errors.Is(nextErr, io.EOF) {
			return nil
		}
		if nextErr != nil && !errors.Is(nextErr, io.EOF) {
			return nextErr
		}
		if !waited {
			if err = Wait(ctx, delay); err != nil {
				return err
			}
		}
		buf, next = next, buf
		n, readErr = nextN, nextErr
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
