package ui

import (
	"strconv"

	"github.com/fcying/SerialGo/config"

	"github.com/egoist/mygo/ui"
)

var displayBufferLabels = func() []string {
	presets := config.DisplayBufferPresets()
	labels := make([]string, len(presets))
	for i, preset := range presets {
		labels[i] = preset.Label
	}
	return labels
}()

// displayBufferOptions edits the settings draft; the owner applies and saves it.
func displayBufferOptions(c *ui.Context, settings *config.Settings, onChange func()) {
	presets := config.DisplayBufferPresets()
	selected := ""
	for _, preset := range presets {
		if preset.Bytes == settings.DisplayBufferLimit {
			selected = preset.Label
		}
	}
	label, tooltip := "Display buffer limit", "Retained data bytes; K = 1000 bytes, M = 1000000 bytes"
	if settings.Language == "zh-CN" {
		label, tooltip = "显示缓冲上限", "保留的数据字节数; K = 1000 字节, M = 1000000 字节"
	}
	ui.Row(c.Key("buffer-options")).Gap(groupGap).AlignItems(ui.Center).Children(func() {
		ui.Text(c.Key("buffer-label"), label)
		compactDisplaySelect(c.Key("buffer-limit"), &selected, displayBufferLabels, label, tooltip, func() {
			for _, preset := range presets {
				if preset.Label == selected && settings.DisplayBufferLimit != preset.Bytes {
					settings.DisplayBufferLimit = preset.Bytes
					if onChange != nil {
						onChange()
					}
					return
				}
			}
		})
	})
}

var receiveHexLineWidthLabels = func() []string {
	widths := config.ReceiveHexLineWidths()
	labels := make([]string, len(widths))
	for i, width := range widths {
		labels[i] = strconv.Itoa(width)
	}
	return labels
}()

func (s *controller) receiveHexLineWidthOptions(c *ui.Context) {
	selected := strconv.Itoa(config.NormalizeReceiveHexBytesPerLine(s.settings.ReceiveHexBytesPerLine))
	label := s.tr("HEX 每行字节数", "HEX bytes per line")
	tooltip := s.tr("仅影响 HEX 显示", "Applies only to HEX display")
	ui.Row(c).Gap(groupGap).AlignItems(ui.Center).Children(func() {
		ui.Text(c.Key("caption"), label)
		compactDisplaySelect(c.Key("width"), &selected, receiveHexLineWidthLabels, label, tooltip, func() {
			width, err := strconv.Atoi(selected)
			if err == nil && width != s.settings.ReceiveHexBytesPerLine {
				s.settings.ReceiveHexBytesPerLine = width
				s.applyDisplaySettings()
			}
		})
	})
}

func compactDisplaySelect(c *ui.Context, selected *string, options []string, label, tooltip string, onChange func()) {
	var textWidth float32
	for _, option := range options {
		w, _ := c.MeasureText(0, ui.Span{Text: option})
		textWidth = max(textWidth, w)
	}
	width := textWidth + 2*(displaySelectPaddingX+baudPickerBorderWidth) + serialSelectorArrowPaddingX + microGap + baudPickerArrowSize
	theme := c.Theme()
	sel := ui.SelectBase(c, selected)
	trigger := sel.Trigger.Width(width).Shrink(0).Height(controlHeight).
		Padding(controlPadY, serialSelectorArrowPaddingX, controlPadY, 0).Gap(microGap).Radius(theme.Radius).Border(baudPickerBorderWidth, theme.Border).
		Background(theme.Surface).TextColor(theme.Text).AlignItems(ui.Center).Label(label).Tooltip(tooltip).OnChange(onChange)
	if trigger.Pressed() {
		trigger.Background(theme.SurfacePressed)
	} else if trigger.Hovered() {
		trigger.Background(theme.SurfaceHover)
	}
	trigger.Children(func() {
		ui.Text(c.Key("selected"), *selected).Width(0).MinWidth(0).Grow(1).Padding(0, displaySelectPaddingX).SingleLine().TextAlign(ui.Center)
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
						item := sel.Item(option).Padding(4, displaySelectPaddingX).Radius(theme.Radius)
						if item.Highlighted() {
							item.Background(theme.Accent).TextColor(theme.AccentText)
						} else if option == *selected {
							item.Background(theme.Surface)
						}
						item.Children(func() { ui.Text(c.Key("label"), option).FillWidth().SingleLine().TextAlign(ui.Center) })
					}
				})
			})
		})
	})
}
