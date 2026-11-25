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

func TestCurveExtremeDeviceTicks(t *testing.T) {
	for _, bounds := range [][2]float64{{-math.MaxFloat64, math.MaxFloat64}, {-1e308, 1e308}, {1e308, math.MaxFloat64}} {
		format, precision := curveDeviceTimeFormat(bounds[0], bounds[1])
		previous := math.Inf(-1)
		for i := 0; i <= 4; i++ {
			value := curveLerp(bounds[0], bounds[1], float64(i)/4)
			label := strconv.FormatFloat(value, format, precision, 64)
			parsed, err := strconv.ParseFloat(label, 64)
			if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) || parsed <= previous {
				t.Fatalf("bounds %v tick %d: %q after %g: %v", bounds, i, label, previous, err)
			}
			previous = parsed
		}
	}
}

func TestCurveYAxisLabelsRemainDistinct(t *testing.T) {
	for _, bounds := range [][2]float64{
		{100000.1, 100000.2}, {-100000.2, -100000.1},
		{1e12, 1e12 + .01}, {1e-20, 9e-20},
		{1, math.Nextafter(1, 2)},
		{0, 4 * math.SmallestNonzeroFloat64},
		{-math.MaxFloat64, math.MaxFloat64},
		{math.Nextafter(math.MaxFloat64, 0), math.MaxFloat64},
	} {
		for ticks := 1; ticks <= 4; ticks++ {
			labels := curveYLabels(curveBounds{y0: bounds[0], y1: bounds[1]}, ticks)
			for i := 0; i <= ticks; i++ {
				value := curveLerp(bounds[1], bounds[0], float64(i)/float64(ticks))
				parsed, err := strconv.ParseFloat(labels[i], 64)
				if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) {
					t.Fatalf("bounds %v ticks %d: invalid label %q: %v", bounds, ticks, labels[i], err)
				}
				if i > 0 {
					previous := curveLerp(bounds[1], bounds[0], float64(i-1)/float64(ticks))
					if value != previous && labels[i] == labels[i-1] {
						t.Fatalf("bounds %v ticks %d: distinct values %g and %g share label %q", bounds, ticks, previous, value, labels[i])
					}
				}
			}
		}
	}
}

func TestCurveYAxisCloseValuesRendered(t *testing.T) {
	s := newController(&internal.App{Settings: config.Settings{Language: "en"}})
	s.curve = newCurveViewState(s.settings)
	s.curve.snapshot = internal.CurveSnapshot{Revision: 1, DeviceTime: true, LatestX: 1,
		Series: []internal.CurveSeries{{Name: "Close", Color: 0xff0000ff, Points: []internal.CurvePoint{
			{X: 0, Y: 100000.1}, {X: 1, Y: 100000.2},
		}}}}
	tt := gui.NewTester(func(c *gui.Context) {
		gui.Column(c).Fill().Children(func() { s.curveChart(c) })
	}, 500, 300)
	got := tt.Image()
	chart, ok := tt.Find("Serial curve chart")
	if !ok {
		t.Fatal("chart missing")
	}
	plot := s.curve.geometry.key.plot
	labels := []string{"100000.21", "100000.18", "100000.15", "100000.12", "100000.09"}
	var widest float32
	reference := gui.NewTester(func(c *gui.Context) {
		gui.Column(c).Fill().Children(func() {
			gui.Box(c).FillWidth().Grow(1).MinHeight(64).Background(c.Theme().Surface).
				Border(1, c.Theme().Border).Radius(4).Draw(func(p *gui.Painter, r gui.Rect) {
				for _, label := range labels {
					width, _ := p.MeasureText(0, gui.Span{Text: label, Size: 10})
					widest = max(widest, width)
				}
				for i, label := range labels {
					y := r.Y + 14 + max(1, r.H-48)*float32(i)/4
					p.Text(r.X+4, y-6, label, 10, c.Theme().TextMuted)
				}
			})
		})
	}, 500, 300)
	want := reference.Image()
	if plot.X < chart.X+widest+8 {
		t.Fatalf("Y labels overlap plot: plot=%+v width=%g", plot, widest)
	}
	for y := int(chart.Y + 8); y < int(plot.Y+plot.H+7); y++ {
		for x := int(chart.X + 4); x < int(chart.X+4+widest); x++ {
			if got.RGBAAt(x, y) != want.RGBAAt(x, y) {
				t.Fatalf("Y-axis pixels differ at (%d, %d), want labels %q", x, y, labels)
			}
		}
	}
	tt.Move(plot.X+plot.W*.9, plot.Y+plot.H/2)
	tt.Image()
	if !s.curve.geometry.hoverValid || s.curve.geometry.closest[0] != 1 {
		t.Fatal("hover did not use the precision-adjusted plot geometry")
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

func TestCurveHoverValueRoundTrip(t *testing.T) {
	for _, test := range []struct {
		value float64
		want  string
	}{
		{100000.1, "100000.1"},
		{100000.2, "100000.2"},
		{0, "0"},
		{math.Copysign(0, -1), "-0"},
		{1e-6, "0.000001"},
		{1e12, "1e+12"},
		{math.SmallestNonzeroFloat64, "5e-324"},
		{math.MaxFloat64, "1.7976931348623157e+308"},
		{-math.MaxFloat64, "-1.7976931348623157e+308"},
		{math.Nextafter(1, 2), "1.0000000000000002"},
	} {
		got := curveHoverValue(test.value)
		if got != test.want {
			t.Errorf("curveHoverValue(%g) = %q, want %q", test.value, got, test.want)
		}
		parsed, err := strconv.ParseFloat(got, 64)
		if err != nil || math.Float64bits(parsed) != math.Float64bits(test.value) {
			t.Errorf("curveHoverValue(%g) = %q does not round-trip: %g, %v", test.value, got, parsed, err)
		}
	}
}

func TestCurveHoverDistinguishesCloseValuesAndDeviceTimes(t *testing.T) {
	for _, language := range []string{"en", "zh-CN"} {
		t.Run(language, func(t *testing.T) {
			s := newController(&internal.App{Settings: config.Settings{Language: language}})
			s.curve = newCurveViewState(s.settings)
			s.curve.snapshot = internal.CurveSnapshot{
				DeviceTime: true, LatestX: 100000.002,
				Series: []internal.CurveSeries{{Name: "Temperature", Unit: "C", Color: 0x1677ccff,
					Points: []internal.CurvePoint{{X: 100000.001, Y: 100000.1}, {X: 100000.002, Y: 100000.2}},
				}},
			}
			tt := gui.NewTester(func(c *gui.Context) {
				gui.Column(c).Fill().Children(func() { s.curveChart(c) })
			}, 720, 300)
			chartName := "Serial curve chart"
			if language == "zh-CN" {
				chartName = "串口曲线图"
			}
			chart, found := tt.Find(chartName)
			if !found {
				t.Fatal("serial curve chart is missing")
			}
			tt.Image()
			for index, sample := range []struct {
				position float32
				label    string
			}{
				{.25, "Temperature: 100000.1 C @ 100000.001 s"},
				{.75, "Temperature: 100000.2 C @ 100000.002 s"},
			} {
				plot := s.curve.geometry.key.plot
				tt.Move(plot.X+plot.W*sample.position, plot.Y+plot.H/2)
				if !s.curve.geometry.hoverValid || s.curve.geometry.closest[0] != index {
					t.Fatalf("hover did not select sample %d", index)
				}
				var referenceRect gui.Rect
				reference := gui.NewTester(func(c *gui.Context) {
					gui.Column(c).Fill().Children(func() {
						gui.Box(c).FillWidth().Grow(1).MinHeight(64).Background(c.Theme().Surface).
							Border(1, c.Theme().Border).Radius(4).Cache().Draw(func(p *gui.Painter, r gui.Rect) {
							referenceRect = r
							p.Fill(gui.Rect{X: plot.X + 4, Y: plot.Y + 4, W: max(0, plot.W-8), H: 17}, c.Theme().Surface, 2)
							p.Text(plot.X+8, plot.Y+5, sample.label, 11, gui.Hex("#1677cc"))
						})
					})
				}, 720, 300)
				got, want := tt.Image(), reference.Image()
				inkPixels := 0
				for y := int(plot.Y + 4); y < int(plot.Y+21); y++ {
					for x := int(plot.X + 8); x < min(int(plot.X+plot.W-4), int(plot.X+358)); x++ {
						pixel := want.RGBAAt(x, y)
						if int(pixel.B)-int(pixel.R) >= 12 {
							inkPixels++
						}
					}
				}
				if inkPixels == 0 {
					probe := gui.NewTester(func(c *gui.Context) {
						gui.Box(c).Fill().Background(c.Theme().Surface).Draw(func(p *gui.Painter, r gui.Rect) {
							p.Text(r.X+8, r.Y+8, "Font rendering probe", 11, gui.Hex("#1677cc"))
						})
					}, 200, 50).Image()
					for y := range 50 {
						for x := range 200 {
							pixel := probe.RGBAAt(x, y)
							if int(pixel.B)-int(pixel.R) >= 12 {
								t.Fatalf("reference hover label %q rendered no visible text despite available font rendering", sample.label)
							}
						}
					}
					t.Skip("font rendering unavailable in MyGo Tester")
				}
				for y := int(plot.Y + 4); y < int(plot.Y+21); y++ {
					for x := int(plot.X + 8); x < min(int(plot.X+plot.W-4), int(plot.X+358)); x++ {
						actualPixel := got.RGBAAt(x, y)
						expectedPixel := want.RGBAAt(x, y)
						// Ignore neutral grid/background pixels, including the grid visible just
						// below the hover backing. Compare both directions so extra glyphs fail.
						actualInk := int(actualPixel.B)-int(actualPixel.R) >= 12
						expectedInk := int(expectedPixel.B)-int(expectedPixel.R) >= 12
						if (actualInk || expectedInk) && actualPixel != expectedPixel {
							t.Fatalf("hover label %q differs at (%d, %d): got %v, want %v; chart=%+v plot=%+v reference=%+v sample=%d", sample.label, x, y, actualPixel, expectedPixel, chart, plot, referenceRect, index)
						}
					}
				}
			}
		})
	}
}
