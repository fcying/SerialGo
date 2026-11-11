package ui

import (
	"fmt"
	"runtime"
	"strconv"

	"github.com/egoist/mygo/ui"
)

var (
	baudRates = []string{
		"300", "600", "1200", "2400", "4800", "9600", "14400", "19200", "38400", "57600",
		"115200", "128000", "230400", "256000", "460800", "500000", "576000", "921600",
		"1000000", "1500000", "2000000", "3000000",
	}
	dataBits   = []string{"5", "6", "7", "8"}
	parities   = []string{"None", "Odd", "Even", "Mark", "Space"}
	stopBits   = []string{"1", "1.5", "2"}
	checksums  = []string{"None", "CRC16-Modbus", "ADD", "XOR"}
	encodings  = []string{"UTF-8", "GB18030"}
	lightTheme = compactTheme()
)

const (
	controlHeight = 22
	// Leave enough vertical room for a complete line of text at this height.
	controlPadY = 2
)

func compactTheme() *ui.Theme {
	theme := ui.LightTheme()
	theme.Background = ui.Hex("#F0F0F0")
	theme.Border = ui.Hex("#B8B8B8")
	theme.Radius = 2
	theme.Spacing = 3
	theme.FontSize = 12
	if runtime.GOOS == "windows" {
		theme.Font = "Microsoft YaHei UI"
		theme.FontSize = 13
	}
	return theme
}

func (s *controller) view(c *ui.Context) {
	c.SetTheme(lightTheme)
	s.syncMenu()
	ui.Column(c.Key("desktop")).Fill().Padding(2).Gap(2).Background(c.Theme().Background).Children(func() {
		if s.settings.ShowPresets {
			ui.SplitPercent(c.Key("split"), &s.presetSplit, func() { s.receivePane(c) }, func() { s.presetPane(c) }).FillWidth().Grow(1).MinHeight(100).SplitReset(64)
		} else {
			s.receivePane(c)
		}
		s.fileBar(c)
		ui.Row(c.Key("lower")).FillWidth().Gap(3).Shrink(0).AlignItems(ui.Start).Children(func() {
			s.serialPanel(c)
			s.sendPanel(c)
		})
		s.statusBar(c)
	})
}

func (s *controller) receivePane(c *ui.Context) {
	ui.TextAreaBuffer(c.Key("receive"), &s.log.text).Fill().MinHeight(80).Grow(1).ReadOnly(true).
		Background(ui.Hex("#FFFFFF")).TrackScroll(&s.log.scroll).Label("接收窗口")
}

func (s *controller) fileBar(c *ui.Context) {
	presetLabel := "扩展"
	if s.settings.ShowPresets {
		presetLabel = "收起"
	}
	ui.Row(c.Key("file-bar")).FillWidth().Gap(3).Shrink(0).AlignItems(ui.Center).Children(func() {
		ui.Button(c.Key("clear-receive"), "清除窗口").Height(controlHeight).PaddingY(controlPadY).OnClick(s.log.clear)
		ui.Button(c.Key("open-file"), "打开文件").Height(controlHeight).PaddingY(controlPadY).Disabled(s.dialogBusy).OnClick(s.chooseFile)
		ui.TextInput(c.Key("file-path"), &s.filePath).Width(0).Grow(1).MinWidth(0).Height(controlHeight).PaddingY(controlPadY).
			Label("待发送文件").Placeholder("待发送文件").ReadOnly(true)
		ui.Button(c.Key("send-file"), "发送文件").Height(controlHeight).PaddingY(controlPadY).Disabled(!s.canSendFile()).OnClick(s.sendFile)
		ui.Button(c.Key("stop-send"), "停止").Height(controlHeight).PaddingY(controlPadY).Disabled(s.cancelSend == nil).OnClick(s.stopSending)
		ui.Button(c.Key("clear-send"), "清发送区").Height(controlHeight).PaddingY(controlPadY).OnClick(func() { *s.sendValue() = "" })
		ui.Checkbox(c.Key("top"), &s.alwaysOnTop, "最前").OnChange(s.applyAlwaysOnTop)
		ui.Button(c.Key("save-settings"), "保存参数").Height(controlHeight).PaddingY(controlPadY).OnClick(s.saveSettings)
		ui.Button(c.Key("show-presets"), presetLabel).Height(controlHeight).PaddingY(controlPadY).Label("多条发送面板").OnClick(func() {
			s.settings.ShowPresets = !s.settings.ShowPresets
		})
	})
}

func (s *controller) serialPanel(c *ui.Context) {
	p := &s.settings.Port
	open := s.session != nil
	ui.Column(c.Key("serial")).Width(218).Shrink(0).Gap(3).Padding(3).Border(1, c.Theme().Border).Children(func() {
		ui.Row(c.Key("port-row")).FillWidth().Gap(4).AlignItems(ui.Center).Children(func() {
			ui.Text(c.Key("port-label"), "端口号")
			ui.Select(c.Key("port-name"), &p.Name, s.ports).Width(0).MinWidth(0).Grow(1).Height(controlHeight).PaddingY(controlPadY).
				Label("端口号").Disabled(open || len(s.ports) == 0)
		})
		ui.Row(c.Key("port-actions")).FillWidth().Gap(3).AlignItems(ui.Center).Children(func() {
			label, color := "打开串口", c.Theme().Danger
			if open {
				label, color = "关闭串口", c.Theme().Success
			}
			ui.Box(c.Key("port-light")).Size(9, 9).Radius(5).Background(color)
			ui.Button(c.Key("open-port"), label).Height(controlHeight).PaddingY(controlPadY).Disabled(!open && p.Name == "").OnClick(s.togglePort)
			ui.Button(c.Key("refresh"), "刷新").Height(controlHeight).PaddingY(controlPadY).Disabled(open).OnClick(s.refreshPorts)
			ui.Button(c.Key("advanced"), "串口设置").Height(controlHeight).PaddingY(controlPadY).Label("更多串口设置").OnClick(func() { s.advanced = !s.advanced })
		})
		ui.Row(c.Key("line-row")).FillWidth().Gap(4).AlignItems(ui.Center).Children(func() {
			dtr, rts := p.DTR, p.RTS
			ui.Checkbox(c.Key("rts"), &p.RTS, "RTS").OnChange(func() { s.setLines(dtr, rts) })
			ui.Checkbox(c.Key("dtr"), &p.DTR, "DTR").OnChange(func() { s.setLines(dtr, rts) })
			ui.Text(c.Key("baud-label"), "波特率")
			ui.Select(c.Key("baud"), &p.Baud, baudRates).Width(0).MinWidth(0).Grow(1).Height(controlHeight).
				Padding(controlPadY, 3).Gap(2).Label("波特率").Disabled(open)
		})
		if s.advanced {
			ui.Row(c.Key("framing")).FillWidth().Gap(4).Children(func() {
				framingSelect(c, "bits", "数据位", &p.DataBits, dataBits, 48, open)
				framingSelect(c, "parity", "校验位", &p.Parity, parities, 76, open)
				framingSelect(c, "stops", "停止位", &p.StopBits, stopBits, 55, open)
			})
		}
		ui.Button(c.Key("send"), "发 送").Width(64).Height(26).PaddingY(controlPadY).AlignSelf(ui.End).Disabled(!s.canSend()).
			OnClick(func() { s.sendText(*s.sendValue()) })
	})
}

func framingSelect(c *ui.Context, id, label string, value *string, options []string, width float32, disabled bool) {
	ui.Column(c.Key(id)).Gap(2).Children(func() {
		ui.Text(c.Key("caption"), label)
		ui.Select(c.Key("select"), value, options).Width(width).MinWidth(0).Height(controlHeight).PaddingY(controlPadY).Label(label).Disabled(disabled)
	})
}

func (s *controller) sendPanel(c *ui.Context) {
	p := &s.settings
	capture := "接收存文件"
	if s.capturePath != "" {
		capture = "停止接收"
	}
	ui.Column(c.Key("send-panel")).Width(0).Grow(1).MinWidth(0).Gap(2).Padding(3).Border(1, c.Theme().Border).Children(func() {
		ui.Row(c.Key("send-options")).FillWidth().Wrap().Gap(2).AlignItems(ui.Center).Children(func() {
			ui.Checkbox(c.Key("rx-hex"), &p.ReceiveHex, "HEX 显示").OnChange(s.configureLog)
			ui.Button(c.Key("save-receive"), "保存数据").Height(controlHeight).PaddingY(controlPadY).PaddingX(5).Disabled(s.dialogBusy).OnClick(s.saveLog)
			ui.Button(c.Key("capture"), capture).Height(controlHeight).PaddingY(controlPadY).PaddingX(5).Disabled(s.dialogBusy || s.session == nil).OnClick(s.toggleCapture)
			ui.Checkbox(c.Key("tx-hex"), &p.SendHex, "HEX 发送")
			ui.Checkbox(c.Key("repeat"), &s.repeat, "定时发送")
			ui.TextInput(c.Key("interval"), &p.Interval).Width(39).Height(controlHeight).PaddingY(controlPadY).Label("定时发送间隔 (ms)").Disabled(!s.repeat)
			ui.Text(c.Key("interval-ms"), "ms/次")
			ui.Checkbox(c.Key("crlf"), &p.CRLF, "加回车换行")
		})
		ui.Row(c.Key("display-options")).FillWidth().Wrap().Gap(4).AlignItems(ui.Center).Children(func() {
			ui.Checkbox(c.Key("timestamp"), &p.Timestamp, "时间戳").OnChange(s.configureLog)
			ui.Checkbox(c.Key("packet"), &p.Packet, "分包").OnChange(s.configureLog)
			ui.Text(c.Key("packet-label"), "超时")
			ui.TextInput(c.Key("packet-gap"), &p.PacketGap).Width(35).Height(controlHeight).PaddingY(controlPadY).Label("分包超时 (ms)").Disabled(!p.Packet).OnChange(s.configureLog)
			ui.Text(c.Key("gap-ms"), "ms")
			ui.Text(c.Key("checksum-label"), "加校验")
			ui.Select(c.Key("checksum"), &p.Checksum, checksums).Width(108).MinWidth(0).Height(controlHeight).PaddingY(controlPadY).Label("发送校验")
			ui.Text(c.Key("file-delay-label"), "文件延时")
			ui.TextInput(c.Key("file-delay"), &p.FileDelay).Width(35).Height(controlHeight).PaddingY(controlPadY).Label("文件分段延时 (ms)")
			ui.Text(c.Key("file-delay-ms"), "ms")
		})
		if p.Checksum != "None" {
			ui.Row(c.Key("checksum-range")).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c.Key("from-label"), "校验起始字节")
				ui.TextInput(c.Key("check-from"), &p.CheckFrom).Width(45).Height(controlHeight).PaddingY(controlPadY).Label("校验起始字节")
				ui.Text(c.Key("end-label"), "末尾跳过字节")
				ui.TextInput(c.Key("check-end"), &p.CheckEnd).Width(45).Height(controlHeight).PaddingY(controlPadY).Label("末尾跳过字节")
			})
		}
		key, hint := "send-text", "输入文本"
		if p.SendHex {
			key, hint = "send-hex", "输入十六进制字节, 如 01 03 00 FF"
		}
		ui.TextArea(c.Key(key), s.sendValue()).FillWidth().MinHeight(47).Height(47).MaxLength(200_000).
			Background(ui.Hex("#FFFFFF")).Label("发送区").Placeholder(hint)
	})
}

func (s *controller) presetPane(c *ui.Context) {
	ui.Column(c.Key("presets")).Fill().MinWidth(0).Gap(3).Padding(3).Clip().Border(1, c.Theme().Border).Children(func() {
		ui.Row(c.Key("preset-actions")).FillWidth().Wrap().Gap(6).Shrink(0).AlignItems(ui.Center).Children(func() {
			ui.Text(c.Key("preset-label"), "多条发送")
			ui.Button(c.Key("run-presets"), "发送勾选").Height(controlHeight).PaddingY(controlPadY).Disabled(!s.canSend()).OnClick(func() { s.sendPresets(-1) })
			ui.Checkbox(c.Key("loop-presets"), &s.loopPresets, "循环")
		})
		ui.List(c.Key("preset-list"), nil, len(s.settings.Presets), func(i int) {
			preset := &s.settings.Presets[i]
			ui.Column(c.Key(i)).FillWidth().Gap(2).Padding(3).Border(1, c.Theme().Border).Children(func() {
				ui.Row(c.Key("preset-top")).FillWidth().Gap(3).AlignItems(ui.Center).Children(func() {
					ui.Checkbox(c.Key("preset-enabled"), &preset.Enabled, strconv.Itoa(i+1))
					ui.TextArea(c.Key("preset-data"), &preset.Data).Width(0).MinWidth(0).Grow(1).MinHeight(controlHeight).Height(controlHeight).PaddingY(controlPadY).
						MaxLength(4096).Placeholder("发送内容").Label("条目内容")
					ui.Button(c.Key("preset-send"), "发送").Height(controlHeight).PaddingY(controlPadY).Disabled(!s.canSend()).OnClick(func() { s.sendPresets(i) })
				})
				ui.Row(c.Key("preset-options")).FillWidth().Wrap().Gap(3).AlignItems(ui.Center).Children(func() {
					ui.TextInput(c.Key("preset-name"), &preset.Label).Width(0).MinWidth(30).Grow(1).Height(controlHeight).PaddingY(controlPadY).Placeholder("备注").Label("条目备注")
					ui.Checkbox(c.Key("preset-hex"), &preset.Hex, "HEX")
					ui.Checkbox(c.Key("preset-crlf"), &preset.CRLF, "CRLF")
					ui.TextInput(c.Key("preset-delay"), &preset.Interval).Width(42).Height(controlHeight).PaddingY(controlPadY).Label("条目发送间隔 (ms)")
					ui.Text(c.Key("preset-ms"), "ms")
				})
			})
		}).FillWidth().Grow(1).MinHeight(0).Gap(4).Label("发送条目")
	})
}

func (s *controller) statusBar(c *ui.Context) {
	p := &s.settings.Port
	state := "已关闭"
	if s.session != nil {
		state = "已打开"
	}
	color := c.Theme().TextMuted
	if s.statusError {
		color = c.Theme().Danger
	}
	ui.Row(c.Key("status-bar")).FillWidth().Shrink(0).Height(20).Gap(1).AlignItems(ui.Center).Children(func() {
		statusCell(c, "tx-count", fmt.Sprintf("S: %d", s.tx), 75)
		statusCell(c, "rx-count", fmt.Sprintf("R: %d", s.rx), 75)
		statusCell(c, "port-state", fmt.Sprintf("%s %s  %sbps,%s,%s,%s", p.Name, state, p.Baud, p.DataBits, p.StopBits, p.Parity), 205)
		ui.Row(c.Key("status-message")).Width(0).Grow(1).MinWidth(0).FillHeight().Padding(0, 3).Border(1, c.Theme().Border).AlignItems(ui.Center).Children(func() {
			ui.Text(c.Key("message"), s.status).Width(0).Grow(1).MinWidth(0).SingleLine().Ellipsis("...").TextColor(color).Label("操作状态")
		})
	})
}

func statusCell(c *ui.Context, id, value string, width float32) {
	ui.Row(c.Key(id)).Width(width).FillHeight().Padding(0, 3).Border(1, c.Theme().Border).AlignItems(ui.Center).Children(func() {
		ui.Text(c.Key("value"), value).Width(0).Grow(1).MinWidth(0).SingleLine().Ellipsis("...")
	})
}
