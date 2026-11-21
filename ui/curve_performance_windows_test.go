//go:build windows

package ui

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/egoist/mygo"
	gui "github.com/egoist/mygo/ui"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"
)

// Set SERIALGO_NATIVE_PERF=1 and MYGO_FRAME_STATS=all in an interactive
// Windows session to measure this synthetic window instead of running tests.
func TestMain(m *testing.M) {
	if os.Getenv("SERIALGO_NATIVE_PERF") != "1" {
		os.Exit(m.Run())
	}
	const count = 1_000_000
	s := newController(&internal.App{Settings: config.Settings{Language: "en"}})
	s.curve = newCurveViewState(s.settings)
	v := s.curve
	v.span = "All"
	v.snapshot.DeviceTime = true
	v.snapshot.Revision = 1
	colors := []uint32{0xff0000ff, 0x00aa00ff, 0x0000ffff, 0xff9900ff}
	for c := range 4 {
		width := 137 + c*32
		series := internal.CurveSeries{
			Name: fmt.Sprintf("channel %d", c+1), Color: colors[c],
			Points: make([]internal.CurvePoint, count), TimeRuns: []int{0},
		}
		for i := range count {
			phase := i%(2*width) - width
			series.Points[i] = internal.CurvePoint{X: float64(i) * 0.0005, Y: float64(width - max(phase, -phase))}
		}
		level := make([]internal.CurveBlock, 0, count/256)
		for start := 0; start+256 <= count; start += 256 {
			block := internal.CurveBlock{Start: start, End: start + 256, Min: start, Max: start}
			for i := start + 1; i < block.End; i++ {
				if series.Points[i].Y < series.Points[block.Min].Y {
					block.Min = i
				}
				if series.Points[i].Y > series.Points[block.Max].Y {
					block.Max = i
				}
			}
			level = append(level, block)
			series.Blocks = append(series.Blocks, block)
		}
		for len(level) > 1 {
			next := make([]internal.CurveBlock, 0, len(level)/2)
			for i := 0; i+1 < len(level); i += 2 {
				left, right := level[i], level[i+1]
				left.End = right.End
				if series.Points[right.Min].Y < series.Points[left.Min].Y {
					left.Min = right.Min
				}
				if series.Points[right.Max].Y > series.Points[left.Max].Y {
					left.Max = right.Max
				}
				next = append(next, left)
				series.Blocks = append(series.Blocks, left)
			}
			level = next
		}
		sort.Slice(series.Blocks, func(i, j int) bool {
			a, b := series.Blocks[i], series.Blocks[j]
			if a.Start == b.Start {
				return a.End > b.End
			}
			return a.Start < b.Start
		})
		v.snapshot.Series = append(v.snapshot.Series, series)
	}
	mygo.App.WhenReady(func() {
		window := mygo.NewWindow(mygo.WindowOptions{
			Title: "SerialGo native curve perf", Width: 1040, Height: 600, Content: gui.View(s.serialCurveView),
		})
		go func() {
			time.Sleep(2 * time.Second)
			uncapped := os.Getenv("MYGO_UNCAPPED") == "1"
			interval := curveRefreshInterval
			mode := "vsync"
			if uncapped {
				interval = time.Millisecond
				mode = "uncapped"
			}
			fmt.Fprintf(os.Stderr, "NATIVE_PERF_BEGIN %d mode=%s interval=%s\n", time.Now().UnixNano(), mode, interval)
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			end := time.NewTimer(8 * time.Second)
			defer end.Stop()
			requested, processed := 0, 0
			for {
				select {
				case <-ticker.C:
					requested++
					mygo.RunOnMain(func() {
						processed++
						v.snapshot.Revision++
						for i := range v.snapshot.Series {
							v.snapshot.Series[i].Points[0].Y = float64(v.snapshot.Revision % 2)
						}
						// A unique color prevents coalesced sample toggles from leaving the scene unchanged.
						color := uint32(v.snapshot.Revision & 0xFFFFFF)
						v.snapshot.Series[3].Color = color<<8 | 0xFF
						window.Invalidate()
					})
				case <-end.C:
					mygo.RunOnMain(func() {
						var memory runtime.MemStats
						runtime.ReadMemStats(&memory)
						fmt.Fprintf(os.Stderr, "NATIVE_PERF_END %d mode=%s requested=%d processed=%d heap_alloc=%d heap_sys=%d\n", time.Now().UnixNano(), mode, requested, processed, memory.HeapAlloc, memory.HeapSys)
						window.Close()
					})
					return
				}
			}
		}()
	})
	if err := mygo.App.Run(); err != nil {
		panic(err)
	}
}
