package ui

import (
	"fmt"
	"github.com/egoist/mygo/ui"
)

func (s *controller) addPreset() {
	if !s.app.AddPreset() {
		return
	}
	s.presetGeneration++
	s.presetList = ui.ListState{}
	s.presetList.ScrollToEnd()
}

func (s *controller) openPresetDelete(index int) {
	if s.app.Sending() || index < 0 || index >= len(s.settings.Presets) {
		return
	}
	s.presetDeleteIndex = index
	s.presetDeleteOpen = true
}

func (s *controller) applyPresetDelete() {
	index := s.presetDeleteIndex
	if !s.app.DeletePreset(index) {
		return
	}
	s.presetGeneration++
	s.presetList = ui.ListState{}
	if len(s.settings.Presets) > 0 {
		s.presetList.ScrollIntoView(min(index, len(s.settings.Presets)-1))
	}
	s.presetDeleteOpen = false
}

func (s *controller) presetDeleteDialog(c *ui.Context) {
	ui.Modal(c.Key("preset-delete-dialog"), &s.presetDeleteOpen, func() {
		ui.Column(c.Key("confirmation")).Width(310).Gap(10).Children(func() {
			ui.Text(c.Key("title"), s.tr("删除发送条目", "Delete preset")).FontSize(16).FontWeight(600)
			ui.Text(c.Key("prompt"), s.tr(
				fmt.Sprintf("删除第 %d 条发送条目? 后续条目将重新编号.", s.presetDeleteIndex+1),
				fmt.Sprintf("Delete preset %d? Following presets will be renumbered.", s.presetDeleteIndex+1),
			)).FillWidth()
			ui.Row(c.Key("actions")).FillWidth().Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c.Key("delete"), s.tr("删除", "Delete")).Width(76).Height(28).Label(s.tr("确认删除条目", "Confirm preset deletion")).Disabled(s.app.Sending()).OnClick(s.applyPresetDelete)
				ui.Button(c.Key("cancel"), s.tr("取消", "Cancel")).Width(76).Height(28).AutoFocus().OnClick(func() { s.presetDeleteOpen = false })
			})
		})
	}).Label(s.tr("删除发送条目", "Delete preset"))
}
