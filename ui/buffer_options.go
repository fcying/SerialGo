package ui

import (
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
		ui.Select(c.Key("buffer-limit"), &selected, displayBufferLabels).Width(88).Height(controlHeight).PaddingY(controlPadY).
			Label(label).Tooltip(tooltip).OnChange(func() {
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
