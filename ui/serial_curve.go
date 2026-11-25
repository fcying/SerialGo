package ui

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const (
	curveWindowMinWidth  = 720
	curveWindowMinHeight = 260
	curveRefreshInterval = time.Second / 60
)

func (s *controller) serialCurveTitle() string {
	return s.tr("串口曲线", "Serial plot")
}

func (s *controller) toolsMenu(c *ui.Context) {
	label := s.tr("工具", "Tools")
	ui.Button(c, "⋯").Size(controlHeight, controlHeight).Shrink(0).Padding(controlPadY, 0).
		Label(label).Tooltip(label).PopoverMenuButton(func(m *ui.Menu) {
		if m.Item(s.serialCurveTitle()).Chosen() {
			s.openSerialCurve()
		}
	})
}

func (s *controller) openSerialCurve() {
	if s.curveWindow != nil {
		if s.curveWindow.IsMinimized() {
			s.curveWindow.Restore()
		}
		s.curveWindow.Focus()
		return
	}
	s.curve = newCurveViewState(s.settings)
	width, height := s.settings.CurveWindow.Width, s.settings.CurveWindow.Height
	if width <= 0 {
		width = 1040
	}
	if height <= 0 {
		height = 600
	}
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: s.serialCurveTitle(), Parent: s.window,
		Width: max(width, curveWindowMinWidth), Height: max(height, curveWindowMinHeight),
		MinWidth: curveWindowMinWidth, MinHeight: curveWindowMinHeight, StateKey: curveWindowStateKey,
		Content: ui.View(s.serialCurveView),
	})
	s.curveWindow = w
	done := make(chan struct{})
	w.OnClose(func(e *mygo.CloseEvent) {
		s.confirmSerialCurveClose(e, func() { w.Destroy() })
		if e.DefaultPrevented() {
			w.Invalidate()
		}
	})
	w.OnClosed(func() {
		close(done)
		s.closeSerialCurve(w)
	})
	go func() {
		ticker := time.NewTicker(curveRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				mygo.RunOnMain(func() {
					changed := s.pollSerialCurve(w)
					if changed {
						w.Invalidate()
					}
				})
			}
		}
	}()
}

// confirmSerialCurveClose uses the same discard confirmation as profile switches.
// Destroy bypasses OnClose after the user explicitly confirms discarding the draft.
func (s *controller) confirmSerialCurveClose(e *mygo.CloseEvent, closeWindow func()) {
	if s.curve == nil || !s.curve.dirty {
		return
	}
	e.PreventDefault()
	s.curveDiscardThen(closeWindow)
}

func (s *controller) closeSerialCurve(w *mygo.Window) {
	if s.curveWindow != w {
		return
	}
	// Retire this window before any already queued poll can update its model.
	s.curveWindow = nil
	s.closeCurveRuleEditor()
	s.closeCurveProfileEditor()
	if s.curve.testWindow != nil {
		s.curve.testWindow.Close()
	}
	s.curve = nil
	s.app.CloseCurve()
	// Closing is a save boundary even if MyGo did not report new window bounds.
	s.app.SaveUsedSettings()
}

func (s *controller) pollSerialCurve(w *mygo.Window) bool {
	if s.curveWindow != w {
		return false
	}
	v := s.curve
	connected := s.app.Connected()
	changed := connected != v.connected || s.app.Status != v.status || s.app.StatusError != v.statusError
	v.connected = connected
	v.status, v.statusError = s.app.Status, s.app.StatusError
	if v.running && !v.paused && !v.previewing {
		revision := v.snapshot.Revision
		statsRevision := v.snapshot.StatsRevision
		s.app.CurveLiveSnapshot(&v.snapshot, &v.liveCache)
		changed = changed || revision != v.snapshot.Revision || statsRevision != v.snapshot.StatsRevision
	}
	return changed
}
