//go:build linux

package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"testing"
	"time"

	gui "github.com/egoist/mygo/ui"
	"github.com/fcying/SerialGo/communication/serialport"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"
	"golang.org/x/sys/unix"
)

const (
	curvePerfWidth      = 1040
	curvePerfHeight     = 600
	curvePerfChannels   = 4
	curvePerfPointLimit = 1_000_000
	curvePerfWindow     = 8 * time.Second
	curvePerfFrameTime  = curveRefreshInterval
)

func curvePerfDuration(t *testing.T) time.Duration {
	t.Helper()
	raw := os.Getenv("SERIALGO_CURVE_PERF_DURATION")
	if raw == "" {
		return curvePerfWindow
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 {
		t.Fatalf("SERIALGO_CURVE_PERF_DURATION must be a positive duration, got %q", raw)
	}
	return duration
}

// TestCurvePerformance repeats the Linux PTY "All" view workload; run with:
// SERIALGO_CURVE_PERF=1 go test ./ui -run '^TestCurvePerformance$' -count=1 -timeout=2m -v
// Set SERIALGO_CURVE_PERF_DURATION=180s and use -run '^TestCurvePerformance/live$'
// to sample more than one live snapshot compaction; the default window is 8s.
// The 60 Hz software render loop measures completed updates, not desktop presents.
func TestCurvePerformance(t *testing.T) {
	if os.Getenv("SERIALGO_CURVE_PERF") != "1" {
		t.Skip("set SERIALGO_CURVE_PERF=1 to run the Linux PTY curve workload")
	}
	perfWindow := curvePerfDuration(t)
	master, port := openCurvePerfPTY(t)
	profile := config.NewCurveProfile("capacity")
	profile.PointLimit, profile.TimePattern, profile.TimeScale = curvePerfPointLimit, `t=([0-9]+)`, 1e-6
	profile.Channels = nil
	for i := range curvePerfChannels {
		name := string(rune('a' + i))
		profile.Channels = append(profile.Channels, config.CurveChannel{
			Name: name, Mode: "prefix", Pattern: name + "=", Scale: 1, Enabled: true,
		})
	}
	app := &internal.App{
		SettingsPath: filepath.Join(t.TempDir(), "config.toml"),
		Settings: config.Settings{
			Language:      "en",
			CurveProfiles: []config.CurveProfile{profile},
			Port:          serialport.Settings{Name: port, Baud: "921600", DataBits: "8", Parity: "None", StopBits: "1"},
		},
	}
	view := &curvePerfView{callbacks: make(chan func(), 32), closed: make(chan struct{})}
	app.Startup(view)
	t.Cleanup(func() {
		close(view.closed)
		if err := app.Shutdown(); err != nil {
			t.Error(err)
		}
	})
	app.SetPorts([]serialport.Info{{Name: port}})
	app.TogglePort()
	select {
	case fn := <-view.callbacks:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("PTY open timed out")
	}
	if !app.Connected() {
		t.Fatalf("PTY open: %+v", app.Status)
	}
	if err := app.StartCurve(0); err != nil {
		t.Fatal(err)
	}

	s := newController(app)
	s.curve = newCurveViewState(s.settings)
	v := s.curve
	v.span, v.running, v.connected = "All", true, true

	// Fill and wrap the retention rings before timing. Pump the frontend queue
	// during prefill as the application would, without rendering partial history.
	prefill := startCurvePerfFeed(t, master, 0, curvePerfPointLimit+250, 250, 2*time.Millisecond)
	deadline := time.NewTimer(time.Minute)
	defer deadline.Stop()
fill:
	for {
		select {
		case fn := <-view.callbacks:
			fn()
		case <-prefill.done:
			break fill
		case <-deadline.C:
			t.Fatal("PTY prefill timed out")
		}
	}
	written := prefill.stop(t)
	settleCurvePerf(t, app, view, &v.snapshot, &v.liveCache, written)

	// Warm layout and caches at full capacity; use the production plot rect
	// for the separate paint probe, including the real sidebar and axes insets.
	full := gui.NewTester(s.serialCurveView, curvePerfWidth, curvePerfHeight)
	plot := v.geometry.key.plot
	if !v.geometry.hasData || plot.W <= 1 || plot.H <= 1 {
		t.Fatalf("full UI did not build the plot: %+v", plot)
	}
	t.Logf("Linux software tester %dx%d DIPs @ 1x; plot %.0fx%.0f; All; %d channels x %d points; target 2000 lines/s; measurement window=%s",
		curvePerfWidth, curvePerfHeight, plot.W, plot.H, curvePerfChannels, curvePerfPointLimit, perfWindow)

	// Keep the uninstrumented full-view result independent of the stroke probe.
	t.Run("live", func(t *testing.T) {
		written = measureCurvePerf(t, master, app, view, v, written, perfWindow, func() curvePerfStages {
			full.Frame()
			return curvePerfStages{}
		})
	})
	t.Run("stages", func(t *testing.T) {
		var costs curvePerfStages
		probe := gui.NewTester(func(c *gui.Context) {
			gui.Box(c).Fill().Draw(func(p *gui.Painter, _ gui.Rect) {
				start := time.Now()
				v.prepareCurveGeometry(plot)
				costs.geometry += time.Since(start)
				p.Clip(plot, 0, func() {
					// Use the same paths, width and colors as curveChart. The
					// real Painter flattens paths and rasterizes mask misses here;
					// subsequent scene compositing is outside the stroke timer.
					for i, series := range v.snapshot.Series {
						color := curveColor(series.Color)
						start := time.Now()
						p.StrokePath(&v.paths[i], 1.5, color)
						costs.stroke += time.Since(start)
						costs.strokes++
					}
				})
			})
		}, curvePerfWidth, curvePerfHeight)
		for i, path := range v.paths[:len(v.snapshot.Series)] {
			if bounds := path.Bounds(); bounds.W <= 1 || bounds.H <= 1 {
				t.Fatalf("series %d has no waveform path: %+v", i, bounds)
			}
		}
		measureCurvePerf(t, master, app, view, v, written, perfWindow, func() curvePerfStages {
			costs = curvePerfStages{}
			probe.Frame()
			if costs.strokes < curvePerfChannels {
				t.Fatalf("expected strokes for all channels, got %d", costs.strokes)
			}
			return costs
		})
	})
}

type curvePerfView struct {
	callbacks chan func()
	closed    chan struct{}
}

func (v *curvePerfView) Dispatch(fn func()) {
	select {
	case v.callbacks <- fn:
	case <-v.closed:
	}
}

// The plot has its own RX worker; the separate main-window log is not drawn.
func (v *curvePerfView) AppendEvents([]internal.Event) {}

func (v *curvePerfView) drain() {
	for {
		select {
		case fn := <-v.callbacks:
			fn()
		default:
			return
		}
	}
}

func openCurvePerfPTY(t *testing.T) (*os.File, string) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close() })
	fd := int(master.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	num, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	return master, fmt.Sprintf("/dev/pts/%d", num)
}

type curvePerfProgress struct{ rows, bytes int }

type curvePerfFeed struct {
	cancel   context.CancelFunc
	done     chan struct{}
	progress curvePerfProgress
	err      error
}

// A zero count writes until canceled. Each complete write is one paced batch.
func startCurvePerfFeed(t *testing.T, master *os.File, start, count, batch int, period time.Duration) *curvePerfFeed {
	ctx, cancel := context.WithCancel(context.Background())
	f := &curvePerfFeed{cancel: cancel, done: make(chan struct{})}
	t.Cleanup(func() {
		cancel()
		<-f.done
	})
	go func() {
		defer close(f.done)
		tick := time.NewTicker(period)
		defer tick.Stop()
		buf := make([]byte, 0, batch*40)
		for count == 0 || f.progress.rows < count {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			if ctx.Err() != nil {
				return
			}
			n := batch
			if count != 0 {
				n = min(n, count-f.progress.rows)
			}
			buf = buf[:0]
			first := start + f.progress.rows
			for i := first; i < first+n; i++ {
				buf = append(buf, 't', '=')
				buf = strconv.AppendInt(buf, int64(i)*500, 10)
				for c := range curvePerfChannels {
					w := 137 + c*32
					phase := i%(2*w) - w
					buf = append(buf, ' ', byte('a'+c), '=')
					buf = strconv.AppendInt(buf, int64(w-max(phase, -phase)), 10)
				}
				buf = append(buf, '\n')
			}
			// Don't let a writer whose slave stopped reading hang cleanup.
			if f.err = master.SetWriteDeadline(time.Now().Add(time.Second)); f.err != nil {
				return
			}
			var bytes int
			bytes, f.err = master.Write(buf)
			f.progress.bytes += bytes
			if f.err != nil {
				return
			}
			f.progress.rows += n
		}
	}()
	return f
}

func (f *curvePerfFeed) stop(t *testing.T) curvePerfProgress {
	t.Helper()
	f.cancel()
	<-f.done
	if f.err != nil {
		t.Fatalf("PTY feed stopped after %d lines: %v", f.progress.rows, f.err)
	}
	return f.progress
}

func settleCurvePerf(t *testing.T, app *internal.App, view *curvePerfView, dst *internal.CurveSnapshot, cache *internal.CurveLiveCache, want curvePerfProgress) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		view.drain()
		if app.Rx >= uint64(want.bytes) || !time.Now().Before(deadline) {
			app.CurveLiveSnapshot(dst, cache)
			if dst.Matched >= uint64(want.rows) || dst.DroppedChunks != 0 || !time.Now().Before(deadline) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	if app.Rx != uint64(want.bytes) || dst.Lines != uint64(want.rows) || dst.Matched != uint64(want.rows) ||
		dst.Invalid != 0 || dst.Oversize != 0 || dst.DroppedChunks != 0 || dst.DroppedBytes != 0 {
		t.Fatalf("rx=%d/%d bytes, lines=%d/%d, matched=%d, invalid=%d, oversize=%d, dropped=%d chunks/%d bytes",
			app.Rx, want.bytes, dst.Lines, want.rows, dst.Matched, dst.Invalid, dst.Oversize, dst.DroppedChunks, dst.DroppedBytes)
	}
	if !dst.DeviceTime || len(dst.Series) != curvePerfChannels {
		t.Fatalf("device time=%v, channels=%d", dst.DeviceTime, len(dst.Series))
	}
	for _, series := range dst.Series {
		if len(series.Points) != curvePerfPointLimit {
			t.Fatalf("%s retained %d points, want %d", series.Name, len(series.Points), curvePerfPointLimit)
		}
	}
}

type curvePerfStages struct {
	geometry, stroke time.Duration
	strokes          int
}

func measureCurvePerf(t *testing.T, master *os.File, app *internal.App, view *curvePerfView, v *curveViewState,
	written curvePerfProgress, window time.Duration, frame func() curvePerfStages) curvePerfProgress {
	t.Helper()
	tick := time.NewTicker(curvePerfFrameTime)
	defer tick.Stop()
	start := time.Now()
	feed := startCurvePerfFeed(t, master, written.rows, 0, 40, 20*time.Millisecond)
	var snapshots, frames, totals, geometries, strokes []time.Duration
	var overBudget, strokeCalls int
	for time.Since(start) < window {
		select {
		case <-feed.done:
			feed.stop(t)
			t.Fatal("live PTY feed stopped early")
		case <-tick.C:
		}
		view.drain()
		rev := v.snapshot.Revision
		begin := time.Now()
		app.CurveLiveSnapshot(&v.snapshot, &v.liveCache)
		snap := time.Since(begin)
		if v.snapshot.Revision == rev {
			continue // Count only frames with fresh waveforms, not refresh attempts.
		}
		begin = time.Now()
		stages := frame()
		draw := time.Since(begin)
		if v.geometry.key.revision != v.snapshot.Revision {
			t.Fatal("frame did not paint the current waveform revision")
		}
		snapshots = append(snapshots, snap)
		frames = append(frames, draw)
		total := snap + draw
		totals = append(totals, total)
		if total > curvePerfFrameTime {
			overBudget++
		}
		if stages.strokes != 0 {
			strokeCalls += stages.strokes
			geometries = append(geometries, stages.geometry)
			strokes = append(strokes, stages.stroke)
		}
	}
	elapsed := time.Since(start)
	input := feed.stop(t)
	if len(frames) == 0 || input.rows == 0 {
		t.Fatal("no live data or updated waveform frames measured")
	}
	written.rows += input.rows
	written.bytes += input.bytes
	settleCurvePerf(t, app, view, &v.snapshot, &v.liveCache, written)
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	t.Logf("Go memory after settle: heap_alloc=%.1f MiB heap_sys=%.1f MiB sys=%.1f MiB", float64(memory.HeapAlloc)/(1<<20), float64(memory.HeapSys)/(1<<20), float64(memory.Sys)/(1<<20))

	t.Logf("elapsed=%.3fs input=%d lines (%.1f lines/s) updates=%d (%.2f FPS) over %.2fms=%d/%d dropped=%d chunks/%d bytes",
		elapsed.Seconds(), input.rows, float64(input.rows)/elapsed.Seconds(), len(frames), float64(len(frames))/elapsed.Seconds(), float64(curvePerfFrameTime)/float64(time.Millisecond),
		overBudget, len(frames), v.snapshot.DroppedChunks, v.snapshot.DroppedBytes)
	logCurvePerfTimes(t, "snapshot", snapshots)
	logCurvePerfTimes(t, "software frame", frames)
	logCurvePerfTimes(t, "snapshot + frame", totals)
	if strokeCalls != 0 {
		t.Logf("isolated chart probe: %d StrokePath calls (including any repaints)", strokeCalls)
		logCurvePerfTimes(t, "prepareCurveGeometry", geometries)
		logCurvePerfTimes(t, "StrokePath sum per update", strokes)
	}
	return written
}

func logCurvePerfTimes(t *testing.T, name string, samples []time.Duration) {
	t.Helper()
	slices.Sort(samples)
	ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
	n := len(samples)
	p50 := (n+1)/2 - 1
	p95 := (95*n+99)/100 - 1
	p99 := (99*n+99)/100 - 1
	t.Logf("%s: p50=%.2fms p95=%.2fms p99=%.2fms max=%.2fms n=%d",
		name, ms(samples[p50]), ms(samples[p95]), ms(samples[p99]), ms(samples[n-1]), n)
}
