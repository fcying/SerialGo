package ui

import (
	"math"
	"testing"

	gui "github.com/egoist/mygo/ui"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"
)

func TestCurveChartClipsVisibleSegments(t *testing.T) {
	for _, test := range []struct {
		name    string
		points  []internal.CurvePoint
		visible bool
	}{
		{"sparse_crossing", []internal.CurvePoint{{X: 0, Y: 0}, {X: 100, Y: 100}}, true},
		{"explicit_break", []internal.CurvePoint{{X: 0, Y: 0}, {X: 100, Y: 100, Break: true}}, false},
		{"backwards_clock_without_break", []internal.CurvePoint{{X: 100, Y: 100}, {X: 0, Y: 0}}, false},
		{"single_sample", []internal.CurvePoint{{X: 50, Y: 50}}, true},
		{"invalid_sample_boundary", []internal.CurvePoint{{X: 0, Y: 0}, {X: 50, Y: math.NaN()}, {X: 100, Y: 100}}, false},
		{"invalid_time_boundary", []internal.CurvePoint{{X: 0, Y: 0}, {X: math.Inf(1), Y: 50}, {X: 100, Y: 100}}, false},
		{"vertical_segment", []internal.CurvePoint{{X: 0, Y: 0}, {X: 50, Y: 40, Break: true}, {X: 50, Y: 60}, {X: 100, Y: 100, Break: true}}, true},
		{"isolated_sample", []internal.CurvePoint{{X: 0, Y: 0}, {X: 50, Y: 50, Break: true}, {X: 100, Y: 100, Break: true}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newController(&internal.App{Settings: config.Settings{Language: "en"}})
			s.curve = newCurveViewState(s.settings)
			s.curve.span, s.curve.position = "10 s", 50
			s.curve.snapshot = internal.CurveSnapshot{DeviceTime: true, LatestX: 100, Series: []internal.CurveSeries{{Color: 0xff0000ff, Points: test.points}}}
			tt := gui.NewTester(func(c *gui.Context) {
				gui.Column(c).FillWidth().FillHeight().Children(func() { s.curveChart(c) })
			}, 500, 300)
			chart, found := tt.Find("Serial curve chart")
			if !found {
				t.Fatal("plot consumer is missing")
			}
			image := tt.Image()
			redPixels := 0
			for y := int(chart.Y + 15); y < int(chart.Y+chart.H-35); y++ {
				for x := int(chart.X + 67); x < int(chart.X+chart.W-17); x++ {
					pixel := image.RGBAAt(x, y)
					if pixel.R > 180 && pixel.G < 100 && pixel.B < 100 {
						redPixels++
					}
				}
			}
			if (redPixels > 0) != test.visible {
				t.Fatalf("visible curve pixels = %d, want visible = %v", redPixels, test.visible)
			}
			if test.name == "sparse_crossing" {
				assertCurveTimeWindowBounds(t, s.curve, curveBounds{45, 55, 44.2, 55.8})
				if len(s.curve.snapshot.Series[0].Points) != 2 || s.curve.snapshot.Series[0].Points[0].X != 0 || s.curve.snapshot.Series[0].Points[1].X != 100 {
					t.Fatal("clipping replaced real samples with synthetic intersections")
				}
			}
			if test.name == "vertical_segment" {
				assertCurveTimeWindowBounds(t, s.curve, curveBounds{45, 55, 38.4, 61.6})
			}
		})
	}
}

func TestCurveChartDoesNotBridgeParserBoundaries(t *testing.T) {
	for _, text := range []string{
		"t=0 VBAT=0\nt=50 VBAT=bad\nt=100 VBAT=100",
		"t=100 VBAT=100\nt=0 VBAT=0",
	} {
		v := newCurveViewState(&config.Settings{})
		v.span, v.position = "10 s", 50
		v.snapshot = curveTimeWindowSnapshot(t, text)
		segments := newCurveVisibleSegments(v.snapshot.Series[0], 45, 55)
		_, _, visible := segments.next()
		if visible {
			t.Fatalf("parser boundary produced a spurious crossing: %q", text)
		}
		assertCurveTimeWindowBounds(t, v, curveBounds{45, 55, -.08, 1.08})
	}
}
