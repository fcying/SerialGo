package ui

import (
	"github.com/egoist/mygo/ui"
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
				ui.Button(c.Key("cancel"), s.tr("取消", "Cancel")).Width(76).Height(28).OnClick(func() {
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

func portSettingSelect(c *ui.Context, id, label string, value *string, options []string, disabled bool) {
	ui.Row(c.Key(id)).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c.Key("caption"), label).Width(90)
		ui.Select(c.Key("value"), value, options).Width(0).MinWidth(0).Grow(1).Height(26).PaddingY(controlPadY).Label(label).Disabled(disabled)
	})
}
