package ui

import (
	"fmt"
	"math"
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
		fraction := (x0 - a.X) / (b.X - a.X)
		start.X, start.Y = x0, (1-fraction)*a.Y+fraction*b.Y
	}
	if b.X > x1 {
		fraction := (x1 - a.X) / (b.X - a.X)
		end.X, end.Y = x1, (1-fraction)*a.Y+fraction*b.Y
	}
	return start, end, true
}

type curveVisibleSegments struct {
	points    []internal.CurvePoint
	x0, x1    float64
	nextIndex int
}

// next emits clipped segments or isolated real samples without copying the series.
// Breaks, invalid samples and backwards clocks all start a new path.
func (segments *curveVisibleSegments) next() (internal.CurvePoint, internal.CurvePoint, bool) {
	for segments.nextIndex < len(segments.points) {
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
	return internal.CurvePoint{}, internal.CurvePoint{}, false
}

func curvePointTime(seconds float64) time.Time {
	return time.UnixMilli(int64(math.Round(seconds * 1000))).Local()
}

// curveDeviceTimeFormat preserves the smallest possible tick interval (four ticks).
// Fixed decimals keep ordinary seconds readable; significant digits bound very
// large or small labels without rounding adjacent representable ticks together.
func curveDeviceTimeFormat(x0, x1 float64) (byte, int) {
	step := (x1 - x0) / 4
	if step == 0 {
		return 'g', 17
	}
	stepExponent := int(math.Floor(math.Log10(step)))
	decimals := max(0, -stepExponent)
	magnitude := max(math.Abs(x0), math.Abs(x1))
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
	// A sample can be much closer to its neighbors than two axis ticks.
	// Use the shortest round-trip precision rather than rounding to tick precision.
	format := byte('g')
	magnitude := math.Abs(seconds)
	if magnitude >= 1e-6 && magnitude < 1e12 {
		format = 'f'
	}
	return strconv.FormatFloat(seconds, format, -1, 64) + " s"
}

func (v *curveViewState) bounds() (curveBounds, bool) {
	bounds := curveBounds{x0: math.Inf(1), x1: math.Inf(-1), y0: math.Inf(1), y1: math.Inf(-1)}
	for _, series := range v.snapshot.Series {
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
	if span > 0 && v.position == 100 && bounds.x1 > bounds.x0 {
		// Follow the latest sample even when the device clock moves backwards.
		width := min(span, bounds.x1-bounds.x0)
		bounds.x1 = v.snapshot.LatestX
		bounds.x0 = bounds.x1 - width
	} else if span > 0 && bounds.x1-bounds.x0 > span {
		bounds.x1 = bounds.x0 + span + (bounds.x1-bounds.x0-span)*v.position/100
		bounds.x0 = bounds.x1 - span
	}
	if bounds.x1 == bounds.x0 {
		bounds.x1 = bounds.x0 + 1
	}
	for _, series := range v.snapshot.Series {
		segments := curveVisibleSegments{points: series.Points, x0: bounds.x0, x1: bounds.x1}
		for {
			start, end, visible := segments.next()
			if !visible {
				break
			}
			bounds.y0, bounds.y1 = min(bounds.y0, start.Y, end.Y), max(bounds.y1, start.Y, end.Y)
		}
	}
	if math.IsInf(bounds.y0, 1) {
		bounds.y0, bounds.y1 = 0, 1
	}
	padding := (bounds.y1 - bounds.y0) * .08
	if padding == 0 {
		padding = max(math.Abs(bounds.y0)*.05, .01)
	}
	bounds.y0 -= padding
	bounds.y1 += padding
	return bounds, true
}

func (s *controller) curveChart(c *ui.Context) {
	v := s.curve
	chart := ui.Box(c).FillWidth().Grow(1).MinHeight(64).Background(c.Theme().Surface).
		Border(1, c.Theme().Border).Radius(4).Cache().Label(s.tr("串口曲线图", "Serial curve chart"))
	pointerX, pointerY, hovered := chart.PointerPosition()
	chart.Draw(func(p *ui.Painter, r ui.Rect) {
		mouseX, mouseY := r.X+pointerX, r.Y+pointerY
		plot := ui.Rect{X: r.X + 66, Y: r.Y + 14, W: max(1, r.W-82), H: max(1, r.H-48)}
		v.prepareCurveGeometry(plot)
		bounds, hasData := v.geometry.bounds, v.geometry.hasData
		if !hasData && !v.snapshot.DeviceTime {
			span := curveSpanSeconds(v.span)
			if span == 0 {
				span = 60
			}
			bounds.x1 = float64(time.Now().UnixMilli()) / 1000
			bounds.x0 = bounds.x1 - span
		}
		xPixel := func(x float64) float32 { return plot.X + float32((x-bounds.x0)/(bounds.x1-bounds.x0))*plot.W }
		yPixel := func(y float64) float32 { return plot.Y + plot.H - float32((y-bounds.y0)/(bounds.y1-bounds.y0))*plot.H }
		yTicks := max(1, min(4, int(plot.H/16)))
		for i := 0; i <= yTicks; i++ {
			fraction := float64(i) / float64(yTicks)
			y := plot.Y + plot.H*float32(fraction)
			p.Line(plot.X, y, plot.X+plot.W, y, 1, c.Theme().Border)
			p.Text(r.X+4, y-6, fmt.Sprintf("%.5g", bounds.y1-fraction*(bounds.y1-bounds.y0)), 10, c.Theme().TextMuted)
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
			stamp := bounds.x0 + fraction*(bounds.x1-bounds.x0)
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
			x := bounds.x0 + float64((mouseX-plot.X)/plot.W)*(bounds.x1-bounds.x0)
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
				label := fmt.Sprintf("%s: %.6g %s @ %s", series.Name, point.Y, series.Unit, stamp)
				p.Fill(ui.Rect{X: plot.X + 4, Y: top, W: max(0, plot.W-8), H: 17}, c.Theme().Surface, 2)
				p.Text(plot.X+8, top+1, label, 11, color)
				p.Fill(ui.Rect{X: xPixel(point.X) - 3, Y: yPixel(point.Y) - 3, W: 6, H: 6}, color, 3)
				top += 18
			}
		}
	})
}
