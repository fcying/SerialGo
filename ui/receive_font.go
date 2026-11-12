package ui

import (
	"slices"
	"strconv"
	"strings"

	"github.com/fcying/SerialGo/config"

	"github.com/egoist/mygo/ui"
)

type receiveFontColor struct {
	zh, en string
	value  string
}

var (
	receiveFontSizes  = []string{"5", "6", "7", "8", "9", "10", "11", "12", "14", "16", "18", "20", "22", "24", "26", "28", "36", "48", "72"}
	receiveFontColors = []receiveFontColor{
		{zh: "黑色", en: "Black", value: "#000000"},
		{zh: "栗色", en: "Maroon", value: "#800000"},
		{zh: "绿色", en: "Green", value: "#008000"},
		{zh: "橄榄色", en: "Olive", value: "#808000"},
		{zh: "海军蓝", en: "Navy", value: "#000080"},
		{zh: "紫色", en: "Purple", value: "#800080"},
		{zh: "蓝绿色", en: "Teal", value: "#008080"},
		{zh: "灰色", en: "Gray", value: "#808080"},
		{zh: "银色", en: "Silver", value: "#C0C0C0"},
		{zh: "红色", en: "Red", value: "#FF0000"},
		{zh: "亮绿色", en: "Lime", value: "#00FF00"},
		{zh: "黄色", en: "Yellow", value: "#FFFF00"},
		{zh: "蓝色", en: "Blue", value: "#0000FF"},
		{zh: "品红", en: "Fuchsia", value: "#FF00FF"},
		{zh: "青色", en: "Aqua", value: "#00FFFF"},
		{zh: "白色", en: "White", value: "#FFFFFF"},
	}
)

func receiveFontColorLabel(language, value string) string {
	for _, option := range receiveFontColors {
		if option.value == value {
			if language == "zh-CN" {
				return option.zh
			}
			return option.en
		}
	}
	return value
}

func (s *controller) openReceiveFontDialog() {
	s.receiveFontNameDraft = config.NormalizeReceiveFont(s.settings.ReceiveFont)
	s.receiveFontSizeDraft = strconv.Itoa(config.NormalizeReceiveFontSize(s.settings.ReceiveFontSize))
	s.receiveFontColorDraft = config.NormalizeReceiveFontColor(s.settings.ReceiveFontColor)
	s.receiveFontColorChoice = receiveFontColorLabel(s.settings.Language, s.receiveFontColorDraft)
	s.receiveFontError = localizedText{}
	s.receiveFontDialogOpen = true
}

func (s *controller) applyReceiveFontDialog() {
	family := strings.TrimSpace(s.receiveFontNameDraft)
	size, err := strconv.Atoi(s.receiveFontSizeDraft)
	if family == "" || len([]rune(family)) > 128 || err != nil || size < config.MinReceiveFontSize || size > config.MaxReceiveFontSize {
		s.receiveFontError = localizedText{
			zh: "请输入字体名称, 并将字号设为 5 到 72 磅",
			en: "Enter a font family and a size from 5 to 72 points",
		}
		return
	}
	color := config.NormalizeReceiveFontColor(s.receiveFontColorDraft)
	if color != strings.ToUpper(strings.TrimSpace(s.receiveFontColorDraft)) {
		s.receiveFontError = localizedText{zh: "字体颜色无效", en: "Invalid font color"}
		return
	}
	if family != s.settings.ReceiveFont || size != s.settings.ReceiveFontSize || color != s.settings.ReceiveFontColor {
		s.settings.ReceiveFont = family
		s.settings.ReceiveFontSize = size
		s.settings.ReceiveFontColor = color
		s.app.SettingsChanged()
		s.app.SaveUsedSettings()
	}
	s.receiveFontDialogOpen = false
}

func (s *controller) receiveFontDialog(c *ui.Context) {
	ui.Modal(c.Key("receive-font-dialog"), &s.receiveFontDialogOpen, func() {
		ui.Column(c.Key("font-settings")).Width(360).Gap(10).Children(func() {
			ui.Text(c.Key("title"), s.tr("接收窗口字体", "Receive font")).FontSize(16).FontWeight(600)
			ui.Row(c.Key("family-row")).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c.Key("family-label"), s.tr("字体", "Font")).Width(72)
				ui.TextInput(c.Key("family"), &s.receiveFontNameDraft).Width(0).MinWidth(0).Grow(1).Height(controlHeight).PaddingY(controlPadY).
					MaxLength(128).Label(s.tr("接收字体名称", "Receive font family"))
			})
			ui.Row(c.Key("size-color-row")).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c.Key("size-label"), s.tr("字号", "Size"))
				ui.Select(c.Key("size"), &s.receiveFontSizeDraft, receiveFontSizes).Width(72).Height(controlHeight).PaddingY(controlPadY).
					Label(s.tr("字体大小 (pt)", "Font size (pt)"))
				ui.Text(c.Key("points"), "pt")
				ui.Text(c.Key("color-label"), s.tr("颜色", "Color"))
				colorLabel := receiveFontColorLabel(s.settings.Language, s.receiveFontColorDraft)
				colorLabels := make([]string, len(receiveFontColors))
				for i, option := range receiveFontColors {
					colorLabels[i] = s.tr(option.zh, option.en)
				}
				if !slices.Contains(colorLabels, colorLabel) {
					colorLabels = append(colorLabels, colorLabel)
				}
				ui.Select(c.Key("color"), &s.receiveFontColorChoice, colorLabels).Width(105).Height(controlHeight).PaddingY(controlPadY).
					Label(s.tr("字体颜色", "Font color")).OnChange(func() {
					for _, option := range receiveFontColors {
						if s.receiveFontColorChoice == s.tr(option.zh, option.en) {
							s.receiveFontColorDraft = option.value
							return
						}
					}
				})
			})
			previewFamily := config.NormalizeReceiveFont(s.receiveFontNameDraft)
			previewSize, err := strconv.Atoi(s.receiveFontSizeDraft)
			if err != nil || previewSize < config.MinReceiveFontSize || previewSize > config.MaxReceiveFontSize {
				previewSize = config.NormalizeReceiveFontSize(s.settings.ReceiveFontSize)
			}
			previewColor := config.NormalizeReceiveFontColor(s.receiveFontColorDraft)
			ui.Column(c.Key("preview")).FillWidth().Height(54).Padding(8, 10).Border(1, c.Theme().Border).Background(ui.Hex("#FFFFFF")).Center().Children(func() {
				ui.Text(c.Key("sample"), s.tr("串口数据预览 123", "Serial data preview 123")).
					Font(previewFamily).FontSize(float32(previewSize) * 4 / 3).TextColor(ui.Hex(previewColor))
			})
			if s.receiveFontError.en != "" {
				ui.Text(c.Key("error"), s.tr(s.receiveFontError.zh, s.receiveFontError.en)).FillWidth().TextColor(c.Theme().Danger)
			}
			ui.Row(c.Key("actions")).FillWidth().Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c.Key("ok"), s.tr("确定", "OK")).Width(76).Height(28).OnClick(s.applyReceiveFontDialog)
				ui.Button(c.Key("cancel"), s.tr("取消", "Cancel")).Width(76).Height(28).OnClick(func() { s.receiveFontDialogOpen = false })
			})
		})
	}).Label(s.tr("接收窗口字体", "Receive font"))
}

func (s *controller) receiveFontButton(c *ui.Context) {
	ui.Row(c.Key("receive-font-options")).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c.Key("font-label"), s.tr("接收窗口字体", "Receive font"))
		ui.Button(c.Key("configure-font"), s.tr("字体...", "Font...")).Height(controlHeight).PaddingY(controlPadY).
			Label(s.tr("配置接收窗口字体", "Configure receive font")).OnClick(s.openReceiveFontDialog)
	})
}
