package internal

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
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
	var total int64
	if err := sendFileWithProgress(ctx, path, 0, sender, func(size int64) { total = size }); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sender.data, want) {
		t.Fatalf("sent data = % x, want % x", sender.data, want)
	}
	if ctx.errChecks != 1 {
		t.Fatalf("inter-chunk cancellation checks = %d, want 1", ctx.errChecks)
	}
	if total != int64(len(want)) {
		t.Fatalf("reported file size = %d, want %d", total, len(want))
	}
}

func TestSendFileUsesOpenedFileAfterPathReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows may prevent renaming an open file")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "payload.bin")
	original := []byte("original content")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	sender := &fileSendTestSender{}
	var reported int64
	err := sendFileWithProgress(context.Background(), path, 0, sender, func(size int64) {
		reported = size
		if err := os.Rename(path, filepath.Join(dir, "old.bin")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
			t.Fatal(err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if reported != int64(len(original)) || !bytes.Equal(sender.data, original) {
		t.Fatalf("size = %d, sent = %q; want size %d, content %q", reported, sender.data, len(original), original)
	}
}

type fileProgressTestPort struct{ writes int }

func (p *fileProgressTestPort) Read([]byte) (int, error) { return 0, io.EOF }

func (p *fileProgressTestPort) Write(data []byte) (int, error) {
	p.writes++
	if p.writes == 1 {
		return min(2, len(data)), nil
	}
	return 1, errors.New("write failed")
}

func (p *fileProgressTestPort) SetLines(bool, bool) error { return nil }

func (p *fileProgressTestPort) Close() error { return nil }

func TestFileSendProgressCountsSuccessfulWritesAcrossClear(t *testing.T) {
	s := &Session{port: &fileProgressTestPort{}, ctx: context.Background(), writeChunk: 5}
	s.beginFileSendProgress()
	err := s.Send(context.Background(), []byte("abcde"))
	if err == nil {
		t.Fatal("partial write failure was ignored")
	}
	if got := s.fileSendProgressBytes(); got != 3 {
		t.Fatalf("file progress = %d, want 3 successfully written bytes", got)
	}
	s.clearDisplay()
	_, _, tx := s.drainDisplay()
	if tx != 0 || s.fileSendProgressBytes() != 3 {
		t.Fatalf("clear reset cumulative TX and file progress incorrectly: TX=%d file=%d", tx, s.fileSendProgressBytes())
	}
	s.endFileSendProgress()
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
	s.acknowledgeCaptureError(event.capture)
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

// barrierSessionPort can complete Close while its final Read and Write are still
// outstanding. No real serial device or timing-dependent producer is involved.
type barrierSessionPort struct {
	readStarted  chan struct{}
	writeStarted chan struct{}
	closeStarted chan struct{}
	releaseRead  chan struct{}
	releaseWrite chan struct{}
	readData     []byte
	writeErr     error
	closeErr     error
	closeOnce    sync.Once
}

func newBarrierSessionPort() *barrierSessionPort {
	return &barrierSessionPort{
		readStarted: make(chan struct{}), writeStarted: make(chan struct{}), closeStarted: make(chan struct{}),
		releaseRead: make(chan struct{}), releaseWrite: make(chan struct{}), readData: []byte("final RX"),
	}
}

func (p *barrierSessionPort) Read(data []byte) (int, error) {
	close(p.readStarted)
	<-p.releaseRead
	return copy(data, p.readData), io.EOF
}

func (p *barrierSessionPort) Write(data []byte) (int, error) {
	close(p.writeStarted)
	<-p.releaseWrite
	return len(data), p.writeErr
}

func (p *barrierSessionPort) SetLines(bool, bool) error { return nil }

func (p *barrierSessionPort) Close() error {
	p.closeOnce.Do(func() { close(p.closeStarted) })
	return p.closeErr
}

func releaseSessionBarrier(barrier chan struct{}) {
	select {
	case <-barrier:
	default:
		close(barrier)
	}
}

func awaitSessionBarrier(t *testing.T, barrier <-chan struct{}) {
	t.Helper()
	select {
	case <-barrier:
	case <-time.After(captureTestTimeout):
		t.Fatal("session worker did not reach its barrier")
	}
}

func TestSessionCloseWaitsForFinalReadAndWriteRecords(t *testing.T) {
	port := newBarrierSessionPort()
	p := newSession(port, 115200, nil)
	defer func() {
		releaseSessionBarrier(port.releaseRead)
		releaseSessionBarrier(port.releaseWrite)
		_ = p.Close()
	}()
	awaitSessionBarrier(t, port.readStarted)
	sent := make(chan error, 1)
	go func() { sent <- p.Send(context.Background(), []byte("final TX")) }()
	awaitSessionBarrier(t, port.writeStarted)
	closed := make(chan error, 1)
	go func() { closed <- p.Close() }()
	awaitSessionBarrier(t, port.closeStarted)
	releaseSessionBarrier(port.releaseRead)
	awaitSessionBarrier(t, p.readDone)
	select {
	case err := <-closed:
		t.Fatalf("Close returned before Write emitted its final record: %v", err)
	default:
	}
	releaseSessionBarrier(port.releaseWrite)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(captureTestTimeout):
		t.Fatal("Close did not join the final writer")
	}
	select {
	case err := <-sent:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(captureTestTimeout):
		t.Fatal("Send did not finish")
	}
	records, rx, tx := p.drainDisplay()
	if len(records) != 2 || rx != uint64(len("final RX")) || tx != uint64(len("final TX")) {
		t.Fatalf("Close left incomplete terminal records: records=%d rx=%d tx=%d", len(records), rx, tx)
	}
}
