package ui

import "github.com/egoist/mygo/ui"

func (s *controller) portPicker(c *ui.Context, refresh func()) {
	// Keep the hover anchor enabled while the selector is locked.
	ui.Row(c.Key("port-tooltip")).Width(0).MinWidth(0).Grow(1).Tooltip(s.portChoice).Children(func() {
		connected := s.session != nil
		sel := ui.SelectBaseOpen(c, &s.portChoice, &s.portMenuOpen)
		if connected {
			sel.SetOpen(false)
		}
		if sel.Open() && !s.portMenuRefreshed {
			s.portMenuRefreshed = true
			refresh()
		}
		theme := c.Theme()
		trigger := sel.Trigger.Width(0).MinWidth(0).Grow(1).Height(controlHeight).
			Padding(controlPadY, 3).Gap(4).Radius(theme.Radius).Border(1, theme.Border).
			Background(theme.Surface).TextColor(theme.Text).AlignItems(ui.Center).
			Label(s.tr("端口号", "Port")).Value(s.portChoice).Tooltip(s.portChoice).
			Disabled(connected).OnChange(s.selectPort)
		if !connected {
			if trigger.Pressed() {
				trigger.Background(theme.SurfacePressed)
			} else if trigger.Hovered() {
				trigger.Background(theme.SurfaceHover)
			}
		}
		trigger.Children(func() {
			label := s.portChoice
			if label == "" {
				label = s.tr("选择串口", "Select port")
			}
			ui.Text(c.Key("selected"), label).Width(0).MinWidth(0).Grow(1).SingleLine()
			ui.Box(c.Key("arrow")).Size(8, 8).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
				var path ui.Path
				path.MoveTo(r.X+r.W*0.1, r.Y+r.H*0.3).LineTo(r.X+r.W*0.5, r.Y+r.H*0.7).LineTo(r.X+r.W*0.9, r.Y+r.H*0.3)
				p.StrokePath(&path, 1.5, theme.TextMuted)
			})
		})
		sel.Popup(func(panel ui.Element) {
			panel.Padding(3).Radius(theme.Radius).Background(theme.Background).Border(1, theme.Border).Children(func() {
				ui.Scroll(c.Key("port-options")).FillWidth().MaxHeight(240).Children(func() {
					ui.Column(c.Key("choices")).FillWidth().Gap(2).Children(func() {
						if len(s.portLabels) == 0 {
							ui.Text(c.Key("empty"), s.tr("未找到串口", "No serial ports found")).Padding(4, 6).SingleLine()
						}
						for _, label := range s.portLabels {
							item := sel.Item(label).Padding(4, 6).Radius(theme.Radius).Tooltip(label)
							if item.Highlighted() {
								item.Background(theme.Accent).TextColor(theme.AccentText)
							} else if label == s.portChoice {
								item.Background(theme.Surface)
							}
							item.Children(func() { ui.Text(c.Key("label"), label).SingleLine() })
						}
					})
				})
			})
		})
		if !sel.Open() {
			s.portMenuRefreshed = false
		}
	})
}
