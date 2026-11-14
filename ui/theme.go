package ui

import (
	"runtime"

	"github.com/egoist/mygo/ui"
)

var (
	lightTheme         = compactTheme()
	panelBackground    = ui.Hex("#F8FAFC")
	textAreaBackground = ui.Hex("#FFFFFF")
)

// Shared layout gaps, in logical pixels.
const (
	microGap         float32 = 1
	compactGap       float32 = 2
	tightGap         float32 = 3
	controlGap       float32 = 4
	groupGap         float32 = 6
	wideGap          float32 = 8
	sectionGap       float32 = 10
	pairedControlGap float32 = 20
)

// Shared control dimensions, padding, and border metrics.
const (
	controlHeight                         = 24
	intervalWidth                         = 48
	presetToggleWidth                     = 32
	controlPadY                           = 2
	panelPaddingX                 float32 = 6
	panelBorderWidth              float32 = 1
	serialConfigPaddingX          float32 = 4
	serialConfigBorderWidth       float32 = 1
	serialConfigButtonChromeWidth         = 2 * (serialConfigPaddingX + serialConfigBorderWidth)
	panelInsetX                           = panelPaddingX + panelBorderWidth
	panelChromeWidth                      = 2 * panelInsetX
	baudPickerArrowSize           float32 = 6
	serialSelectorPaddingX        float32 = 4
	serialSelectorArrowPaddingX   float32 = 4
	baudPickerBorderWidth         float32 = 1
	baudPickerChromeWidth                 = 2*(serialSelectorPaddingX+baudPickerBorderWidth) + serialSelectorArrowPaddingX + microGap + baudPickerArrowSize
)

func compactTheme() *ui.Theme {
	theme := ui.LightTheme()
	theme.Background = ui.Hex("#EEF1F5")
	theme.Surface = ui.Hex("#FFFFFF")
	theme.SurfaceHover = ui.Hex("#E8EFFA")
	theme.SurfacePressed = ui.Hex("#DAE5F5")
	theme.Border = ui.Hex("#CCD3DD")
	theme.Radius = 4
	theme.Spacing = tightGap
	theme.FontSize = 12
	if runtime.GOOS == "windows" {
		theme.Font = "Microsoft YaHei UI"
	}
	return theme
}
