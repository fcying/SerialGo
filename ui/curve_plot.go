package ui

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/fcying/SerialGo/internal"
)

func curveColor(rgba uint32) ui.Color {
	return ui.Color{R: uint8(rgba >> 24), G: uint8(rgba >> 16), B: uint8(rgba >> 8), A: uint8(rgba)}
}

type curveBounds struct{ x0, x1, y0, y1 float64 }

func finiteCurvePoint(point internal.CurvePoint) bool {
	return !math.IsNaN(point.X) && !math.IsInf(point.X, 0) && !math.IsNaN(point.Y) && !math.IsInf(point.Y, 0)
}

// curveFraction and curveLerp avoid overflowing the difference of finite endpoints.
func curveFraction(value, low, high float64) float64 {
	width := high - low
	if math.IsInf(width, 0) {
		return (value/2 - low/2) / (high/2 - low/2)
	}
	return (value - low) / width
}

func curveLerp(low, high, fraction float64) float64 {
	if fraction == 0 {
		return low
	}
	if fraction == 1 {
		return high
	}
	width := high - low
	if math.IsInf(width, 0) {
		return (1-fraction)*low + fraction*high
	}
	if fraction > .5 {
		return high - (1-fraction)*width
	}
	return low + fraction*width
}

// Interpolate from the closer X endpoint so a tiny distance is not lost by
// rounding a fraction near one.
func curveIntersectionY(a, b internal.CurvePoint, x float64) float64 {
	from, to, distance := a.Y, b.Y, x-a.X
	if distance > b.X-x {
		from, to, distance = b.Y, a.Y, b.X-x
	}
	width := b.X - a.X
	widthExp := 0
	if math.IsInf(width, 0) {
		width = b.X/2 - a.X/2
		widthExp = 1
	}
	fraction := (distance / width) * math.Ldexp(1, -widthExp)
	if fraction >= 0x1p-1022 || distance == 0 {
		return curveLerp(from, to, fraction)
	}
	// A subnormal fraction can lose significant bits (or underflow to zero)
	// before multiplication by a large Y range. Scale the product instead.
	dy := to - from
	dyExp := 0
	if math.IsInf(dy, 0) {
		dy = to/2 - from/2
		dyExp = 1
	}
	dm, de := math.Frexp(distance)
	wm, we := math.Frexp(width)
	ym, ye := math.Frexp(dy)
	delta := math.Ldexp((dm*ym)/wm, de+ye+dyExp-we-widthExp)
	return from + delta
}

func curveFinite(value float64) float64 {
	return max(-math.MaxFloat64, min(math.MaxFloat64, value))
}

// clipCurveSegment clips a forward segment to the time window, including verticals.
func clipCurveSegment(a, b internal.CurvePoint, x0, x1 float64) (internal.CurvePoint, internal.CurvePoint, bool) {
	if b.X < x0 || a.X > x1 {
		return internal.CurvePoint{}, internal.CurvePoint{}, false
	}
	if a.X == b.X {
		return a, b, true
	}
	start, end := a, b
	if a.X < x0 {
		start.X, start.Y = x0, curveIntersectionY(a, b, x0)
	}
	if b.X > x1 {
		end.X, end.Y = x1, curveIntersectionY(a, b, x1)
	}
	return start, end, true
}

type curveVisibleSegments struct {
	points         []internal.CurvePoint
	blocks         []internal.CurveBlock
	runs           []int
	x0, x1         float64
	nextIndex, end int
}

func newCurveVisibleSegments(series internal.CurveSeries, x0, x1 float64) curveVisibleSegments {
	segments := curveVisibleSegments{points: series.Points, runs: series.TimeRuns, blocks: series.Blocks, x0: x0, x1: x1}
	if len(segments.runs) == 0 {
		segments.end = len(segments.points)
	}
	return segments
}

func curveTimeRunEnd(runs []int, pointCount int) int {
	if len(runs) > 1 {
		return runs[1]
	}
	return pointCount
}

func curveTimeWindow(points []internal.CurvePoint, x0, x1 float64) (first, end int) {
	first = sort.Search(len(points), func(i int) bool { return points[i].X >= x0 })
	end = first + sort.Search(len(points)-first, func(i int) bool { return points[first+i].X > x1 })
	return first, end
}

func (segments *curveVisibleSegments) nextRun() bool {
	for len(segments.runs) != 0 {
		lo, hi := segments.runs[0], curveTimeRunEnd(segments.runs, len(segments.points))
		segments.runs = segments.runs[1:]
		points := segments.points[lo:hi]
		if points[0].X > segments.x1 || points[len(points)-1].X < segments.x0 {
			continue
		}
		first, end := curveTimeWindow(points, segments.x0, segments.x1)
		segments.nextIndex = lo + first
		// The existing segment logic reads the predecessor. Visit one point
		// to the right as well, including a segment spanning the entire window.
		segments.end = min(lo+end+1, hi)
		return true
	}
	return false
}

// nextBlock consumes the largest complete summary starting at the point just
// handled by next. A nil column allows any width (for exact bounds); rendering
// requires both endpoints to project to the same column. The summary's Xs are
// nondecreasing, so these endpoints also bound every point it would skip.
func (segments *curveVisibleSegments) nextBlock(column func(float64) int) (internal.CurveBlock, bool) {
	start := segments.nextIndex - 1
	blocks := segments.blocks
	if len(blocks) != 0 && blocks[0].Start < start {
		// Jump over off-window blocks and descendants of a consumed summary.
		i := sort.Search(len(blocks), func(i int) bool { return blocks[i].Start >= start })
		blocks = blocks[i:]
		segments.blocks = blocks
	}
	if len(blocks) == 0 || blocks[0].Start != start || segments.points[start].X < segments.x0 {
		return internal.CurveBlock{}, false
	}
	var firstColumn int
	if column != nil {
		firstColumn = column(segments.points[start].X)
	}
	for i, b := range blocks {
		if b.Start != start {
			break
		}
		segments.blocks = blocks[i+1:]
		if b.End > segments.end || segments.points[b.End-1].X > segments.x1 {
			continue
		}
		if column != nil && firstColumn != column(segments.points[b.End-1].X) {
			continue
		}
		segments.nextIndex = b.End
		return b, true
	}
	return internal.CurveBlock{}, false
}

// next emits clipped segments or isolated real samples without copying the series.
// Breaks, invalid samples and backwards clocks all start a new path.
func (segments *curveVisibleSegments) next() (internal.CurvePoint, internal.CurvePoint, bool) {
	for {
		if segments.nextIndex == segments.end && !segments.nextRun() {
			return internal.CurvePoint{}, internal.CurvePoint{}, false
		}
		index := segments.nextIndex
		segments.nextIndex++
		point := segments.points[index]
		if !finiteCurvePoint(point) {
			continue
		}
		if index > 0 && !point.Break {
			previous := segments.points[index-1]
			if finiteCurvePoint(previous) && point.X >= previous.X {
				start, end, visible := clipCurveSegment(previous, point, segments.x0, segments.x1)
				if visible {
					return start, end, true
				}
				continue
			}
		}
		if point.X >= segments.x0 && point.X <= segments.x1 {
			return point, point, true
		}
	}
}

func curvePointTime(seconds float64) time.Time {
	return time.UnixMilli(int64(math.Round(seconds * 1000))).Local()
}

// curveDeviceTimeFormat preserves the smallest possible tick interval (four ticks).
// Fixed decimals keep ordinary seconds readable; significant digits bound very
// large or small labels without rounding adjacent representable ticks together.
func curveDeviceTimeFormat(x0, x1 float64) (byte, int) {
	step := (x1 - x0) / 4
	if math.IsInf(step, 0) {
		step = x1/4 - x0/4
	}
	if step == 0 {
		return 'g', 17
	}
	stepExponent := int(math.Floor(math.Log10(step)))
	decimals := max(0, -stepExponent)
	magnitude := max(math.Abs(x0), math.Abs(x1))
	// Avoid rounding finite endpoints near MaxFloat64 to an infinite value.
	if magnitude >= 1e308 {
		return 'g', 17
	}
	if decimals <= 6 && magnitude < 1e12 {
		return 'f', decimals
	}
	digits := int(math.Floor(math.Log10(magnitude))) - stepExponent + 2
	return 'g', max(1, min(17, digits))
}

func curveHoverTime(seconds float64, deviceTime bool) string {
	if !deviceTime {
		return curvePointTime(seconds).Format("15:04:05.000")
	}
	return curveHoverValue(seconds) + " s"
}

func curveHoverValue(value float64) string {
	// Show ordinary values in decimal while preserving the shortest round-trip representation.
	format := byte('g')
	magnitude := math.Abs(value)
	if magnitude >= 1e-6 && magnitude < 1e12 {
		format = 'f'
	}
	return strconv.FormatFloat(value, format, -1, 64)
}

func (v *curveViewState) bounds() (curveBounds, bool) {
	bounds := curveBounds{x0: math.Inf(1), x1: math.Inf(-1), y0: math.Inf(1), y1: math.Inf(-1)}
	for _, series := range v.snapshot.Series {
		if len(series.TimeRuns) != 0 {
			for i, first := range series.TimeRuns {
				end := curveTimeRunEnd(series.TimeRuns[i:], len(series.Points))
				bounds.x0 = min(bounds.x0, series.Points[first].X)
				bounds.x1 = max(bounds.x1, series.Points[end-1].X)
			}
			continue
		}
		for _, point := range series.Points {
			if !finiteCurvePoint(point) {
				continue
			}
			bounds.x0, bounds.x1 = min(bounds.x0, point.X), max(bounds.x1, point.X)
		}
	}
	if math.IsInf(bounds.x0, 1) {
		return curveBounds{0, 1, 0, 1}, false
	}
	span := curveSpanSeconds(v.span)
	followingRange := span > 0 && v.position == 100 && bounds.x1 > bounds.x0
	if followingRange {
		// Follow the latest sample even when the device clock moves backwards.
		width := min(span, bounds.x1-bounds.x0)
		bounds.x1 = v.snapshot.LatestX
		bounds.x0 = curveFinite(bounds.x1 - width)
	} else if span > 0 && bounds.x1-bounds.x0 > span {
		width := bounds.x1 - bounds.x0
		shift := (width - span) * v.position / 100
		if math.IsInf(shift, 0) || math.IsNaN(shift) {
			bounds.x1 = curveLerp(bounds.x0+span, bounds.x1, v.position/100)
		} else {
			bounds.x1 = bounds.x0 + span + shift
		}
		bounds.x0 = curveFinite(bounds.x1 - span)
	}
	if bounds.x1 == bounds.x0 {
		if followingRange {
			bounds.x0 = math.Nextafter(bounds.x1, math.Inf(-1))
		}
		if math.IsInf(bounds.x0, -1) || bounds.x0 == bounds.x1 {
			bounds.x0 = bounds.x1
			bounds.x1 = bounds.x0 + 1
			if bounds.x1 == bounds.x0 {
				bounds.x1 = math.Nextafter(bounds.x0, math.Inf(1))
				if math.IsInf(bounds.x1, 1) {
					bounds.x1 = bounds.x0
					bounds.x0 = math.Nextafter(bounds.x1, math.Inf(-1))
				}
			}
		}
	}
	for _, series := range v.snapshot.Series {
		segments := newCurveVisibleSegments(series, bounds.x0, bounds.x1)
		for {
			start, end, visible := segments.next()
			if !visible {
				break
			}
			bounds.y0, bounds.y1 = min(bounds.y0, start.Y, end.Y), max(bounds.y1, start.Y, end.Y)
			block, ok := segments.nextBlock(nil)
			if ok {
				bounds.y0 = min(bounds.y0, series.Points[block.Min].Y)
				bounds.y1 = max(bounds.y1, series.Points[block.Max].Y)
			}
		}
	}
	if math.IsInf(bounds.y0, 1) {
		bounds.y0, bounds.y1 = 0, 1
	}
	padding := (bounds.y1 - bounds.y0) * .08
	if math.IsInf(padding, 0) {
		padding = bounds.y1*.08 - bounds.y0*.08
	}
	if padding == 0 {
		padding = max(math.Abs(bounds.y0)*.05, .01)
	}
	bounds.y0 = curveFinite(bounds.y0 - padding)
	bounds.y1 = curveFinite(bounds.y1 + padding)
	return bounds, true
}

// curveYLabels uses one precision for the axis, increasing it only when
// distinct representable ticks would otherwise receive the same label.
func curveYLabels(bounds curveBounds, ticks int) [5]string {
	var values [5]float64
	var labels [5]string
	for i := 0; i <= ticks; i++ {
		values[i] = curveLerp(bounds.y1, bounds.y0, float64(i)/float64(ticks))
	}
	precision := 5
	if max(math.Abs(bounds.y0), math.Abs(bounds.y1)) >= 1e308 {
		precision = 17
	}
	for {
		distinct := true
		for i := 0; i <= ticks; i++ {
			labels[i] = strconv.FormatFloat(values[i], 'g', precision, 64)
			if i > 0 && values[i] != values[i-1] && labels[i] == labels[i-1] {
				distinct = false
			}
		}
		if distinct || precision == 17 {
			return labels
		}
		precision++
	}
}

func (s *controller) curveChart(c *ui.Context) {
	v := s.curve
	chart := ui.Box(c).FillWidth().Grow(1).MinHeight(64).Background(c.Theme().Surface).
		Border(1, c.Theme().Border).Radius(4).Cache().Label(s.tr("串口曲线图", "Serial curve chart"))
	pointerX, pointerY, hovered := chart.PointerPosition()
	chart.Draw(func(p *ui.Painter, r ui.Rect) {
		mouseX, mouseY := r.X+pointerX, r.Y+pointerY
		bounds, hasData := v.curveLayoutBounds()
		height := max(1, r.H-48)
		yTicks := max(1, min(4, int(height/16)))
		yLabels := curveYLabels(bounds, yTicks)
		labelWidth := float32(0)
		for i := 0; i <= yTicks; i++ {
			width, _ := p.MeasureText(0, ui.Span{Text: yLabels[i], Size: 10})
			labelWidth = max(labelWidth, width)
		}
		right := min(float32(16), max(0, r.W-1))
		minPlotWidth := min(float32(40), max(1, r.W-right))
		left := min(max(float32(66), labelWidth+8), max(0, r.W-right-minPlotWidth))
		plot := ui.Rect{X: r.X + left, Y: r.Y + 14, W: max(1, r.W-left-right), H: height}
		v.prepareCurveGeometryWithBounds(plot, bounds, hasData)
		if !hasData && !v.snapshot.DeviceTime {
			span := curveSpanSeconds(v.span)
			if span == 0 {
				span = 60
			}
			bounds.x1 = float64(time.Now().UnixMilli()) / 1000
			bounds.x0 = bounds.x1 - span
		}
		xPixel := func(x float64) float32 { return plot.X + float32(curveFraction(x, bounds.x0, bounds.x1))*plot.W }
		yPixel := func(y float64) float32 {
			return plot.Y + plot.H - float32(curveFraction(y, bounds.y0, bounds.y1))*plot.H
		}
		for i := 0; i <= yTicks; i++ {
			fraction := float64(i) / float64(yTicks)
			y := plot.Y + plot.H*float32(fraction)
			p.Line(plot.X, y, plot.X+plot.W, y, 1, c.Theme().Border)
			if left > 8 {
				p.Clip(ui.Rect{X: r.X + 4, Y: r.Y, W: left - 8, H: r.H}, 0, func() {
					p.Text(r.X+4, y-6, yLabels[i], 10, c.Theme().TextMuted)
				})
			}
		}
		clockLayout, sample := "15:04:05", "00:00:00"
		if bounds.x1-bounds.x0 < 10 {
			clockLayout, sample = "15:04:05.000", "00:00:00.000"
		}
		deviceFormat, devicePrecision := byte('f'), 0
		var deviceStart string
		if v.snapshot.DeviceTime {
			deviceFormat, devicePrecision = curveDeviceTimeFormat(bounds.x0, bounds.x1)
			deviceStart = strconv.FormatFloat(bounds.x0, deviceFormat, devicePrecision, 64)
			sample = strconv.FormatFloat(bounds.x1, deviceFormat, devicePrecision, 64)
		}
		clockWidth, _ := p.MeasureText(0, ui.Span{Text: sample, Size: 10})
		if v.snapshot.DeviceTime {
			startWidth, _ := p.MeasureText(0, ui.Span{Text: deviceStart, Size: 10})
			clockWidth = max(clockWidth, startWidth)
		}
		ticks := max(1, min(4, int(plot.W/(clockWidth+16))))
		nextLabelX := r.X + r.W
		for i := ticks; i >= 0; i-- {
			fraction := float64(i) / float64(ticks)
			x := plot.X + plot.W*float32(fraction)
			p.Line(x, plot.Y, x, plot.Y+plot.H, 1, c.Theme().Border)
			labelX := max(r.X+4, min(x-clockWidth/2, r.X+r.W-clockWidth-4))
			if i != ticks && labelX+clockWidth+4 > nextLabelX {
				continue
			}
			nextLabelX = labelX
			stamp := curveLerp(bounds.x0, bounds.x1, fraction)
			var label string
			if v.snapshot.DeviceTime {
				switch i {
				case 0:
					label = deviceStart
				case ticks:
					label = sample
				default:
					label = strconv.FormatFloat(stamp, deviceFormat, devicePrecision, 64)
				}
			} else {
				label = curvePointTime(stamp).Format(clockLayout)
			}
			p.RichText(labelX, plot.Y+plot.H+8, 0, ui.Span{Text: label, Size: 10, Color: c.Theme().TextMuted})
		}
		axisLabel := s.tr("系统时间", "System time")
		if v.snapshot.DeviceTime {
			axisLabel = s.tr("设备时间 (s)", "Device time (s)")
		}
		p.Text(plot.X, plot.Y+plot.H+22, axisLabel, 10, c.Theme().TextMuted)
		if !hasData {
			messageY := plot.Y + min(20, max(0, (plot.H-14)/2))
			message := s.tr("开始采集, 或粘贴日志预览", "Start acquisition or preview pasted logs")
			if r.W < curveCompactChartWidth {
				message = s.tr("无数据", "No data")
			}
			p.Text(plot.X+10, messageY, message, 12, c.Theme().TextMuted)
			return
		}
		p.Clip(plot, 0, func() {
			for i, series := range v.snapshot.Series {
				color := curveColor(series.Color)
				for _, dot := range v.geometry.dots[i] {
					p.Fill(dot, color, 2)
				}
				p.StrokePath(&v.paths[i], 1.5, color)
			}
		})
		if hovered && mouseX >= plot.X && mouseX <= plot.X+plot.W && mouseY >= plot.Y && mouseY <= plot.Y+plot.H {
			x := curveLerp(bounds.x0, bounds.x1, float64((mouseX-plot.X)/plot.W))
			v.prepareCurveHover(mouseX, x)
			p.Line(mouseX, plot.Y, mouseX, plot.Y+plot.H, 1, c.Theme().TextMuted)
			top := plot.Y + 4
			for i, series := range v.snapshot.Series {
				closest := v.geometry.closest[i]
				if closest < 0 {
					continue
				}
				point := series.Points[closest]
				color := curveColor(series.Color)
				stamp := curveHoverTime(point.X, v.snapshot.DeviceTime)
				label := fmt.Sprintf("%s: %s %s @ %s", series.Name, curveHoverValue(point.Y), series.Unit, stamp)
				p.Fill(ui.Rect{X: plot.X + 4, Y: top, W: max(0, plot.W-8), H: 17}, c.Theme().Surface, 2)
				p.Text(plot.X+8, top+1, label, 11, color)
				p.Fill(ui.Rect{X: xPixel(point.X) - 3, Y: yPixel(point.Y) - 3, W: 6, H: 6}, color, 3)
				top += 18
			}
		}
	})
}
