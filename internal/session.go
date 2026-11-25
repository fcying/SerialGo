package internal

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
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
var errFileNotRegular = i18n.NewError("仅支持发送普通文件", "only regular files are supported")

type Event struct {
	At         time.Time
	Data       []byte
	Sent       bool
	Err        error
	ReadFailed bool
	// SessionEnd marks the end of a serial session in display history.
	SessionEnd bool
	capture    *captureWriter
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

type sessionPort interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	SetParameters(next, previous serialport.Settings) error
	SetLines(bool, bool) error
	Close() error
}

type Session struct {
	port               sessionPort
	ctx                context.Context
	cancel             context.CancelFunc
	events             chan Event
	writeMu            sync.Mutex // guards the write chunk and each write against reconfiguration
	writeChunk         int
	readDone           chan struct{}
	sendMu             sync.Mutex
	settingsMu         sync.Mutex // keeps Close out of the entire settings transaction
	captureMu          sync.Mutex
	captureOps         sync.Mutex
	capture            *captureWriter
	captureWorker      *captureWriter
	captureErrors      map[*captureWriter]error
	displayMu          sync.Mutex
	displayEvents      [displayEventLimit]Event
	displayHead        int
	displayCount       int
	displayBytes       int
	displayRx          uint64
	displayTx          uint64
	fileSendActive     bool
	fileSendBytes      uint64
	fileSendReported   uint64
	fileSendNextUpdate time.Time
	curve              *curveWorker
}

func NewSession(port *serialport.Port, baud int) *Session {
	return newSession(port, baud, nil)
}

func newSession(port sessionPort, baud int, receiver *curveReceiver) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{port: port, ctx: ctx, cancel: cancel, events: make(chan Event, 256), readDone: make(chan struct{})}
	if receiver != nil {
		s.curve = newCurveWorker(receiver)
	}
	s.writeChunk = serialWriteChunk(baud)
	go s.receive()
	return s
}

func serialWriteChunk(baud int) int {
	// A Windows driver can wait for every byte to leave the UART. Keep each
	// write comfortably within the backend's deadline, even at low baud.
	return min(4096, max(1, baud/100))
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
		if s.fileSendActive {
			s.fileSendBytes += uint64(dataBytes)
		}
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

func (s *Session) beginFileSendProgress() {
	s.displayMu.Lock()
	s.fileSendBytes = 0
	s.fileSendReported = 0
	s.fileSendNextUpdate = time.Now().Add(fileSendProgressInterval)
	s.fileSendActive = true
	s.displayMu.Unlock()
}

func (s *Session) fileSendProgressBytes() uint64 {
	s.displayMu.Lock()
	defer s.displayMu.Unlock()
	return s.fileSendBytes
}

func (s *Session) takeFileSendProgress() uint64 {
	s.displayMu.Lock()
	defer s.displayMu.Unlock()
	s.fileSendReported = s.fileSendBytes
	s.fileSendNextUpdate = time.Now().Add(fileSendProgressInterval)
	return s.fileSendBytes
}

func (s *Session) fileSendProgressDue(now time.Time) bool {
	s.displayMu.Lock()
	defer s.displayMu.Unlock()
	return s.fileSendActive && s.fileSendBytes != s.fileSendReported && !now.Before(s.fileSendNextUpdate)
}

func (s *Session) endFileSendProgress() {
	s.displayMu.Lock()
	s.fileSendActive = false
	s.displayMu.Unlock()
}

func (s *Session) hasDisplay() bool {
	s.displayMu.Lock()
	defer s.displayMu.Unlock()
	return s.displayCount != 0 || s.displayRx != 0 || s.displayTx != 0
}

func (s *Session) clearDisplay() {
	s.displayMu.Lock()
	for s.displayCount > 0 {
		s.dropOldestDisplayLocked()
	}
	s.displayHead = 0
	s.displayRx, s.displayTx = 0, 0
	s.displayMu.Unlock()
}

type curveReadState struct {
	epoch   uint64
	enabled bool
}

func (s *Session) curveReadState() curveReadState {
	if s.curve == nil {
		return curveReadState{}
	}
	// Read the epoch first so a concurrent start cannot tag an old read
	// with the newly enabled acquisition.
	return curveReadState{epoch: s.curve.receiver.epoch.Load(), enabled: s.curve.receiver.enabled.Load()}
}

func (s *Session) receive() {
	defer close(s.readDone)
	if s.curve != nil {
		defer close(s.curve.queue)
	}
	buf := make([]byte, receiveBufferSize)
	for s.ctx.Err() == nil {
		// A read spanning a curve transition is conservatively excluded from
		// the new acquisition, even if its bytes arrive after that transition.
		curveState := s.curveReadState()
		n, err := s.port.Read(buf)
		if n > 0 {
			s.receiveChunk(buf[:n], time.Now(), curveState)
		}
		if err != nil {
			s.emit(Event{Err: i18n.WrapError("接收失败", "Receive failed", err), ReadFailed: true})
			return
		}
	}
}

func (s *Session) receiveChunk(chunk []byte, at time.Time, curveState curveReadState) {
	data := append([]byte(nil), chunk...)
	s.enqueueCapture(data)
	s.emit(Event{At: at, Data: data})
	if s.curve != nil {
		s.curve.enqueue(data, at, curveState.epoch, curveState.enabled)
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
		if s.captureErrors == nil {
			s.captureErrors = make(map[*captureWriter]error)
		}
		s.captureErrors[capture] = reportedErr
	}
	s.captureMu.Unlock()
	if reportedErr != nil {
		s.emit(Event{Err: reportedErr, capture: capture})
	}
}

func framingChanged(previous, next serialport.Settings) bool {
	return next.Baud != previous.Baud || next.DataBits != previous.DataBits || next.Parity != previous.Parity || next.StopBits != previous.StopBits
}

// applyPortSettings runs on a worker. No write can see an intermediate
// configuration, and Close cannot interrupt rollback. Take writeMu first so
// Close can still release the driver if an in-flight Write blocks.
func (s *Session) applyPortSettings(previous, next serialport.Settings) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	if s.ctx.Err() != nil {
		return errPortSettingsObsolete
	}
	select {
	case <-s.readDone:
		return errPortSettingsObsolete
	default:
	}

	changeFraming := framingChanged(previous, next)
	chunk := s.writeChunk
	if next.Baud != previous.Baud {
		baud, _ := strconv.Atoi(strings.TrimSpace(next.Baud))
		chunk = serialWriteChunk(baud)
	}
	if changeFraming {
		if err := s.port.SetParameters(next, previous); err != nil {
			var restoreErr *serialport.ParametersRestoreError
			if errors.As(err, &restoreErr) {
				s.writeChunk = min(s.writeChunk, chunk)
			}
			return i18n.WrapError("设置串口参数失败", "Failed to set serial port parameters", err)
		}
	}
	if next.DTR != previous.DTR || next.RTS != previous.RTS {
		if err := s.port.SetLines(next.DTR, next.RTS); err != nil {
			var restoreErr error
			if lineErr := s.port.SetLines(previous.DTR, previous.RTS); lineErr != nil {
				restoreErr = i18n.WrapError("恢复原控制线配置失败", "Failed to restore previous control-line settings", lineErr)
			}
			if changeFraming {
				if paramErr := s.port.SetParameters(previous, next); paramErr != nil {
					restoreErr = errors.Join(restoreErr, i18n.WrapError("恢复原串口参数失败", "Failed to restore previous serial port parameters", paramErr))
					// The active baud may still be the requested one.
					s.writeChunk = min(s.writeChunk, chunk)
				}
			}
			if restoreErr == nil {
				return i18n.WrapError("设置控制线失败, 已重新应用原配置状态", "Failed to set control lines; previous configured state was reapplied", err)
			}
			return i18n.WrapError("设置控制线失败, 硬件状态可能与配置不一致", "Failed to set control lines; hardware state may not match configuration", errors.Join(err, restoreErr))
		}
	}
	s.writeChunk = chunk
	return nil
}

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

func (s *Session) acknowledgeCaptureError(capture *captureWriter) {
	s.captureMu.Lock()
	delete(s.captureErrors, capture)
	s.captureMu.Unlock()
}

func (s *Session) Close() error {
	s.cancel()
	s.settingsMu.Lock()
	err := s.port.Close()
	s.settingsMu.Unlock()
	<-s.readDone
	// Port.Close may return before Write has returned and emitted its byte count.
	// Cancellation prevents later sends from producing any new records.
	s.sendMu.Lock()
	s.sendMu.Unlock()
	err = errors.Join(err, s.SetCapture(nil))
	if s.curve != nil {
		<-s.curve.done
	}
	s.captureMu.Lock()
	var captureErr error
	for _, result := range s.captureErrors {
		captureErr = errors.Join(captureErr, result)
	}
	s.captureErrors = nil
	s.captureMu.Unlock()
	return errors.Join(err, captureErr)
}

// Send is called by one job at a time; partial writes count as sent bytes.
func (s *Session) Send(ctx context.Context, data []byte) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	for len(data) != 0 {
		s.writeMu.Lock()
		at := time.Now()
		n, err := s.write(ctx, data)
		s.writeMu.Unlock()
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

// write requires writeMu to couple the selected chunk size to the active baud.
func (s *Session) write(ctx context.Context, data []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	return s.port.Write(data[:min(s.writeChunk, len(data))])
}

type fileSender interface {
	Send(context.Context, []byte) error
}

func (s *Session) SendFile(ctx context.Context, path string, delay time.Duration) error {
	return sendFile(ctx, path, delay, s)
}

func sendFile(ctx context.Context, path string, delay time.Duration, sender fileSender) error {
	return sendFileWithProgress(ctx, path, delay, sender, nil)
}

func sendFileWithProgress(ctx context.Context, path string, delay time.Duration, sender fileSender, onSize func(int64)) error {
	file, err := os.OpenFile(path, os.O_RDONLY|outputNonblock, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	return sendOpenedFileWithProgress(ctx, file, delay, sender, onSize)
}

func sendOpenedFileWithProgress(ctx context.Context, file *os.File, delay time.Duration, sender fileSender, onSize func(int64)) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errFileNotRegular
	}
	if onSize != nil {
		onSize(info.Size())
	}
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
