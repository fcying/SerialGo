package ui

import (
	"math"
	"strconv"
	"testing"
	"time"

	gui "github.com/egoist/mygo/ui"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"
)

func TestCurveChartTimeAxisRendering(t *testing.T) {
	for _, test := range []struct {
		name       string
		deviceTime bool
		x0, x1     float64
		labels     []string
		clock      string
	}{
		{
			name: "device_100000_seconds_60_second_window", deviceTime: true,
			x0: 100000, x1: 100060,
			labels: []string{"100000", "100015", "100030", "100045", "100060"},
		},
		{
			name: "device_subsecond_large_base", deviceTime: true,
			x0: 100000, x1: 100000.08,
			labels: []string{"100000.00", "100000.02", "100000.04", "100000.06", "100000.08"},
		},
		{
			name: "device_submicrosecond_large_base", deviceTime: true,
			x0: 100000, x1: 100000.00000008,
			labels: []string{"100000", "100000.00000002", "100000.00000004", "100000.00000006", "100000.00000008"},
		},
		{
			name: "system_local_clock_seconds",
			x0:   1700000000, x1: 1700000060, clock: "15:04:05",
		},
		{
			name: "system_local_clock_milliseconds",
			x0:   1700000000, x1: 1700000002, clock: "15:04:05.000",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			labels := test.labels
			if !test.deviceTime {
				for i := 0; i <= 4; i++ {
					millis := int64(math.Round((test.x0 + float64(i)/4*(test.x1-test.x0)) * 1000))
					labels = append(labels, time.UnixMilli(millis).Local().Format(test.clock))
				}
			}
			s := newController(&internal.App{Settings: config.Settings{Language: "en"}})
			s.curve = newCurveViewState(s.settings)
			s.curve.snapshot = internal.CurveSnapshot{
				DeviceTime: test.deviceTime, LatestX: test.x1,
				Series: []internal.CurveSeries{{Color: 0xff0000ff, Points: []internal.CurvePoint{{X: test.x0, Y: 0}, {X: test.x1, Y: 1}}}},
			}
			tt := gui.NewTester(func(c *gui.Context) {
				gui.Column(c).FillWidth().FillHeight().Children(func() { s.curveChart(c) })
			}, 1200, 300)
			chart, found := tt.Find("Serial curve chart")
			if !found {
				t.Fatal("serial curve chart is missing")
			}
			// Painter text is not exposed by Tester.Texts; compare its rendered
			// axis band against independently specified labels instead.
			reference := gui.NewTester(func(c *gui.Context) {
				gui.Column(c).FillWidth().FillHeight().Children(func() {
					gui.Box(c).FillWidth().Grow(1).MinHeight(100).Background(c.Theme().Surface).
						Border(1, c.Theme().Border).Radius(4).Draw(func(p *gui.Painter, r gui.Rect) {
						plot := gui.Rect{X: r.X + 66, Y: r.Y + 14, W: max(1, r.W-82), H: max(1, r.H-48)}
						sample := "00:00:00"
						if test.clock == "15:04:05.000" {
							sample = "00:00:00.000"
						}
						if test.deviceTime {
							sample = labels[4]
						}
						width, _ := p.MeasureText(0, gui.Span{Text: sample, Size: 10})
						if test.deviceTime {
							startWidth, _ := p.MeasureText(0, gui.Span{Text: labels[0], Size: 10})
							width = max(width, startWidth)
						}
						for i, label := range labels {
							x := plot.X + plot.W*float32(i)/4
							labelX := max(r.X+4, min(x-width/2, r.X+r.W-width-4))
							p.RichText(labelX, plot.Y+plot.H+8, 0, gui.Span{Text: label, Size: 10, Color: c.Theme().TextMuted})
						}
					})
				})
			}, 1200, 300)
			got, want := tt.Image(), reference.Image()
			for y := int(chart.Y + chart.H - 27); y < int(chart.Y+chart.H-13); y++ {
				for x := int(chart.X); x < int(chart.X+chart.W); x++ {
					if got.RGBAAt(x, y) != want.RGBAAt(x, y) {
						t.Fatalf("time-axis pixels differ at (%d, %d), want labels %q", x, y, labels)
					}
				}
			}
		})
	}
}

func TestCurveDeviceTimeLabelsRemainDistinct(t *testing.T) {
	for _, bounds := range [][2]float64{
		{100000, 100060},
		{-100060, -100000},
		{-.04, .04},
		{100000, 100000.08},
		{100000, 100000.00000008},
		{1e12, 1e12 + .01},
		{1e20, 1e20 + 1e6},
		{1e-20, 9e-20},
		{1, 1 + 16*(math.Nextafter(1, 2)-1)},
	} {
		format, precision := curveDeviceTimeFormat(bounds[0], bounds[1])
		previous := math.Inf(-1)
		for i := 0; i <= 4; i++ {
			stamp := bounds[0] + float64(i)/4*(bounds[1]-bounds[0])
			label := strconv.FormatFloat(stamp, format, precision, 64)
			seconds, err := strconv.ParseFloat(label, 64)
			if err != nil || seconds <= previous {
				t.Fatalf("bounds %v: tick %d label %q does not advance beyond %g seconds: %v", bounds, i, label, previous, err)
			}
			previous = seconds
		}
	}
}

func TestCurveChartAccessibleName(t *testing.T) {
	for _, test := range []struct{ language, name, oldName string }{
		{"en", "Serial curve chart", "Voltage plot"},
		{"zh-CN", "串口曲线图", "电压曲线图"},
	} {
		t.Run(test.language, func(t *testing.T) {
			s := newController(&internal.App{Settings: config.Settings{Language: test.language}})
			s.curve = newCurveViewState(s.settings)
			tt := gui.NewTester(func(c *gui.Context) {
				gui.Column(c).FillWidth().FillHeight().Children(func() { s.curveChart(c) })
			}, 500, 300)
			if _, found := tt.Access(test.name); !found {
				t.Fatalf("accessible chart name %q is missing", test.name)
			}
			if _, found := tt.Access(test.oldName); found {
				t.Fatalf("obsolete voltage-specific chart name %q is still accessible", test.oldName)
			}
		})
	}
}

func TestCurveHoverDistinguishesCloseDeviceTimes(t *testing.T) {
	s := newController(&internal.App{Settings: config.Settings{Language: "en"}})
	s.curve = newCurveViewState(s.settings)
	s.curve.snapshot = internal.CurveSnapshot{
		DeviceTime: true, LatestX: 100000.002,
		Series: []internal.CurveSeries{{Name: "Temperature", Unit: "C", Color: 0x1677ccff,
			Points: []internal.CurvePoint{{X: 100000.001, Y: 20}, {X: 100000.002, Y: 20}},
		}},
	}
	tt := gui.NewTester(func(c *gui.Context) {
		gui.Column(c).Fill().Children(func() { s.curveChart(c) })
	}, 720, 300)
	chart, found := tt.Find("Serial curve chart")
	if !found {
		t.Fatal("serial curve chart is missing")
	}
	for _, sample := range []struct {
		position float32
		label    string
	}{
		{.25, "Temperature: 20 C @ 100000.001 s"},
		{.75, "Temperature: 20 C @ 100000.002 s"},
	} {
		tt.Move(chart.X+66+(chart.W-82)*sample.position, chart.Y+80)
		reference := gui.NewTester(func(c *gui.Context) {
			gui.Box(c).Fill().Background(c.Theme().Surface).Draw(func(p *gui.Painter, r gui.Rect) {
				p.Text(r.X+74, r.Y+19, sample.label, 11, gui.Hex("#1677cc"))
			})
		}, 720, 300)
		got, want := tt.Image(), reference.Image()
		for y := 20; y < 33; y++ {
			for x := 74; x < 260; x++ {
				if got.RGBAAt(int(chart.X)+x, int(chart.Y)+y) != want.RGBAAt(x, y) {
					t.Fatalf("hover label differs at (%d, %d), want %q", x, y, sample.label)
				}
			}
		}
	}
}
