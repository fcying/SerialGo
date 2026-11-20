package ui

import (
	"math"
	"slices"
	"sort"

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

type curvePixel struct{ x, y float32 }

// Draw dense segments in the same logical pixel column as one min/max stroke.
// Keep up to three sparse segments exact.
type curveColumnStroke struct {
	path      *ui.Path
	vertices  [4]curvePixel
	top, base curvePixel
	last      curvePixel
	column    int
	count     int
}

func (s *curveColumnStroke) line(a, b curvePixel) {
	if a != b {
		s.path.MoveTo(a.x, a.y).LineTo(b.x, b.y)
	}
}

func (s *curveColumnStroke) flush() {
	if s.count == 0 {
		return
	}
	if s.count <= len(s.vertices) {
		for i := 1; i < s.count; i++ {
			s.line(s.vertices[i-1], s.vertices[i])
		}
	} else if s.top.y == s.base.y {
		s.line(s.vertices[0], s.last)
	} else {
		s.line(s.top, s.base)
	}
	s.count = 0
}

func (s *curveColumnStroke) segment(a, b curvePixel) {
	column := int(a.x)
	if column != int(b.x) {
		s.flush()
		s.line(a, b)
		return
	}
	if s.count != 0 && (s.column != column || s.last != a) {
		s.flush()
	}
	if s.count == 0 {
		s.vertices[0] = a
		s.top, s.base = a, a
		s.column, s.count = column, 1
	}
	if s.count < len(s.vertices) {
		s.vertices[s.count] = b
	}
	if b.y < s.top.y {
		s.top = b
	}
	if b.y > s.base.y {
		s.base = b
	}
	s.last = b
	s.count++
}

func (s *curveColumnStroke) summary(first, a, b, last curvePixel) {
	s.segment(first, a)
	s.segment(a, b)
	s.segment(b, last)
	// These vertices represent a dense run, not three sparse segments.
	s.count = len(s.vertices) + 1
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
	projectX := func(x float64) float32 {
		return plot.X + float32((x-bounds.x0)/(bounds.x1-bounds.x0))*plot.W
	}
	column := func(x float64) int { return int(projectX(x)) }
	project := func(point internal.CurvePoint) curvePixel {
		return curvePixel{
			x: projectX(point.X),
			y: plot.Y + plot.H - float32((point.Y-bounds.y0)/(bounds.y1-bounds.y0))*plot.H,
		}
	}
	for i, series := range v.snapshot.Series {
		path := &v.paths[i]
		path.Reset()
		g.dots[i] = g.dots[i][:0]
		if !g.hasData {
			continue
		}
		segments := newCurveVisibleSegments(series, bounds.x0, bounds.x1)
		stroke := curveColumnStroke{path: path}
		prevIndex := 0
		for {
			start, end, visible := segments.next()
			if !visible {
				break
			}
			if segments.nextIndex != prevIndex+1 {
				stroke.flush()
			}
			prevIndex = segments.nextIndex
			a := project(start)
			if start.X == end.X && start.Y == end.Y {
				stroke.flush()
				dot := ui.Rect{X: a.x - 2, Y: a.y - 2, W: 4, H: 4}
				dots := g.dots[i]
				if len(dots) == 0 || dots[len(dots)-1] != dot {
					g.dots[i] = append(dots, dot)
				}
			} else {
				stroke.segment(a, project(end))
			}
			block, ok := segments.nextBlock(column)
			if ok {
				first := project(series.Points[block.Start])
				last := project(series.Points[block.End-1])
				a, b := block.Min, block.Max
				if a > b {
					a, b = b, a
				}
				stroke.summary(first, project(series.Points[a]), project(series.Points[b]), last)
				prevIndex = segments.nextIndex
			}
		}
		stroke.flush()
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
		g.closest[i] = closestCurvePoint(series, g.bounds.x0, g.bounds.x1, x)
	}
}

func closestCurvePoint(series internal.CurveSeries, x0, x1, x float64) int {
	points := series.Points
	closest, distance := -1, math.Inf(1)
	consider := func(i int) {
		d := math.Abs(points[i].X - x)
		if d < distance {
			closest, distance = i, d
		}
	}
	if len(series.TimeRuns) == 0 {
		for i, point := range points {
			if finiteCurvePoint(point) && point.X >= x0 && point.X <= x1 {
				consider(i)
			}
		}
		return closest
	}
	for i, start := range series.TimeRuns {
		end := curveTimeRunEnd(series.TimeRuns[i:], len(points))
		run := points[start:end]
		if run[0].X > x1 || run[len(run)-1].X < x0 {
			continue
		}
		lo, hi := curveTimeWindow(run, x0, x1)
		next := lo + sort.Search(hi-lo, func(j int) bool { return run[lo+j].X >= x })
		if next > lo {
			prevX := run[next-1].X
			// Prefer the earliest retained point on equal timestamps, as in a scan.
			prev := lo + sort.Search(next-lo, func(j int) bool { return run[lo+j].X >= prevX })
			consider(start + prev)
		}
		if next < hi {
			consider(start + next)
		}
	}
	return closest
}
