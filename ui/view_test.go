package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/fcying/SerialGo/communication/serialport"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"

	gui "github.com/egoist/mygo/ui"
)

func TestStatusBarKeepsLongTextOnOneLine(t *testing.T) {
	for _, language := range []struct {
		code, portState, statusLabel string
	}{
		{"zh-CN", "COM1 已关闭  115200bps,8,1,None", "操作状态"},
		{"en", "COM1 Closed  115200bps,8,1,None", "Operation status"},
	} {
		for _, width := range []int{780, 800, 1000} {
			for _, scale := range []float32{1, 1.25, 1.5, 1.6, 2} {
				t.Run(fmt.Sprintf("%s_width_%d_scale_%g", language.code, width, scale), func(t *testing.T) {
					s := newController(&internal.App{
						Settings: config.Settings{Language: language.code, Port: serialport.Settings{
							Name: "COM1", Baud: "115200", DataBits: "8", StopBits: "1", Parity: "None",
						}},
						Tx: ^uint64(0), StatusError: true,
						Status: internal.Text{
							ZH: "打开串口失败: " + strings.Repeat("The port is unavailable. ", 10),
							EN: "Failed to open port: " + strings.Repeat("The port is unavailable. ", 10),
						},
					})
					s.configureLog()
					tt := gui.NewTester(s.view, width, 600)
					tt.SetScale(scale)
					line, found := tt.Find("R: 0")
					if !found {
						t.Fatal("receive counter is missing")
					}
					for _, text := range []string{
						"S: 18446744073709551615",
						language.portState,
						language.statusLabel,
						s.tr("收发计数清零", "Reset counters"),
					} {
						bounds, found := tt.Find(text)
						if !found {
							t.Fatalf("status field %q is missing", text)
						}
						center := bounds.Y + bounds.H/2
						lineCenter := line.Y + line.H/2
						if center < lineCenter-1 || center > lineCenter+1 || bounds.Y < 0 || bounds.Y+bounds.H > 600 {
							t.Errorf("status field %q extends outside its footer line: field=%+v line=%+v", text, bounds, line)
						}
					}
					for _, label := range []string{
						s.tr("选项", "Options"),
						s.tr("清空发送区", "Clear send area"),
						s.tr("保存数据", "Save log"),
						s.tr("接收存文件", "Capture"),
						s.tr("HEX 显示", "HEX display"),
						s.tr("HEX 发送", "HEX send"),
					} {
						bounds, found := tt.Find(label)
						if !found {
							t.Fatalf("control %q is missing", label)
						}
						if bounds.X < 0 || bounds.X+bounds.W > float32(width)+1 || bounds.Y < 0 || bounds.Y+bounds.H > line.Y+1 {
							t.Errorf("control %q is clipped or overlaps the footer: %+v", label, bounds)
						}
					}
					for _, pair := range [][2]string{
						{s.tr("文件延时", "File delay"), s.tr("文件分段延时 (ms)", "File chunk delay (ms)")},
					} {
						caption, captionFound := tt.Find(pair[0])
						field, fieldFound := tt.Find(pair[1])
						if !captionFound || !fieldFound {
							t.Fatalf("option group is missing: %q", pair)
						}
						if caption.Y < field.Y-1 || caption.Y+caption.H > field.Y+field.H+1 {
							t.Errorf("caption %q wrapped away from its field: caption=%+v field=%+v", pair[0], caption, field)
						}
					}
					if err := tt.Click(s.tr("选项", "Options")); err != nil {
						t.Fatal(err)
					}
					for _, label := range []string{
						"语言 / Language",
						s.tr("显示发送记录", "Show sent data"),
						s.tr("文本编码", "Text encoding"),
						s.tr("分包超时 (ms)", "Packet gap (ms)"),
					} {
						bounds, found := tt.Find(label)
						if !found || bounds.X < 0 || bounds.X+bounds.W > float32(width)+1 || bounds.Y < 0 || bounds.Y+bounds.H > 600 {
							t.Errorf("option %q is missing or clipped: %+v", label, bounds)
						}
					}
				})
			}
		}
	}
}

func TestSendAreaResizePreservesDraftAndVisibleActions(t *testing.T) {
	for _, height := range []int{420, 600} {
		t.Run(fmt.Sprint(height), func(t *testing.T) {
			s := newController(&internal.App{Settings: config.Settings{Language: "en", SendText: "draft\nsecond line"}})
			s.configureLog()
			tt := gui.NewTester(s.view, 800, height)
			before, found := tt.Find("Send area")
			if !found || before.H < 40 || before.H > 64 {
				t.Fatalf("default send area is not compact: %+v", before)
			}
			receive, found := tt.Find("Receive log")
			if !found {
				t.Fatal("receive area is missing")
			}
			x, y := receive.X+receive.W/2, receive.Y+receive.H+0.5
			tt.Press(x, y)
			tt.Move(x, y-60)
			tt.Release(x, y-60)
			tt.Frame()
			after, _ := tt.Find("Send area")
			if after.H < before.H+59 || after.H > before.H+61 {
				t.Fatalf("dragging 60px did not resize input by 60px: before=%+v after=%+v", before, after)
			}
			receive, _ = tt.Find("Receive log")
			y = receive.Y + receive.H + 0.5
			tt.Press(x, y)
			tt.Move(x, float32(height*2))
			tt.Release(x, float32(height*2))
			tt.Frame()
			input, _ := tt.Find("Send area")
			actions, found := tt.Find("Clear send area")
			if input.H < 40 || !found || actions.Y+actions.H > input.Y || actions.Y < 0 {
				t.Fatalf("resizing hid the input or actions: input=%+v actions=%+v", input, actions)
			}
			if s.settings.SendText != "draft\nsecond line" {
				t.Fatal("resizing changed the send draft")
			}
		})
	}
}
