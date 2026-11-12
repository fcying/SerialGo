package ui

import (
	"strings"
	"testing"

	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"

	gui "github.com/egoist/mygo/ui"
)

func chooseReceiveFontOption(t *testing.T, tt *gui.Tester, label, option string) {
	t.Helper()
	picker, found := tt.AccessByRole(gui.RolePopUpButton, label)
	if !found || picker.Disabled {
		t.Fatalf("font option %q is missing or disabled", label)
	}
	tt.ClickAt(picker.Bounds.X+picker.Bounds.W/2, picker.Bounds.Y+picker.Bounds.H/2)
	choice, found := tt.AccessByRole(gui.RoleOption, option)
	if !found || choice.Disabled || choice.Offscreen {
		t.Fatalf("font choice %q is missing or unavailable", option)
	}
	tt.ClickAt(choice.Bounds.X+choice.Bounds.W/2, choice.Bounds.Y+choice.Bounds.H/2)
}

func TestReceiveFontDialogAppliesAndPersistsSettings(t *testing.T) {
	s, notified := settingsSaveFixture(t, t.TempDir()+"/config.toml")
	tt := gui.NewTester(s.view, 800, 600)
	if err := tt.Click("Options"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Font..."); err != nil {
		t.Fatal(err)
	}
	if _, found := tt.Find("Serial data preview 123"); !found {
		t.Fatal("font preview is missing")
	}
	field, found := tt.AccessByRole(gui.RoleTextField, "Receive font family")
	if !found {
		t.Fatal("font family field is missing")
	}
	tt.ClickAt(field.Bounds.X+field.Bounds.W/2, field.Bounds.Y+field.Bounds.H/2)
	tt.Key(gui.Ctrl, gui.KeyA)
	tt.Type("monospace")
	chooseReceiveFontOption(t, tt, "Font size (pt)", "16")
	chooseReceiveFontOption(t, tt, "Font color", "Red")
	if err := tt.Click("OK"); err != nil {
		t.Fatal(err)
	}
	if s.receiveFontDialogOpen || s.settings.ReceiveFont != "monospace" || s.settings.ReceiveFontSize != 16 || s.settings.ReceiveFontColor != "#FF0000" || !s.app.SettingsDirty {
		t.Fatalf("confirmed font preferences did not apply: open=%v settings=(%q,%d,%q) dirty=%v",
			s.receiveFontDialogOpen, s.settings.ReceiveFont, s.settings.ReceiveFontSize, s.settings.ReceiveFontColor, s.app.SettingsDirty)
	}
	completeSettingsSave(t, s, notified)
	stored := readSavedSettings(t, s.app.SettingsPath)
	if stored.ReceiveFont != "monospace" || stored.ReceiveFontSize != 16 || stored.ReceiveFontColor != "#FF0000" {
		t.Fatalf("font preferences not persisted: (%q,%d,%q)", stored.ReceiveFont, stored.ReceiveFontSize, stored.ReceiveFontColor)
	}

	if err := tt.Click("Font..."); err != nil {
		t.Fatal(err)
	}
	s.receiveFontNameDraft = "serif"
	tt.Frame()
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if s.receiveFontDialogOpen || s.settings.ReceiveFont != "monospace" || s.app.SettingsDirty {
		t.Fatal("canceling font preferences changed or dirtied the applied settings")
	}
}

func TestReceivePaneRendersConfiguredFontColor(t *testing.T) {
	settings := config.Settings{
		Language:         "en",
		ReceiveFont:      "monospace",
		ReceiveFontSize:  18,
		ReceiveFontColor: "#FF0000",
	}
	s := newController(&internal.App{Settings: settings})
	s.log.text.Set(strings.Repeat("MMMMMMMM", 2))
	tt := gui.NewTester(s.receivePane, 400, 120)
	bounds, found := tt.Find("Receive log")
	if !found {
		t.Fatal("receive pane is missing")
	}
	image := tt.Image()
	redPixels := 0
	for y := max(0, int(bounds.Y)); y < min(image.Bounds().Dy(), int(bounds.Y+bounds.H)); y++ {
		for x := max(0, int(bounds.X)); x < min(image.Bounds().Dx(), int(bounds.X+bounds.W)); x++ {
			pixel := image.RGBAAt(x, y)
			if pixel.A > 0 && pixel.R > 160 && pixel.G < 90 && pixel.B < 90 {
				redPixels++
			}
		}
	}
	if redPixels == 0 {
		t.Fatal("receive pane did not render glyphs with the configured red font color")
	}
}
