package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/fcying/SerialGo/communication/serialport"
	"github.com/fcying/SerialGo/config"

	gui "github.com/egoist/mygo/ui"
)

func TestStatusBarKeepsLongTextOnOneLine(t *testing.T) {
	for _, width := range []int{780, 1000} {
		for _, scale := range []float32{1, 1.25, 1.5, 1.6, 2} {
			t.Run(fmt.Sprintf("width_%d_scale_%g", width, scale), func(t *testing.T) {
				s := &controller{
					settings: config.Settings{Port: serialport.Settings{
						Name: "COM1", Baud: "115200", DataBits: "8", StopBits: "1", Parity: "None",
					}},
					tx: ^uint64(0), statusError: true,
					status: "打开串口失败: " + strings.Repeat("The port is unavailable. ", 10),
				}
				s.configureLog()
				tt := gui.NewTester(s.view, width, 600)
				tt.SetScale(scale)
				line, found := tt.Find("R: 0")
				if !found {
					t.Fatal("receive counter is missing")
				}
				for _, text := range []string{
					"S: 18446744073709551615",
					"COM1 已关闭  115200bps,8,1,None",
					"操作状态",
				} {
					bounds, found := tt.Find(text)
					if !found {
						t.Fatalf("status field %q is missing", text)
					}
					if bounds.Y < line.Y-1 || bounds.Y+bounds.H > line.Y+line.H+1 || bounds.Y+bounds.H > 600 {
						t.Errorf("status field %q extends outside its footer line: field=%+v line=%+v", text, bounds, line)
					}
				}
			})
		}
	}
}
