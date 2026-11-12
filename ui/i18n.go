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
