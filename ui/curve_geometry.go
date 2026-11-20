package ui

import (
	"math"
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/fcying/SerialGo/internal"
)

type curveGeometryKey struct {
	revision uint64
	series   *internal.CurveSeries
	count    int
	preview  bool
	span     string
	position float64
	plot     ui.Rect
}

type curveGeometry struct {
	key        curveGeometryKey
	valid      bool
	bounds     curveBounds
	hasData    bool
	dots       [][]ui.Rect
	hoverValid bool
	hoverX     float32
	closest    []int
}

// Statistics-only frames reuse bounds, clipped paths and isolated sample dots.
// Revision zero is an explicitly uncached snapshot, used before acquisition.
func (v *curveViewState) prepareCurveGeometry(plot ui.Rect) {
	key := curveGeometryKey{
		revision: v.snapshot.Revision, count: len(v.snapshot.Series), preview: v.previewing,
		span: v.span, position: v.position, plot: plot,
	}
	if len(v.snapshot.Series) != 0 {
		key.series = &v.snapshot.Series[0]
	}
	g := &v.geometry
	if g.valid && key.revision != 0 && g.key == key {
		return
	}
	g.key, g.valid, g.hoverValid = key, true, false
	g.bounds, g.hasData = v.bounds()
	if len(v.paths) < len(v.snapshot.Series) {
		v.paths = slices.Grow(v.paths, len(v.snapshot.Series)-len(v.paths))
		v.paths = v.paths[:len(v.snapshot.Series)]
	}
	if len(g.dots) < len(v.snapshot.Series) {
		g.dots = slices.Grow(g.dots, len(v.snapshot.Series)-len(g.dots))
		g.dots = g.dots[:len(v.snapshot.Series)]
	}
	bounds := g.bounds
	for i, series := range v.snapshot.Series {
		path := &v.paths[i]
		path.Reset()
		g.dots[i] = g.dots[i][:0]
		if !g.hasData {
			continue
		}
		segments := curveVisibleSegments{points: series.Points, x0: bounds.x0, x1: bounds.x1}
		for {
			start, end, visible := segments.next()
			if !visible {
				break
			}
			x := plot.X + float32((start.X-bounds.x0)/(bounds.x1-bounds.x0))*plot.W
			y := plot.Y + plot.H - float32((start.Y-bounds.y0)/(bounds.y1-bounds.y0))*plot.H
			if start.X == end.X && start.Y == end.Y {
				g.dots[i] = append(g.dots[i], ui.Rect{X: x - 2, Y: y - 2, W: 4, H: 4})
			} else {
				endX := plot.X + float32((end.X-bounds.x0)/(bounds.x1-bounds.x0))*plot.W
				endY := plot.Y + plot.H - float32((end.Y-bounds.y0)/(bounds.y1-bounds.y0))*plot.H
				path.MoveTo(x, y).LineTo(endX, endY)
			}
		}
	}
}

// A stationary pointer must not rescan every sample when only counters change.
func (v *curveViewState) prepareCurveHover(mouseX float32, x float64) {
	g := &v.geometry
	if g.hoverValid && g.hoverX == mouseX {
		return
	}
	g.hoverValid, g.hoverX = true, mouseX
	if cap(g.closest) < len(v.snapshot.Series) {
		g.closest = make([]int, len(v.snapshot.Series))
	} else {
		g.closest = g.closest[:len(v.snapshot.Series)]
	}
	for i, series := range v.snapshot.Series {
		closest, distance := -1, math.Inf(1)
		for j, point := range series.Points {
			if !finiteCurvePoint(point) || point.X < g.bounds.x0 || point.X > g.bounds.x1 {
				continue
			}
			d := math.Abs(point.X - x)
			if d < distance {
				closest, distance = j, d
			}
		}
		g.closest[i] = closest
	}
}
