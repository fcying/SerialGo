//go:build linux

package ui

import (
	"path/filepath"
	"testing"
	"time"

	gui "github.com/egoist/mygo/ui"
	"github.com/fcying/SerialGo/communication/serialport"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"
)

func TestCurveStoppedPausedViewCanShowFinalAcquisition(t *testing.T) {
	for _, language := range []string{"en", "zh-CN"} {
		t.Run(language, func(t *testing.T) {
			master, port := openCurvePerfPTY(t)
			profile := config.NewCurveProfile("firmware")
			app := internal.NewAppWithSettingsStore(config.Settings{
				Language: language, CurveProfiles: []config.CurveProfile{profile},
				Port: serialport.Settings{Name: port, Baud: "115200", DataBits: "8", Parity: "None", StopBits: "1"},
			}, filepath.Join(t.TempDir(), "config.toml"), settingsTestStore{})
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
				t.Fatal("opening serial port timed out")
			}
			if !app.Connected() {
				t.Fatal("serial port did not connect")
			}
			s := newController(app)
			s.curve = newCurveViewState(s.settings)
			tt := gui.NewTester(s.serialCurveView, 1040, 600)
			click := func(zh, en string) {
				t.Helper()
				if err := tt.Click(s.tr(zh, en)); err != nil {
					t.Fatal(err)
				}
			}
			writeSerial := func(line string) {
				t.Helper()
				n, err := master.Write([]byte(line))
				if err != nil || n != len(line) {
					t.Fatalf("serial write: wrote %d/%d bytes: %v", n, len(line), err)
				}
			}
			feed := func(line string, matched uint64) {
				t.Helper()
				writeSerial(line)
				deadline := time.Now().Add(5 * time.Second)
				var snapshot internal.CurveSnapshot
				for time.Now().Before(deadline) {
					view.drain()
					app.CurveSnapshot(&snapshot)
					if snapshot.Matched == matched {
						return
					}
					time.Sleep(2 * time.Millisecond)
				}
				t.Fatalf("serial input not captured: matched=%d, want %d", snapshot.Matched, matched)
			}
			// Reads spanning a start are excluded from the new epoch. Send blank
			// lines until the parser itself confirms that it received one in the
			// new epoch; old pending display events cannot satisfy this barrier.
			readBoundary := func() {
				t.Helper()
				deadline := time.Now().Add(5 * time.Second)
				var snapshot internal.CurveSnapshot
				for time.Now().Before(deadline) {
					writeSerial("\n")
					time.Sleep(2 * time.Millisecond)
					view.drain()
					app.CurveSnapshot(&snapshot)
					if snapshot.Lines > 0 {
						return
					}
				}
				t.Fatal("new acquisition did not receive a serial read boundary")
			}
			click("曲线采集", "Plot acquisition")
			readBoundary()
			feed("VBAT=1000\n", 1)
			app.CurveLiveSnapshot(&s.curve.snapshot, &s.curve.liveCache)
			click("暂停查看", "Pause view")
			feed("VBAT=2000\n", 2)
			if s.curve.snapshot.Matched != 1 || !s.curve.paused {
				t.Fatal("paused view did not retain the first snapshot while receiving")
			}
			click("曲线采集", "Plot acquisition")
			if s.curve.running || !s.curve.paused || s.curve.snapshot.Matched != 1 {
				t.Fatal("stopping replaced the paused snapshot")
			}
			click("查看最终快照", "View final snapshot")
			if s.curve.paused || s.curve.previewing || s.curve.snapshot.Matched != 2 || len(s.curve.snapshot.Series[0].Points) != 2 || s.curve.snapshot.Series[0].Points[1].Y != 2 {
				t.Fatalf("final snapshot did not show samples received while paused: %+v", s.curve.snapshot)
			}
			click("曲线采集", "Plot acquisition")
			if !s.curve.running || s.curve.paused || s.curve.snapshot.Matched != 0 {
				t.Fatal("restarting did not reset the acquisition and follow live")
			}
			readBoundary()
			feed("VBAT=3000\n", 1)
			app.CurveLiveSnapshot(&s.curve.snapshot, &s.curve.liveCache)
			click("暂停查看", "Pause view")
			click("实时跟随", "Follow live")
			if s.curve.paused || !s.curve.running {
				t.Fatal("follow live did not resume a running acquisition")
			}
			click("清空曲线", "Clear plot")
			if s.curve.snapshot.Matched != 0 || !s.curve.running {
				t.Fatal("clear did not reset the running snapshot")
			}
		})
	}
}
