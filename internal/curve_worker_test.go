package internal

import (
	"bytes"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/fcying/SerialGo/config"
)

type curveWorkerTestRead struct {
	data []byte
	err  error
}

type curveWorkerTestPort struct {
	reads  chan curveWorkerTestRead
	ready  chan struct{}
	closed chan struct{}
	once   sync.Once
}

func (p *curveWorkerTestPort) Read(dst []byte) (int, error) {
	select {
	case p.ready <- struct{}{}:
	case <-p.closed:
		return 0, io.EOF
	}
	select {
	case read := <-p.reads:
		return copy(dst, read.data), read.err
	case <-p.closed:
		return 0, io.EOF
	}
}

func (p *curveWorkerTestPort) Write(data []byte) (int, error) { return len(data), nil }
func (p *curveWorkerTestPort) SetLines(bool, bool) error      { return nil }
func (p *curveWorkerTestPort) Close() error {
	p.once.Do(func() { close(p.closed) })
	return nil
}

type curveWorkerTestHarness struct {
	app       *App
	session   *Session
	port      *curveWorkerTestPort
	capture   *captureTestFile
	entered   <-chan struct{}
	processed <-chan struct{}
	release   func()
	input     []byte
	reads     int
}

func newCurveWorkerTestHarness(t *testing.T) *curveWorkerTestHarness {
	t.Helper()
	profile := config.NewCurveProfile("worker")
	profile.Channels = []config.CurveChannel{{Name: "value", Mode: "regex", Pattern: `^V=([0-9]+)$`, Scale: 1, Enabled: true}}
	app := &App{Settings: config.Settings{CurveProfiles: []config.CurveProfile{profile}}}
	if err := app.StartCurve(0); err != nil {
		t.Fatal(err)
	}
	receiver := app.curveReceiver()
	receive := receiver.receive
	entered := make(chan struct{})
	gate := make(chan struct{})
	processed := make(chan struct{}, curveQueueLimit+16)
	first := true
	receiver.receive = func(chunk curveChunk) {
		if first {
			first = false
			close(entered)
			<-gate
		}
		receive(chunk)
		processed <- struct{}{}
	}
	port := &curveWorkerTestPort{
		reads: make(chan curveWorkerTestRead), ready: make(chan struct{}), closed: make(chan struct{}),
	}
	var releaseOnce sync.Once
	h := &curveWorkerTestHarness{
		app: app, port: port, capture: &captureTestFile{}, entered: entered, processed: processed,
		release: func() { releaseOnce.Do(func() { close(gate) }) },
	}
	h.session = newSession(port, 115200, receiver)
	t.Cleanup(func() {
		h.release()
		_ = h.session.Close()
	})
	curveWorkerTestAwait(t, port.ready)
	if err := h.session.setCapture(h.capture); err != nil {
		t.Fatal(err)
	}
	return h
}

func curveWorkerTestAwait(t *testing.T, ready <-chan struct{}) {
	t.Helper()
	select {
	case <-ready:
	case <-time.After(captureTestTimeout):
		t.Fatal("等待曲线测试屏障超时")
	}
}

func (h *curveWorkerTestHarness) send(t *testing.T, text string) {
	t.Helper()
	if len(text) > receiveBufferSize {
		t.Fatal("假设备单次读取超过接收缓冲区")
	}
	select {
	case h.port.reads <- curveWorkerTestRead{data: []byte(text)}:
	case <-time.After(captureTestTimeout):
		t.Fatal("曲线消费者阻塞了串口读取")
	}
	// Entering the next Read acknowledges all work on the previous serial chunk.
	curveWorkerTestAwait(t, h.port.ready)
	h.input = append(h.input, text...)
	h.reads++
}

func (h *curveWorkerTestHarness) eof(t *testing.T) {
	t.Helper()
	select {
	case h.port.reads <- curveWorkerTestRead{err: io.EOF}:
	case <-time.After(captureTestTimeout):
		t.Fatal("假设备无法提交 EOF")
	}
	curveWorkerTestAwait(t, h.session.readDone)
}

func (h *curveWorkerTestHarness) awaitProcessed(t *testing.T, count int) {
	t.Helper()
	for range count {
		curveWorkerTestAwait(t, h.processed)
	}
}

func (h *curveWorkerTestHarness) assertIO(t *testing.T) {
	t.Helper()
	data, closed := h.capture.snapshot()
	if !closed || !bytes.Equal(data, h.input) {
		t.Fatalf("曲线阻塞或溢出影响 DAT 录制: closed=%v got=%q want=%q", closed, data, h.input)
	}
	events, rx, tx := h.session.drainDisplay()
	var displayed []byte
	for _, event := range events {
		if event.Sent || event.Err != nil {
			t.Fatalf("显示队列混入非接收数据: %+v", event)
		}
		displayed = append(displayed, event.Data...)
	}
	if len(events) != h.reads || rx != uint64(len(h.input)) || tx != 0 || !bytes.Equal(displayed, h.input) {
		t.Fatalf("曲线阻塞或溢出影响显示: events=%d/%d RX=%d/%d TX=%d got=%q want=%q", len(events), h.reads, rx, len(h.input), tx, displayed, h.input)
	}
}

func (h *curveWorkerTestHarness) startClose(t *testing.T) <-chan error {
	t.Helper()
	closed := make(chan error, 1)
	go func() { closed <- h.session.Close() }()
	curveWorkerTestAwait(t, h.port.closed)
	curveWorkerTestAwait(t, h.session.readDone)
	return closed
}

func curveWorkerTestFinishClose(t *testing.T, closed <-chan error) {
	t.Helper()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(captureTestTimeout):
		t.Fatal("Close 未排空并退出曲线 worker")
	}
}

func TestCurveWorkerBlockedConsumerPreservesSerialCaptureAndDisplay(t *testing.T) {
	h := newCurveWorkerTestHarness(t)
	h.send(t, "V=11\n")
	curveWorkerTestAwait(t, h.entered)
	h.send(t, "V=22\n")
	h.send(t, "V=33\n")
	// Recording must flush even while the independent curve consumer is stopped.
	if err := h.session.SetCapture(nil); err != nil {
		t.Fatal(err)
	}
	h.assertIO(t)
	var snapshot CurveSnapshot
	h.app.CurveSnapshot(&snapshot)
	if snapshot.Lines != 0 || len(snapshot.Series[0].Points) != 0 {
		t.Fatalf("消费者门关闭时曲线不应已处理数据: %+v", snapshot)
	}
	closed := h.startClose(t)
	select {
	case err := <-closed:
		t.Fatalf("Close 在曲线消费者退出前返回: %v", err)
	default:
	}
	h.release()
	curveWorkerTestFinishClose(t, closed)
	select {
	case <-h.session.curve.done:
	default:
		t.Fatal("Close 返回后曲线 worker 仍未退出")
	}
	h.app.CurveSnapshot(&snapshot)
	points := snapshot.Series[0].Points
	if snapshot.Lines != 3 || snapshot.Matched != 3 || snapshot.DroppedBytes != 0 || snapshot.DroppedChunks != 0 || len(points) != 3 {
		t.Fatalf("关闭时未完整排空曲线数据: %+v", snapshot)
	}
	for i, want := range []float64{11, 22, 33} {
		if points[i].Y != want || points[i].Break {
			t.Fatalf("排空后的点值或连续性错误: %+v", points)
		}
	}
}

func TestCurveWorkerOverflowSeparatesRecordsAndPreservesSerialBytes(t *testing.T) {
	for _, test := range []struct {
		name     string
		dropped  []string
		recovery string
	}{
		{"complete dropped record", []string{"lost", " record\n"}, "99\nV=2\n"},
		{"partial dropped record", []string{"ignored\n", "V="}, "V=99\nV=2\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := newCurveWorkerTestHarness(t)
			h.send(t, "V=1\n")
			curveWorkerTestAwait(t, h.entered)
			for range curveQueueLimit - 1 {
				h.send(t, "\n")
			}
			// The last accepted record is incomplete before the missing bytes.
			h.send(t, "V=")
			var droppedBytes uint64
			for _, text := range test.dropped {
				h.send(t, text)
				droppedBytes += uint64(len(text))
			}
			h.release()
			h.awaitProcessed(t, curveQueueLimit+1)
			h.send(t, test.recovery)
			curveWorkerTestFinishClose(t, h.startClose(t))
			h.assertIO(t)
			var snapshot CurveSnapshot
			h.app.CurveSnapshot(&snapshot)
			points := snapshot.Series[0].Points
			if snapshot.DroppedBytes != droppedBytes || snapshot.DroppedChunks != uint64(len(test.dropped)) {
				t.Fatalf("曲线丢弃统计不精确: %+v", snapshot)
			}
			if snapshot.Lines != curveQueueLimit+2 || snapshot.Matched != 2 || snapshot.Invalid != 0 || len(points) != 2 || points[0].Y != 1 || points[0].Break || points[1].Y != 2 || !points[1].Break {
				t.Fatalf("缺口拼接出伪记录、未丢弃残行或未断线: snapshot=%+v points=%+v", snapshot, points)
			}
		})
	}
}

func TestCurveWorkerTerminalOverflowReportedBeforeCloseReturns(t *testing.T) {
	for _, eof := range []bool{false, true} {
		name := "Close"
		if eof {
			name = "EOF then Close"
		}
		t.Run(name, func(t *testing.T) {
			h := newCurveWorkerTestHarness(t)
			h.send(t, "V=1\n")
			curveWorkerTestAwait(t, h.entered)
			for range curveQueueLimit - 1 {
				h.send(t, "\n")
			}
			h.send(t, "V=3\n")
			dropped := []string{"V=9000\n", "V=9999"}
			var droppedBytes uint64
			for _, text := range dropped {
				h.send(t, text)
				droppedBytes += uint64(len(text))
			}
			if eof {
				h.eof(t)
			}
			closed := h.startClose(t)
			select {
			case err := <-closed:
				t.Fatalf("Close 跳过未排空的终止溢出: %v", err)
			default:
			}
			h.release()
			curveWorkerTestFinishClose(t, closed)
			h.assertIO(t)
			var snapshot CurveSnapshot
			h.app.CurveSnapshot(&snapshot)
			points := snapshot.Series[0].Points
			if snapshot.DroppedBytes != droppedBytes || snapshot.DroppedChunks != uint64(len(dropped)) || snapshot.Lines != curveQueueLimit+1 || snapshot.Matched != 2 || len(points) != 2 || points[0].Y != 1 || points[1].Y != 3 {
				t.Fatalf("无后续数据时终止溢出未报告或队列未排空: %+v", snapshot)
			}
		})
	}
}

func TestCurveWorkerQueuedEpochCannotPolluteResetAcquisition(t *testing.T) {
	for _, reset := range []string{"clear", "schema", "stop and restart", "close and restart"} {
		t.Run(reset, func(t *testing.T) {
			h := newCurveWorkerTestHarness(t)
			h.send(t, "V=7\nV=")
			curveWorkerTestAwait(t, h.entered)
			for range curveQueueLimit {
				h.send(t, "8\nV=9\n")
			}
			h.send(t, "V=999\n")
			want := float64(4)
			switch reset {
			case "clear":
				h.app.ClearCurve()
			case "schema":
				profiles := config.CloneCurveProfiles(h.app.Settings.CurveProfiles)
				profiles[0].Channels[0].Scale = 2
				if err := h.app.ApplyCurveProfiles(profiles, 0); err != nil {
					t.Fatal(err)
				}
				want = 8
			case "stop and restart":
				h.app.StopCurve()
				if err := h.app.StartCurve(0); err != nil {
					t.Fatal(err)
				}
			case "close and restart":
				h.app.CloseCurve()
				h.send(t, "V=100\n")
				if err := h.app.StartCurve(0); err != nil {
					t.Fatal(err)
				}
			}
			h.release()
			h.awaitProcessed(t, curveQueueLimit+1)
			h.send(t, "V=4\n")
			curveWorkerTestFinishClose(t, h.startClose(t))
			h.assertIO(t)
			var snapshot CurveSnapshot
			h.app.CurveSnapshot(&snapshot)
			points := snapshot.Series[0].Points
			if !h.app.CurveRunning() || snapshot.Lines != 1 || snapshot.Matched != 1 || snapshot.DroppedBytes != 0 || snapshot.DroppedChunks != 0 || len(points) != 1 || points[0].Y != want || points[0].Break {
				t.Fatalf("旧 epoch 的排队数据或丢弃统计污染新采集: %+v", snapshot)
			}
		})
	}
}
