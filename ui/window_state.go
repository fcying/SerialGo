package ui

import "github.com/egoist/mygo"

const mainWindowStateKey = "main"

func (s *controller) LoadWindowStates() (map[string]mygo.WindowState, error) {
	geometry := normalizeWindowGeometry(s.settings.Window)
	if !geometry.PositionSet {
		return nil, nil
	}
	return map[string]mygo.WindowState{
		mainWindowStateKey: {
			X: geometry.X, Y: geometry.Y,
			Width: geometry.Width, Height: geometry.Height,
			Maximized: geometry.Maximized,
			PixelX:    geometry.PixelX, PixelY: geometry.PixelY,
			PixelWidth: geometry.PixelWidth, PixelHeight: geometry.PixelHeight,
		},
	}, nil
}

func (s *controller) SaveWindowStates(states map[string]mygo.WindowState) error {
	state, ok := states[mainWindowStateKey]
	s.windowState = state
	s.hasWindowState = ok
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
