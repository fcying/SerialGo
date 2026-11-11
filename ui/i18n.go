package ui

import (
	"os"

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
	if language != "zh-CN" {
		language = "en"
	}
	if language == s.settings.Language {
		return
	}
	s.settings.Language = language
	mygo.App.SetLocale(language)
	s.settingsChanged()
	s.saveUsedSettings()
}

func (s *controller) toggleLanguage() {
	if s.settings.Language == "zh-CN" {
		s.setLanguage("en")
	} else {
		s.setLanguage("zh-CN")
	}
}
