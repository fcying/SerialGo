package ui

import (
	"fmt"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (s *controller) openCurveRuleTest() {
	v := s.curve
	if v.testWindow != nil {
		if v.testWindow.IsMinimized() {
			v.testWindow.Restore()
		}
		v.testWindow.Focus()
		return
	}
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: s.tr("测试规则", "Test rules"), Parent: s.curveWindow,
		Width: 480, Height: 440, MinWidth: 400, MinHeight: 400,
		Content: ui.View(s.curveRuleTestView),
	})
	v.testWindow = w
	w.OnClosed(func() { v.testWindow = nil })
}

func (s *controller) curveRuleTestView(c *ui.Context) {
	c.SetTheme(lightTheme)
	v := s.curve
	ui.Column(c.Key("rule-test")).Fill().Padding(12).Gap(wideGap).Background(c.Theme().Background).Children(func() {
		ui.Text(c.Key("help"), s.tr("粘贴日志测试当前草稿, 不保存规则. 结果同时绘制在曲线窗口.", "Test the current draft with pasted logs without saving. Results also appear in the plot window.")).FillWidth()
		ui.TextArea(c.Key("input"), &v.previewText).FillWidth().Grow(1).MinHeight(80).MaxLength(65536).Font("monospace").Label(s.tr("预览日志", "Preview log"))
		if v.error.en != "" || v.error.zh != "" {
			ui.Text(c.Key("error"), s.tr(v.error.zh, v.error.en)).FillWidth().TextColor(c.Theme().Danger)
		} else if v.previewing {
			ui.Text(c.Key("counts"), fmt.Sprintf("%s %d | %s %d | %s %d", s.tr("匹配", "Matched"), v.snapshot.Matched,
				s.tr("无效", "Invalid"), v.snapshot.Invalid, s.tr("超长", "Oversize"), v.snapshot.Oversize)).FillWidth().SingleLine()
			ui.Scroll(c.Key("results")).FillWidth().Height(88).Children(func() {
				ui.Column(c.Key("values")).FillWidth().Gap(controlGap).Children(func() {
					for i, series := range v.snapshot.Series {
						value := s.tr("未匹配", "No match")
						if len(series.Points) > 0 {
							value = fmt.Sprintf("%.6g %s (%d)", series.Points[len(series.Points)-1].Y, series.Unit, len(series.Points))
						}
						ui.Text(c.Key(i), series.Name+": "+value).FillWidth().SingleLine().TextColor(curveColor(series.Color))
					}
				})
			})
		}
		ui.Row(c.Key("actions")).FillWidth().Gap(wideGap).Justify(ui.End).Children(func() {
			ui.Button(c.Key("close"), s.tr("关闭", "Close")).Width(76).Height(28).OnClick(func() {
				if v.testWindow != nil {
					v.testWindow.Close()
				}
			})
			ui.PrimaryButton(c.Key("preview"), s.tr("预览提取", "Preview extraction")).Height(28).OnClick(func() {
				s.previewCurve()
				if s.curveWindow != nil {
					s.curveWindow.Invalidate()
				}
			})
		})
	})
	if c.Shortcut(0, ui.KeyEscape) && v.testWindow != nil {
		v.testWindow.Close()
	}
}
