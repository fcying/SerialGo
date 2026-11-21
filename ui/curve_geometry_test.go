package ui

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	gui "github.com/egoist/mygo/ui"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"
)

func TestCurveGeometryRefreshesAcrossPreviewLiveAndViewportChanges(t *testing.T) {
	s := newController(&internal.App{Settings: config.Settings{Language: "en"}})
	s.curve = newCurveViewState(s.settings)
	v := s.curve
	v.previewing = true
	v.snapshot = internal.CurveSnapshot{
		Revision: 7, DeviceTime: true, LatestX: 60,
		Series: []internal.CurveSeries{{Color: 0xff0000ff, Points: []internal.CurvePoint{
			{X: 0, Y: 1}, {X: 30, Y: 3}, {X: 60, Y: 2},
		}}},
	}
	view := func(c *gui.Context) {
		gui.Column(c).Fill().Children(func() { s.curveChart(c) })
	}
	tt := gui.NewTester(view, 720, 320)
	width := 720
	for _, change := range []struct {
		name  string
		apply func()
	}{
		{"preview to live with reused snapshot storage and revision", func() {
			v.previewing = false
			v.snapshot.Series[0].Points[0].Y = 3
			v.snapshot.Series[0].Points[1].Y = 1
		}},
		{"new samples", func() {
			for i := range v.snapshot.Series[0].Points {
				v.snapshot.Series[0].Points[i].X += 30
			}
			v.snapshot.LatestX = 90
			v.snapshot.Revision++
		}},
		{"time range", func() { v.span = "10 s" }},
		{"resize", func() {
			width = 900
			tt.SetSize(width, 320)
		}},
		{"frozen color without new samples", func() { v.snapshot.Series[0].Color = 0x008800ff }},
		{"clear", func() {
			v.snapshot.Series[0].Points = nil
			v.snapshot.Revision++
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			before := bytes.Clone(tt.Image().Pix)
			change.apply()
			tt.Frame()
			fresh := newController(&internal.App{Settings: *s.settings})
			fresh.curve = newCurveViewState(fresh.settings)
			fresh.curve.snapshot = v.snapshot
			fresh.curve.span, fresh.curve.position = v.span, v.position
			reference := gui.NewTester(func(c *gui.Context) {
				gui.Column(c).Fill().Children(func() { fresh.curveChart(c) })
			}, width, 320)
			got, want := tt.Image(), reference.Image()
			if !bytes.Equal(got.Pix, want.Pix) {
				t.Fatal("retained chart differs from a fresh rendering of the current data and viewport")
			}
			if bytes.Equal(before, got.Pix) {
				t.Fatal("visible chart did not reflect the data or viewport change")
			}
		})
	}
}

func TestCurveDenseColumnsPreserveExtremaAndBreaks(t *testing.T) {
	s := newController(&internal.App{Settings: config.Settings{Language: "en"}})
	s.curve = newCurveViewState(s.settings)
	v := s.curve
	v.span = "All"
	points := []internal.CurvePoint{{X: 0, Y: 2}}
	for run, sign := range []float64{1, -1} {
		for i := range 100 {
			x := 5 + float64(100*run+i)/100_000
			y := 2.0
			if i == 37 {
				y = 8
			}
			points = append(points, internal.CurvePoint{X: x, Y: y * sign, Break: i == 0})
		}
	}
	points = append(points, internal.CurvePoint{X: 10, Y: -2, Break: true})
	v.snapshot = internal.CurveSnapshot{Revision: 1, Series: []internal.CurveSeries{{Points: points, Color: 0xff0000ff}}}
	tt := gui.NewTester(func(c *gui.Context) {
		gui.Column(c).Fill().Children(func() { s.curveChart(c) })
	}, 500, 300)
	image := tt.Image()
	chart, found := tt.Find("Serial curve chart")
	if !found {
		t.Fatal("plot is not visible")
	}
	plot := gui.Rect{X: chart.X + 66, Y: chart.Y + 14, W: chart.W - 82, H: chart.H - 48}
	bounds := v.geometry.bounds
	x := int(plot.X + plot.W/2)
	redAt := func(value float64) bool {
		y := int(plot.Y + plot.H - float32((value-bounds.y0)/(bounds.y1-bounds.y0))*plot.H)
		for dx := -2; dx <= 2; dx++ {
			pixel := image.RGBAAt(x+dx, y)
			if int(pixel.R)-int(pixel.G) > 30 && int(pixel.R)-int(pixel.B) > 30 {
				return true
			}
		}
		return false
	}
	peak, trough, bridge := redAt(7), redAt(-7), redAt(0)
	if !peak || !trough || bridge {
		t.Fatalf("dense traces: upper extreme=%t lower extreme=%t gap bridged=%t (plot=%+v bounds=%+v path=%+v)", peak, trough, bridge, plot, bounds, v.paths[0].Bounds())
	}
}

func TestCurveBlockSummaryDrawPreservesSpikesAndGaps(t *testing.T) {
	var records strings.Builder
	records.WriteString("t=0 VBAT=2\nt=1 VBAT=bad\n")
	for i := 1; i < 1024; i++ {
		y := 2
		if i >= 512 {
			y = -2
		}
		switch i {
		case 300:
			y = 8
		case 512:
			// An invalid record creates a break at the join of two children.
			records.WriteString("t=5000512 VBAT=bad\n")
		case 800:
			y = -8
		}
		fmt.Fprintf(&records, "t=%d VBAT=%d\n", 5000000+i, y)
	}
	records.WriteString("t=9999999 VBAT=bad\nt=10000000 VBAT=-2\n")
	s := newController(&internal.App{Settings: config.Settings{Language: "en"}})
	s.curve = newCurveViewState(s.settings)
	s.curve.span = "All"
	s.curve.snapshot = curveTimeWindowSnapshot(t, records.String())
	s.curve.snapshot.Series[0].Color = 0xff0000ff
	tt := gui.NewTester(func(c *gui.Context) {
		gui.Column(c).Fill().Children(func() { s.curveChart(c) })
	}, 500, 300)
	chart, found := tt.Find("Serial curve chart")
	if !found {
		t.Fatal("plot is not visible")
	}
	plot := gui.Rect{X: chart.X + 66, Y: chart.Y + 14, W: chart.W - 82, H: chart.H - 48}
	bounds, image := s.curve.geometry.bounds, tt.Image()
	x := int(plot.X + plot.W/2)
	redAt := func(value float64) bool {
		y := int(plot.Y + plot.H - float32((value-bounds.y0)/(bounds.y1-bounds.y0))*plot.H)
		for dx := -2; dx <= 2; dx++ {
			pixel := image.RGBAAt(x+dx, y)
			if int(pixel.R)-int(pixel.G) > 30 && int(pixel.R)-int(pixel.B) > 30 {
				return true
			}
		}
		return false
	}
	if !redAt(7) || !redAt(-7) || redAt(0) {
		t.Fatal("summarized plot lost a spike or bridged a gap")
	}
}
