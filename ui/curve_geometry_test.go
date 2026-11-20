package ui

import (
	"bytes"
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
