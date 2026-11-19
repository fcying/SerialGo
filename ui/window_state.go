package ui

import (
	"github.com/egoist/mygo"
	"github.com/fcying/SerialGo/config"
)

const (
	mainWindowStateKey  = "main"
	curveWindowStateKey = "serial-plot"
)

func (s *controller) LoadWindowStates() (map[string]mygo.WindowState, error) {
	curve := s.settings.CurveWindow
	hasCurveState := curve.PositionSet && curve.Width > 0 && curve.Height > 0
	main := normalizeWindowGeometry(s.settings.Window)
	if !main.PositionSet && !hasCurveState {
		return nil, nil
	}
	states := make(map[string]mygo.WindowState, 2)
	if main.PositionSet {
		states[mainWindowStateKey] = windowStateFromGeometry(main)
	}
	if hasCurveState {
		curve.Width = max(curve.Width, curveWindowMinWidth)
		curve.Height = max(curve.Height, curveWindowMinHeight)
		states[curveWindowStateKey] = windowStateFromGeometry(curve)
	}
	return states, nil
}

func windowStateFromGeometry(geometry config.WindowSettings) mygo.WindowState {
	return mygo.WindowState{
		X: geometry.X, Y: geometry.Y,
		Width: geometry.Width, Height: geometry.Height,
		Maximized: geometry.Maximized,
		PixelX:    geometry.PixelX, PixelY: geometry.PixelY,
		PixelWidth: geometry.PixelWidth, PixelHeight: geometry.PixelHeight,
	}
}

func (s *controller) SaveWindowStates(states map[string]mygo.WindowState) error {
	s.windowState, s.hasWindowState = states[mainWindowStateKey]
	state, ok := states[curveWindowStateKey]
	if ok && state.Width > 0 && state.Height > 0 {
		s.app.SetCurveWindowGeometry(config.WindowSettings{
			X: state.X, Y: state.Y, Width: state.Width, Height: state.Height,
			PixelX: state.PixelX, PixelY: state.PixelY, PixelWidth: state.PixelWidth, PixelHeight: state.PixelHeight,
			Maximized: state.Maximized, PositionSet: true,
		})
	}
	return nil
}

func (s *controller) syncWindowState() {
	if !s.hasWindowState {
		return
	}
	state := s.windowState
	s.windowGeometry.Maximized = state.Maximized
	if !s.windowGeometry.PositionSet || state.Width <= 0 || state.Height <= 0 {
		return
	}
	s.windowGeometry.X, s.windowGeometry.Y = state.X, state.Y
	s.windowGeometry.Width, s.windowGeometry.Height = state.Width, state.Height
	s.windowGeometry.PixelX, s.windowGeometry.PixelY = state.PixelX, state.PixelY
	s.windowGeometry.PixelWidth, s.windowGeometry.PixelHeight = state.PixelWidth, state.PixelHeight
}
