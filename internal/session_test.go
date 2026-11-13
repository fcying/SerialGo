package internal

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

const captureTestTimeout = 5 * time.Second

type fileSendTestContext struct {
	context.Context
	errChecks int
}

func (c *fileSendTestContext) Err() error {
	c.errChecks++
	return nil
}

type fileSendTestSender struct {
	data []byte
}

func (s *fileSendTestSender) Send(_ context.Context, data []byte) error {
	s.data = append(s.data, data...)
	return nil
}

func TestSendFileWaitsOnlyBetweenChunks(t *testing.T) {
	want := bytes.Repeat([]byte{0x5A}, 257)
	path := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(path, want, 0600); err != nil {
		t.Fatal(err)
	}
	ctx := &fileSendTestContext{Context: context.Background()}
	sender := &fileSendTestSender{}
	if err := sendFile(ctx, path, 0, sender); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sender.data, want) {
		t.Fatalf("sent data = % x, want % x", sender.data, want)
	}
	if ctx.errChecks != 1 {
		t.Fatalf("inter-chunk cancellation checks = %d, want 1", ctx.errChecks)
	}
}

type captureTestFile struct {
	mu             sync.Mutex
	data           []byte
	writeErr       error
	writeStarted   chan struct{}
	writeCompleted chan struct{}
	blockWrite     chan struct{}
	closed         bool
}

func (f *captureTestFile) Write(data []byte) (int, error) {
	if f.writeStarted != nil {
		select {
		case f.writeStarted <- struct{}{}:
		default:
		}
	}
	if f.blockWrite != nil {
		<-f.blockWrite
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		if f.writeCompleted != nil {
			select {
			case f.writeCompleted <- struct{}{}:
			default:
			}
		}
		return 0, f.writeErr
	}
	f.data = append(f.data, data...)
	if f.writeCompleted != nil {
		select {
		case f.writeCompleted <- struct{}{}:
		default:
		}
	}
	return len(data), nil
}

func (f *captureTestFile) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

func (f *captureTestFile) snapshot() ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.data...), f.closed
}

func awaitSessionCapture(t *testing.T, s *Session) {
	t.Helper()
	timer := time.NewTimer(captureTestTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		s.captureMu.Lock()
		active := s.capture != nil
		s.captureMu.Unlock()
		if active {
			return
		}
		select {
		case <-timer.C:
			t.Fatal("capture worker did not start")
		case <-ticker.C:
		}
	}
}

func TestCaptureBufferFlushesWhenFull(t *testing.T) {
	file := &captureTestFile{}
	writer := bufio.NewWriterSize(file, captureBufferSize)
	prefix := []byte("buffered")
	if err := writeCaptureChunk(writer, prefix); err != nil {
		t.Fatal(err)
	}
	if got, _ := file.snapshot(); len(got) != 0 {
		t.Fatal("partial buffer was written before it filled")
	}
	rest := bytes.Repeat([]byte{0x5A}, captureBufferSize-len(prefix))
	if err := writeCaptureChunk(writer, rest); err != nil {
		t.Fatal(err)
	}
	got, closed := file.snapshot()
	want := append([]byte("buffered"), rest...)
	if !bytes.Equal(got, want) || closed {
		t.Fatal("full buffer was not immediately written")
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSessionCaptureFlushesPeriodicallyAndOnStop(t *testing.T) {
	file := &captureTestFile{writeCompleted: make(chan struct{}, 2)}
	s := &Session{ctx: context.Background(), events: make(chan Event, 2)}
	if err := s.setCapture(file); err != nil {
		t.Fatal(err)
	}
	s.enqueueCapture([]byte("periodic"))
	select {
	case <-file.writeCompleted:
	case <-time.After(3 * captureFlushInterval):
		t.Fatal("buffered data was not flushed on the timer")
	}
	s.enqueueCapture([]byte("stop"))
	if err := s.SetCapture(nil); err != nil {
		t.Fatal(err)
	}
	got, closed := file.snapshot()
	if string(got) != "periodicstop" || !closed {
		t.Fatalf("stop did not flush and close capture: data=%q closed=%v", got, closed)
	}
}

func TestSessionCaptureQueueDoesNotBlockAndReportsOverflow(t *testing.T) {
	file := &captureTestFile{
		writeStarted: make(chan struct{}, 1),
		blockWrite:   make(chan struct{}),
	}
	s := &Session{ctx: context.Background(), events: make(chan Event, 2)}
	if err := s.setCapture(file); err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte{0x5A}, receiveBufferSize)
	for range captureBufferSize / receiveBufferSize {
		s.enqueueCapture(chunk)
	}
	select {
	case <-file.writeStarted:
	case <-time.After(captureTestTimeout):
		t.Fatal("writer did not reach the full-buffer write")
	}

	queueChunk := bytes.Repeat([]byte{0x5A}, 1024)
	enqueueDone := make(chan struct{})
	go func() {
		for range captureQueueBufferSize / len(queueChunk) {
			s.enqueueCapture(queueChunk)
		}
		s.enqueueCapture(queueChunk)
		close(enqueueDone)
	}()
	select {
	case <-enqueueDone:
	case <-time.After(time.Second):
		t.Fatal("receive-side queueing blocked behind file I/O")
	}
	s.captureMu.Lock()
	active := s.capture != nil
	s.captureMu.Unlock()
	if active {
		t.Fatal("queue overflow did not stop capture")
	}

	close(file.blockWrite)
	var event Event
	select {
	case event = <-s.Events():
	case <-time.After(captureTestTimeout):
		t.Fatal("queue overflow was not reported")
	}
	if !errors.Is(event.Err, errCaptureQueueFull) || event.ReadFailed {
		t.Fatalf("queue overflow event = %+v", event)
	}
	if err := s.SetCapture(nil); err != nil {
		t.Fatal(err)
	}
	got, closed := file.snapshot()
	want := bytes.Repeat([]byte{0x5A}, captureBufferSize+captureQueueBufferSize)
	if !bytes.Equal(got, want) || !closed {
		t.Fatalf("overflow did not drain queued bytes and close capture: bytes=%d closed=%v", len(got), closed)
	}
	s.acknowledgeCaptureError()
}

func TestCaptureQueueTracksBytesAcrossWraparound(t *testing.T) {
	capture := &captureWriter{
		queue: make([]byte, 8),
		wake:  make(chan struct{}, 1),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	s := &Session{capture: capture}
	s.enqueueCapture([]byte("abcdef"))

	first := make([]byte, 4)
	if n := s.dequeueCapture(capture, first); n != len(first) || string(first) != "abcd" {
		t.Fatalf("first queue read = %q (%d bytes)", first, n)
	}
	s.enqueueCapture([]byte("ghijkl"))
	remaining := make([]byte, 8)
	if n := s.dequeueCapture(capture, remaining); n != len(remaining) || string(remaining) != "efghijkl" {
		t.Fatalf("wrapped queue read = %q (%d bytes)", remaining, n)
	}
	if capture.queueSize != 0 {
		t.Fatalf("queued bytes after drain = %d, want 0", capture.queueSize)
	}
	s.enqueueCapture([]byte("123456789"))
	if s.capture != nil || capture.queueSize != len(capture.queue) || !errors.Is(capture.failure, errCaptureQueueFull) {
		t.Fatal("overflow did not fill available byte capacity before stopping")
	}
	full := make([]byte, len(capture.queue))
	if n := s.dequeueCapture(capture, full); n != len(full) || string(full) != "12345678" {
		t.Fatalf("queue-full tail = %q (%d bytes)", full, n)
	}
}

func TestSessionDisplayQueueDropsOldestAtEventLimit(t *testing.T) {
	s := &Session{}
	for i := range displayEventLimit + 1 {
		s.emit(Event{Data: []byte{byte(i)}})
	}
	events, rx, tx := s.drainDisplay()
	if len(events) != displayEventLimit || rx != displayEventLimit+1 || tx != 0 {
		t.Fatalf("event-limited display queue = %d events, RX=%d TX=%d", len(events), rx, tx)
	}
	if events[0].Data[0] != 1 || events[len(events)-1].Data[0] != 0 {
		t.Fatal("display queue did not retain the newest events in order")
	}
}

func TestSessionDisplayQueueCopiesOversizedEventTail(t *testing.T) {
	data := make([]byte, displayBufferSize+1)
	data[0], data[1] = 0x11, 0x22
	s := &Session{}
	s.emit(Event{Data: data})
	events, rx, tx := s.drainDisplay()
	if len(events) != 1 || len(events[0].Data) != displayBufferSize || events[0].Data[0] != 0x22 || rx != displayBufferSize+1 || tx != 0 {
		t.Fatal("oversized display event was not trimmed to its newest bytes")
	}
	if &events[0].Data[0] == &data[1] {
		t.Fatal("display queue retained the oversized event's backing array")
	}
}

func TestSessionDisplayQueueConcurrentEnqueueAndDrain(t *testing.T) {
	const producers = 4
	const eventsPerProducer = 1000
	s := &Session{}
	start := make(chan struct{})
	done := make(chan struct{})
	var producersDone sync.WaitGroup
	for producer := range producers {
		producersDone.Add(1)
		go func(producer int) {
			defer producersDone.Done()
			<-start
			for i := range eventsPerProducer {
				s.emit(Event{Data: []byte{byte(producer)}, Sent: i%2 == 0})
			}
		}(producer)
	}
	go func() {
		producersDone.Wait()
		close(done)
	}()
	close(start)
	var totalRx, totalTx uint64
	for {
		_, rx, tx := s.drainDisplay()
		totalRx += rx
		totalTx += tx
		select {
		case <-done:
			_, rx, tx = s.drainDisplay()
			totalRx += rx
			totalTx += tx
			want := uint64(producers * eventsPerProducer / 2)
			if totalRx != want || totalTx != want || s.hasDisplay() {
				t.Fatalf("concurrent display totals = RX %d, TX %d", totalRx, totalTx)
			}
			return
		default:
			runtime.Gosched()
		}
	}
}

func TestDisplayQueueOverflowDoesNotAffectCapture(t *testing.T) {
	const chunks = displayBufferSize/receiveBufferSize + 8
	file := &captureTestFile{writeCompleted: make(chan struct{}, 8)}
	s := &Session{ctx: context.Background(), events: make(chan Event, 2)}
	if err := s.setCapture(file); err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte{0x5A}, receiveBufferSize)
	for i := range chunks {
		s.enqueueCapture(chunk)
		s.emit(Event{Data: chunk})
		if (i+1)%(captureBufferSize/receiveBufferSize) == 0 {
			select {
			case <-file.writeCompleted:
			case <-time.After(captureTestTimeout):
				t.Fatal("capture writer did not keep pace with display overflow test")
			}
		}
	}
	if err := s.SetCapture(nil); err != nil {
		t.Fatal(err)
	}
	data, closed := file.snapshot()
	want := bytes.Repeat([]byte{0x5A}, chunks*receiveBufferSize)
	if !bytes.Equal(data, want) || !closed {
		t.Fatalf("UI display overflow affected DAT capture: bytes=%d closed=%v", len(data), closed)
	}
	events, rx, tx := s.drainDisplay()
	if len(events) != displayBufferSize/receiveBufferSize || rx != uint64(chunks*receiveBufferSize) || tx != 0 {
		t.Fatalf("UI queue retained %d events and counted RX=%d TX=%d", len(events), rx, tx)
	}
}
