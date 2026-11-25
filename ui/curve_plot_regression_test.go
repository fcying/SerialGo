package ui

import (
	"math"
	"strconv"
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

func TestCurveChartEqualLargeDeviceTimes(t *testing.T) {
	for _, test := range []struct {
		name     string
		x        float64
		span     string
		previous bool
	}{
		{name: "ordinary", x: 42},
		{name: "positive_integer_precision", x: 9007199254740992},
		{name: "negative_integer_precision", x: -9007199254740992},
		{name: "large_negative", x: -1e20},
		{name: "positive_max", x: math.MaxFloat64},
		{name: "negative_max", x: -math.MaxFloat64},
		{name: "follow_window_rounds_to_zero", x: 1e20, span: "10 s", previous: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newController(&internal.App{Settings: config.Settings{Language: "en"}})
			s.curve = newCurveViewState(s.settings)
			s.curve.span, s.curve.position = test.span, 100
			points := []internal.CurvePoint{{X: test.x, Y: 3}}
			if test.previous {
				points = append([]internal.CurvePoint{{X: math.Nextafter(test.x, math.Inf(-1)), Y: 2, Break: true}}, points...)
				points[1].Break = true
			}
			s.curve.snapshot = internal.CurveSnapshot{DeviceTime: true, LatestX: test.x, Series: []internal.CurveSeries{{Color: 0xff0000ff, Points: points}}}
			tt := gui.NewTester(func(c *gui.Context) {
				gui.Column(c).FillWidth().FillHeight().Children(func() { s.curveChart(c) })
			}, 500, 300)
			tt.Image()
			bounds := s.curve.geometry.bounds
			if math.IsInf(bounds.x0, 0) || math.IsInf(bounds.x1, 0) || !(bounds.x0 < bounds.x1) || test.x < bounds.x0 || test.x > bounds.x1 {
				t.Fatalf("invalid bounds for %g: %+v", test.x, bounds)
			}
			if test.name == "ordinary" && (bounds.x0 != 42 || bounds.x1 != 43) {
				t.Fatalf("ordinary bounds changed: %+v", bounds)
			}
			dots := s.curve.geometry.dots[0]
			wantDots := 1
			if test.previous {
				wantDots = 2
			}
			if len(dots) != wantDots || math.IsNaN(float64(dots[len(dots)-1].X)) || math.IsInf(float64(dots[len(dots)-1].X), 0) {
				t.Fatalf("samples did not produce finite dots: %+v", dots)
			}
		})
	}
}

func TestCurveExtremeFiniteRanges(t *testing.T) {
	for _, tc := range []struct {
		name, span string
		position   float64
		points     []internal.CurvePoint
	}{
		{"opposite extremes", "All", 0, []internal.CurvePoint{{X: -math.MaxFloat64, Y: -math.MaxFloat64}, {X: math.MaxFloat64, Y: math.MaxFloat64}}},
		{"large range", "All", 0, []internal.CurvePoint{{X: -1e308, Y: 1e308}, {X: 1e308, Y: -1e308}}},
		{"positive singleton", "All", 0, []internal.CurvePoint{{X: math.MaxFloat64, Y: math.MaxFloat64}}},
		{"negative singleton", "All", 0, []internal.CurvePoint{{X: -math.MaxFloat64, Y: -math.MaxFloat64}}},
		{"window center", "10 s", 50, []internal.CurvePoint{{X: -1e308, Y: -1e308}, {X: 1e308, Y: 1e308}}},
		{"window end", "10 s", 100, []internal.CurvePoint{{X: -1e308, Y: -1e308}, {X: 1e308, Y: 1e308}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := newCurveViewState(&config.Settings{})
			v.span, v.position = tc.span, tc.position
			v.snapshot = internal.CurveSnapshot{DeviceTime: true, LatestX: tc.points[len(tc.points)-1].X, Series: []internal.CurveSeries{{Points: tc.points}}}
			v.prepareCurveGeometry(gui.Rect{W: 600, H: 200})
			b := v.geometry.bounds
			for _, value := range []float64{b.x0, b.x1, b.y0, b.y1} {
				if math.IsInf(value, 0) || math.IsNaN(value) {
					t.Fatalf("non-finite bounds: %+v", b)
				}
			}
			if !v.geometry.hasData || b.x0 >= b.x1 || b.y0 >= b.y1 {
				t.Fatalf("invalid bounds: %+v", b)
			}
			for i := 0; i <= 4; i++ {
				f := float64(i) / 4
				for _, axis := range [][2]float64{{b.x0, b.x1}, {b.y0, b.y1}} {
					value := curveLerp(axis[0], axis[1], f)
					projected := curveFraction(value, axis[0], axis[1])
					if math.IsNaN(value) || math.IsInf(value, 0) || math.IsNaN(projected) || math.IsInf(projected, 0) || projected < 0 || projected > 1 {
						t.Fatalf("axis %v tick %d: value=%g projection=%g", axis, i, value, projected)
					}
				}
			}
			path := v.paths[0].Bounds()
			for _, value := range []float32{path.X, path.Y, path.W, path.H} {
				if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
					t.Fatalf("non-finite path bounds: %+v", path)
				}
			}
			if (tc.name == "opposite extremes" || tc.name == "large range") && (path.W < 599 || path.H < 100) {
				t.Fatalf("extreme segment did not span plot: %+v", path)
			}
			for _, dot := range v.geometry.dots[0] {
				if math.IsNaN(float64(dot.X)) || math.IsInf(float64(dot.X), 0) || math.IsNaN(float64(dot.Y)) || math.IsInf(float64(dot.Y), 0) {
					t.Fatalf("non-finite sample projection: %+v", dot)
				}
			}
		})
	}
}

func TestClipCurveSegmentExtremeFiniteValues(t *testing.T) {
	a := internal.CurvePoint{X: -math.MaxFloat64, Y: math.MaxFloat64}
	b := internal.CurvePoint{X: math.MaxFloat64, Y: -math.MaxFloat64}
	start, end, ok := clipCurveSegment(a, b, -math.MaxFloat64/2, math.MaxFloat64/2)
	if !ok || !finiteCurvePoint(start) || !finiteCurvePoint(end) || start.X != -math.MaxFloat64/2 || end.X != math.MaxFloat64/2 || math.Abs(start.Y/math.MaxFloat64-.5) > 1e-15 || math.Abs(end.Y/math.MaxFloat64+.5) > 1e-15 {
		t.Fatalf("clipped segment = %+v, %+v, %t", start, end, ok)
	}
}

func TestClipCurveSegmentNearEndpoints(t *testing.T) {
	for _, tc := range []struct {
		name          string
		a, b          internal.CurvePoint
		x0, x1, wantY float64
		clippedStart  bool
	}{
		{"near right", internal.CurvePoint{X: -1e20, Y: -1e20}, internal.CurvePoint{X: 10, Y: 10, Break: true}, 0, 10, 0, true},
		{"near left", internal.CurvePoint{X: -10, Y: -10, Break: true}, internal.CurvePoint{X: 1e20, Y: 1e20}, -10, 0, 0, false},
		{"overflowing width", internal.CurvePoint{X: -math.MaxFloat64, Y: -math.MaxFloat64}, internal.CurvePoint{X: 10, Y: 10}, 0, 10, 0, true},
		{"underflowing fraction", internal.CurvePoint{X: 0, Y: 0}, internal.CurvePoint{X: math.MaxFloat64, Y: math.MaxFloat64}, math.SmallestNonzeroFloat64, math.MaxFloat64, math.SmallestNonzeroFloat64, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start, end, ok := clipCurveSegment(tc.a, tc.b, tc.x0, tc.x1)
			if !ok || !finiteCurvePoint(start) || !finiteCurvePoint(end) {
				t.Fatalf("invalid clipped segment: %+v %+v %v", start, end, ok)
			}
			intersection, endpoint, wantEndpoint := start, end, tc.b
			if !tc.clippedStart {
				intersection, endpoint, wantEndpoint = end, start, tc.a
			}
			if endpoint != wantEndpoint {
				t.Fatalf("endpoint = %+v, want %+v", endpoint, wantEndpoint)
			}
			if tc.wantY == math.SmallestNonzeroFloat64 {
				if intersection.Y != tc.wantY {
					t.Fatalf("intersection Y = %g, want %g", intersection.Y, tc.wantY)
				}
			} else if math.Abs(intersection.Y-tc.wantY) > 1e-12 {
				t.Fatalf("intersection Y = %g, want %g", intersection.Y, tc.wantY)
			}
		})
	}
	for _, pair := range [][2]float64{{1e20, 1}, {-math.MaxFloat64, math.MaxFloat64}} {
		if curveLerp(pair[0], pair[1], 0) != pair[0] || curveLerp(pair[0], pair[1], 1) != pair[1] {
			t.Fatalf("lerp did not preserve endpoints %v", pair)
		}
	}
}

func TestCurveChartNearEndpointInterpolation(t *testing.T) {
	s := newController(&internal.App{Settings: config.Settings{Language: "en"}})
	s.curve = newCurveViewState(s.settings)
	s.curve.span, s.curve.position = "10 s", 100
	s.curve.snapshot = internal.CurveSnapshot{DeviceTime: true, LatestX: 10, Series: []internal.CurveSeries{{
		Color: 0xff0000ff, Points: []internal.CurvePoint{{X: -1e20, Y: -1e20}, {X: 10, Y: 10}},
	}}}
	tt := gui.NewTester(func(c *gui.Context) {
		gui.Column(c).FillWidth().FillHeight().Children(func() { s.curveChart(c) })
	}, 500, 300)
	image := tt.Image()
	assertCurveTimeWindowBounds(t, s.curve, curveBounds{0, 10, -.8, 10.8})
	plot := s.curve.geometry.key.plot
	redPixels := 0
	for y := int(plot.Y); y < int(plot.Y+plot.H); y++ {
		for x := int(plot.X); x < int(plot.X+plot.W); x++ {
			pixel := image.RGBAAt(x, y)
			if pixel.R > 180 && pixel.G < 100 && pixel.B < 100 {
				redPixels++
			}
		}
	}
	if redPixels < int(plot.W/2) {
		t.Fatalf("clipped diagonal did not render across plot: %d red pixels", redPixels)
	}
}

func TestClosestCurvePointOverflowDistance(t *testing.T) {
	series := internal.CurveSeries{Points: []internal.CurvePoint{{X: -math.MaxFloat64}, {X: -1e308}}}
	if got := closestCurvePoint(series, -math.MaxFloat64, math.MaxFloat64, math.MaxFloat64); got != 1 {
		t.Fatalf("closest point = %d, want 1", got)
	}
}

func TestCurveFollowRoundedWindowShowsLatestAtRight(t *testing.T) {
	var paintRect gui.Rect
	probe := gui.NewTester(func(c *gui.Context) {
		gui.Column(c).Fill().Children(func() {
			gui.Box(c).FillWidth().Grow(1).MinHeight(64).Background(c.Theme().Surface).
				Border(1, c.Theme().Border).Radius(4).Cache().Draw(func(_ *gui.Painter, r gui.Rect) {
				paintRect = r
			})
		})
	}, 500, 300)
	probe.Image()
	for _, latest := range []float64{1e20, -1e20, math.MaxFloat64} {
		s := newController(&internal.App{Settings: config.Settings{Language: "en"}})
		s.curve = newCurveViewState(s.settings)
		s.curve.span, s.curve.position = "10 s", 100
		s.curve.snapshot = internal.CurveSnapshot{Revision: 1, DeviceTime: true, LatestX: latest,
			Series: []internal.CurveSeries{{Color: 0xff0000ff, Points: []internal.CurvePoint{
				{X: math.Nextafter(latest, math.Inf(-1)), Y: 1}, {X: latest, Y: 2, Break: true},
			}}}}
		tt := gui.NewTester(func(c *gui.Context) {
			gui.Column(c).Fill().Children(func() { s.curveChart(c) })
		}, 500, 300)
		image := tt.Image()
		plot := s.curve.geometry.key.plot
		chart, ok := tt.Find("Serial curve chart")
		if !ok {
			t.Fatal("chart missing")
		}
		bounds := s.curve.geometry.bounds
		if bounds.x1 != latest || bounds.x0 != math.Nextafter(latest, math.Inf(-1)) {
			t.Fatalf("latest %g: wrong follow window %+v", latest, bounds)
		}
		y := int(plot.Y + plot.H - float32(curveFraction(2, bounds.y0, bounds.y1))*plot.H)
		found := false
		for dy := -2; dy <= 2; dy++ {
			for dx := 1; dx <= 3; dx++ {
				pixel := image.RGBAAt(int(chart.X+plot.X+plot.W-paintRect.X)-dx, int(chart.Y-paintRect.Y)+y+dy)
				found = found || (int(pixel.R)-int(pixel.G) > 80 && int(pixel.R)-int(pixel.B) > 80)
			}
		}
		if !found {
			t.Fatalf("latest %g is not visibly plotted at the right edge", latest)
		}
	}
}

func TestCurveYAxisReservesMeasuredLabelsAndUpdatesHitTesting(t *testing.T) {
	s := newController(&internal.App{Settings: config.Settings{Language: "en"}})
	s.curve = newCurveViewState(s.settings)
	s.curve.snapshot = internal.CurveSnapshot{Revision: 1, DeviceTime: true, LatestX: 1,
		Series: []internal.CurveSeries{{Name: "Extreme", Color: 0xff0000ff, Points: []internal.CurvePoint{
			{X: 0, Y: -math.MaxFloat64}, {X: 1, Y: math.MaxFloat64},
		}}}}
	tt := gui.NewTester(func(c *gui.Context) {
		gui.Column(c).Fill().Children(func() { s.curveChart(c) })
	}, 500, 300)
	image := tt.Image()
	chart, ok := tt.Find("Serial curve chart")
	if !ok {
		t.Fatal("chart missing")
	}
	plot := s.curve.geometry.key.plot
	var width float32
	reference := gui.NewTester(func(c *gui.Context) {
		gui.Box(c).Fill().Background(c.Theme().Surface).Draw(func(p *gui.Painter, r gui.Rect) {
			label := "1.7976931348623157e+308"
			width, _ = p.MeasureText(0, gui.Span{Text: label, Size: 10})
			p.Text(r.X+4, r.Y+8, label, 10, c.Theme().TextMuted)
		})
	}, 500, 300)
	want := reference.Image()
	if plot.X < chart.X+width+8 || plot.X <= chart.X+66 {
		t.Fatalf("long label overlaps plot: plot=%+v width=%g", plot, width)
	}
	for y := 8; y < 19; y++ {
		for x := 4; x < int(4+width); x++ {
			if image.RGBAAt(int(chart.X)+x, int(chart.Y)+y) != want.RGBAAt(x, y) {
				t.Fatalf("extreme Y label clipped or overwritten at %d,%d", x, y)
			}
		}
	}
	tt.Move(plot.X+plot.W*.9, plot.Y+plot.H/2)
	tt.Image()
	if !s.curve.geometry.hoverValid || s.curve.geometry.closest[0] != 1 {
		t.Fatal("hover did not use the expanded plot geometry")
	}
	s.curve.snapshot.Series[0].Points[0].Y = 0
	s.curve.snapshot.Series[0].Points[1].Y = 1
	s.curve.snapshot.Revision++
	tt.Frame()
	tt.Image()
	ordinary := s.curve.geometry.bounds
	var expectedX float32
	measure := gui.NewTester(func(c *gui.Context) {
		gui.Column(c).Fill().Children(func() {
			gui.Box(c).FillWidth().Grow(1).MinHeight(64).Background(c.Theme().Surface).
				Border(1, c.Theme().Border).Radius(4).Cache().Draw(func(p *gui.Painter, r gui.Rect) {
				var widest float32
				for i := 0; i <= 4; i++ {
					value := curveLerp(ordinary.y1, ordinary.y0, float64(i)/4)
					label := strconv.FormatFloat(value, 'g', 5, 64)
					width, _ := p.MeasureText(0, gui.Span{Text: label, Size: 10})
					widest = max(widest, width)
				}
				expectedX = r.X + max(float32(66), widest+8)
			})
		})
	}, 500, 300)
	measure.Image()
	if got := s.curve.geometry.key.plot.X; got != expectedX || got >= plot.X {
		t.Fatalf("ordinary label margin = %g, measured expectation %g, extreme margin %g", got, expectedX, plot.X)
	}
}

func TestCurveExtremeLabelsInVeryNarrowChart(t *testing.T) {
	s := newController(&internal.App{Settings: config.Settings{Language: "en"}})
	s.curve = newCurveViewState(s.settings)
	s.curve.snapshot = internal.CurveSnapshot{Revision: 1, DeviceTime: true, LatestX: 1,
		Series: []internal.CurveSeries{{Color: 0xff0000ff, Points: []internal.CurvePoint{
			{X: 0, Y: -math.MaxFloat64}, {X: 1, Y: math.MaxFloat64},
		}}}}
	tt := gui.NewTester(func(c *gui.Context) {
		gui.Column(c).Fill().Children(func() { s.curveChart(c) })
	}, 60, 200)
	tt.Image()
	chart, ok := tt.Find("Serial curve chart")
	plot := s.curve.geometry.key.plot
	if !ok || plot.W < 1 || plot.X < chart.X || plot.X+plot.W > chart.X+chart.W {
		t.Fatalf("narrow chart has unusable plot: chart=%+v plot=%+v", chart, plot)
	}
	tt.Move(plot.X+plot.W/2, plot.Y+plot.H/2)
	tt.Image()
	if !s.curve.geometry.hoverValid {
		t.Fatal("narrow plot cannot be hovered")
	}
}
