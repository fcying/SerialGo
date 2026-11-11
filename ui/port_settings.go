package ui

import (
	"slices"

	"github.com/fcying/SerialGo/internal/i18n"

	"github.com/egoist/mygo/ui"
)

func (s *controller) openPortSettings() {
	s.portDraft = s.settings.Port
	s.portBaudRatesDraft = slices.Clone(s.settings.BaudRates)
	s.portSettingsError = localizedText{}
	s.portSettingsOpen = true
}

func (s *controller) applyPortSettings() {
	previous := s.settings.Port
	next := s.portDraft
	next.Name = previous.Name
	nextBaudRates := s.portBaudRatesDraft
	s.portSettingsError = localizedText{}
	if s.session != nil {
		// Framing cannot change while the current session owns the port.
		next.Baud = previous.Baud
		next.DataBits = previous.DataBits
		next.Parity = previous.Parity
		next.StopBits = previous.StopBits
		nextBaudRates = s.settings.BaudRates
		if next.DTR != previous.DTR || next.RTS != previous.RTS {
			err := s.session.SetLines(next.DTR, next.RTS)
			if err != nil {
				s.portSettingsError = localizedText{
					zh: "设置控制线失败: " + i18n.ErrorText(err, false),
					en: "Failed to set control lines: " + i18n.ErrorText(err, true),
				}
				return
			}
		}
	}
	if next != previous || !slices.Equal(nextBaudRates, s.settings.BaudRates) {
		s.settings.Port = next
		s.settings.BaudRates = slices.Clone(nextBaudRates)
		s.settingsChanged()
		s.note("串口设置已更新", "Port settings updated")
		s.saveUsedSettings()
	}
	s.portSettingsOpen = false
}

func (s *controller) portSettingsDialog(c *ui.Context) {
	ui.Modal(c.Key("port-settings-dialog"), &s.portSettingsOpen, func() {
		p := &s.portDraft
		open := s.session != nil
		ui.Column(c.Key("properties")).Width(340).Gap(10).Children(func() {
			ui.Text(c.Key("title"), s.tr("串口设置", "Port settings")).FontSize(16).FontWeight(600)
			ui.Scroll(c.Key("fields")).FillWidth().MaxHeight(240).Children(func() {
				ui.Column(c.Key("options")).FillWidth().Gap(8).Children(func() {
					ui.Row(c.Key("baud")).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(c.Key("caption"), s.tr("波特率", "Baud rate")).Width(90)
						s.baudPicker(c.Key("value"), true, 26)
					})
					portSettingSelect(c, "bits", s.tr("数据位", "Data bits"), &p.DataBits, dataBits, open)
					portSettingSelect(c, "stops", s.tr("停止位", "Stop bits"), &p.StopBits, stopBits, open)
					portSettingSelect(c, "parity", s.tr("校验位", "Parity"), &p.Parity, parities, open)
					ui.Row(c.Key("control-lines")).FillWidth().Gap(20).Children(func() {
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
			ui.Row(c.Key("actions")).FillWidth().Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c.Key("ok"), s.tr("确定", "OK")).Width(76).Height(28).OnClick(s.applyPortSettings)
				ui.Button(c.Key("cancel"), s.tr("取消", "Cancel")).Width(76).Height(28).OnClick(func() { s.portSettingsOpen = false })
			})
		})
	}).Label(s.tr("串口设置", "Port settings"))
}

func portSettingSelect(c *ui.Context, id, label string, value *string, options []string, disabled bool) {
	ui.Row(c.Key(id)).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c.Key("caption"), label).Width(90)
		ui.Select(c.Key("value"), value, options).Width(0).MinWidth(0).Grow(1).Height(26).PaddingY(controlPadY).Label(label).Disabled(disabled)
	})
}
