package ui

import (
	"os"

	"github.com/fcying/SerialGo/internal/i18n"

	"github.com/egoist/mygo"
	_ "github.com/egoist/mygo/locales/zh-Hans"
)

type localizedText struct {
	zh, en string
}

func initializeLanguage(language string) error {
	// Keep MyGo's locale session-only; config.toml is the sole preferences store.
	err := os.Setenv("MYGO_LOCALE", language)
	if err != nil {
		return err
	}
	mygo.App.SetLanguages("en", "zh-CN")
	mygo.App.SetLocale(language)
	return nil
}

func (s *controller) tr(zh, en string) string {
	if s.settings.Language == "zh-CN" {
		return zh
	}
	return en
}

func (s *controller) setLanguage(language string) {
	if s.app.SetLanguage(language) {
		mygo.App.SetLocale(s.settings.Language)
		if s.curve != nil && curveSpanSeconds(s.curve.span) == 0 {
			s.curve.span = s.tr("全部", "All")
		}
		if s.curveWindow != nil {
			s.curveWindow.SetTitle(s.serialCurveTitle())
			s.curveWindow.Invalidate()
			if s.curve.ruleEditor != nil && s.curve.ruleEditor.window != nil {
				s.curve.ruleEditor.window.SetTitle(s.curveRuleEditorTitle())
				s.curve.ruleEditor.window.Invalidate()
			}
			if s.curve.profileEditor != nil && s.curve.profileEditor.window != nil {
				s.curve.profileEditor.window.SetTitle(s.tr("配置", "Configuration"))
				s.curve.profileEditor.window.Invalidate()
			}
			if s.curve.testWindow != nil {
				s.curve.testWindow.SetTitle(s.tr("测试规则", "Test rules"))
				s.curve.testWindow.Invalidate()
			}
		}
	}
}

func (s *controller) toggleLanguage() {
	if s.settings.Language == "zh-CN" {
		s.setLanguage("en")
	} else {
		s.setLanguage("zh-CN")
	}
}

func dialogError(err error) localizedText {
	return localizedText{zh: i18n.ErrorText(err, false), en: i18n.ErrorText(err, true)}
}
