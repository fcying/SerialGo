package ui

import (
	"github.com/egoist/mygo/ui"
)

const (
	portSettingsFieldLabelWidth   float32 = 90
	portSettingsActionButtonWidth float32 = 76
)

func (s *controller) openPortSettings() {
	s.portDraft, s.portBaudRatesDraft = s.app.PortSettingsDraft()
	s.portSettingsError = localizedText{}
	s.portSettingsOpen = true
}

func (s *controller) applyPortSettings() {
	s.portSettingsError = localizedText{}
	if err := s.app.ApplyPortSettings(s.portDraft, s.portBaudRatesDraft); err != nil {
		s.portSettingsError = dialogError(err)
		return
	}
	s.portSettingsOpen = false
}

func (s *controller) portSettingsDialog(c *ui.Context) {
	wasOpen := s.portSettingsOpen
	ui.Modal(c.Key("port-settings-dialog"), &s.portSettingsOpen, func() {
		p := &s.portDraft
		open := s.app.Connected() || s.app.PortBusy
		baudWidth := s.portBaudPickerWidth(c)
		fieldRowWidth := portSettingsFieldLabelWidth + sectionGap + baudWidth
		actionRowWidth := 2*portSettingsActionButtonWidth + wideGap
		contentWidth := max(fieldRowWidth, actionRowWidth)
		ui.Column(c.Key("properties")).Width(contentWidth).Gap(sectionGap).Children(func() {
			ui.Text(c.Key("title"), s.tr("串口设置", "Port settings")).FontSize(16).FontWeight(600)
			ui.Scroll(c.Key("fields")).FillWidth().MaxHeight(240).Children(func() {
				ui.Column(c.Key("options")).FillWidth().Gap(wideGap).Children(func() {
					ui.Row(c.Key("baud")).FillWidth().Gap(sectionGap).AlignItems(ui.Center).Children(func() {
						ui.Text(c.Key("caption"), s.tr("波特率", "Baud rate")).Width(portSettingsFieldLabelWidth)
						s.baudPicker(c.Key("value"), true, 26)
					})
					portSettingSelect(c, "bits", s.tr("数据位", "Data bits"), &p.DataBits, dataBits, baudWidth, open)
					portSettingSelect(c, "stops", s.tr("停止位", "Stop bits"), &p.StopBits, stopBits, baudWidth, open)
					portSettingSelect(c, "parity", s.tr("校验位", "Parity"), &p.Parity, parities, baudWidth, open)
					ui.Row(c.Key("control-lines")).FillWidth().Gap(pairedControlGap).Children(func() {
						ui.Checkbox(c.Key("rts"), &p.RTS, "RTS")
						ui.Checkbox(c.Key("dtr"), &p.DTR, "DTR")
					})
					if open {
						ui.Text(c.Key("connected-hint"), s.tr("串口已打开, 仅可修改 RTS/DTR", "Port is open; only RTS/DTR can be changed")).FillWidth()
					}
					if s.portSettingsError.en != "" || s.portSettingsError.zh != "" {
						ui.Text(c.Key("error"), s.tr(s.portSettingsError.zh, s.portSettingsError.en)).FillWidth().TextColor(c.Theme().Danger)
					}
				})
			})
			ui.Row(c.Key("actions")).FillWidth().Gap(wideGap).Justify(ui.End).Children(func() {
				ui.Button(c.Key("ok"), s.tr("确定", "OK")).Width(portSettingsActionButtonWidth).Height(28).OnClick(s.applyPortSettings)
				ui.Button(c.Key("cancel"), s.tr("取消", "Cancel")).Width(portSettingsActionButtonWidth).Height(28).OnClick(func() {
					s.portSettingsOpen = false
					s.portDraft = s.settings.Port
				})
			})
		})
	}).Label(s.tr("串口设置", "Port settings"))
	if wasOpen && !s.portSettingsOpen {
		s.portDraft = s.settings.Port
	}
}

func portSettingSelect(c *ui.Context, id, label string, value *string, options []string, width float32, disabled bool) {
	ui.Row(c.Key(id)).FillWidth().Gap(sectionGap).AlignItems(ui.Center).Children(func() {
		ui.Text(c.Key("caption"), label).Width(portSettingsFieldLabelWidth)
		theme := c.Theme()
		sel := ui.SelectBase(c, value)
		trigger := sel.Trigger.Width(width).MinWidth(width).Grow(0).Shrink(0).Height(26).
			Padding(controlPadY, serialSelectorArrowPaddingX, controlPadY, 0).Gap(microGap).Radius(theme.Radius).Border(baudPickerBorderWidth, theme.Border).
			Background(theme.Surface).TextColor(theme.Text).AlignItems(ui.Center).Label(label).Disabled(disabled)
		if trigger.Pressed() {
			trigger.Background(theme.SurfacePressed)
		} else if trigger.Hovered() {
			trigger.Background(theme.SurfaceHover)
		}
		trigger.Children(func() {
			ui.Text(c.Key("selected"), *value).Width(0).MinWidth(0).Grow(1).Padding(0, serialSelectorPaddingX).SingleLine().TextAlign(ui.Center)
			ui.Box(c.Key("arrow")).Size(baudPickerArrowSize, baudPickerArrowSize).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
				var path ui.Path
				path.MoveTo(r.X+r.W*0.1, r.Y+r.H*0.3).LineTo(r.X+r.W*0.5, r.Y+r.H*0.7).LineTo(r.X+r.W*0.9, r.Y+r.H*0.3)
				p.StrokePath(&path, 1.5, theme.TextMuted)
			})
		})
		sel.Popup(func(panel ui.Element) {
			panel.Padding(3).Radius(theme.Radius).Background(theme.Background).Border(1, theme.Border).Children(func() {
				ui.Scroll(c.Key("options")).FillWidth().MaxHeight(240).Children(func() {
					ui.Column(c.Key("choices")).FillWidth().Gap(compactGap).Children(func() {
						for _, option := range options {
							item := sel.Item(option).Padding(4, 6).Radius(theme.Radius)
							if item.Highlighted() {
								item.Background(theme.Accent).TextColor(theme.AccentText)
							} else if option == *value {
								item.Background(theme.Surface)
							}
							item.Children(func() { ui.Text(c.Key("label"), option).SingleLine() })
						}
					})
				})
			})
		})
	})
}
