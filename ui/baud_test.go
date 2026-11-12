package ui

import (
	gui "github.com/egoist/mygo/ui"
	"path/filepath"
	"reflect"
	"slices"
	"github.com/fcying/SerialGo/communication/serialport"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"
	"testing"
)

func baudSettingsFixture(t *testing.T) (*controller, <-chan func()) {
	t.Helper()
	s, notified := settingsSaveFixture(t, filepath.Join(t.TempDir(), "config.toml"))
	s.settings.Port = serialport.Settings{
		Name: "COM17", Baud: "115200", DataBits: "8", Parity: "None", StopBits: "1", DTR: true,
	}
	s.settings.BaudRates = []string{"115200", "250000", "500000"}
	s.portDraft = s.settings.Port
	if err := config.Save(s.app.SettingsPath, *s.settings); err != nil {
		t.Fatal(err)
	}
	return s, notified
}

func baudSettingsSnapshot(s *controller) config.Settings {
	settings := *s.settings
	settings.BaudRates = slices.Clone(settings.BaudRates)
	return settings
}

func requireBaudRates(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("baud rates = %v, want %v", got, want)
	}
}

func requireBaudUnwritten(t *testing.T, s *controller, notified <-chan func(), want config.Settings) {
	t.Helper()
	select {
	case <-notified:
		t.Fatal("unconfirmed baud edit invoked the settings writer")
	default:
	}
	if got := readSavedSettings(t, s.app.SettingsPath); !reflect.DeepEqual(got, want) {
		t.Fatalf("unconfirmed baud edit changed the saved settings: got %+v, want %+v", got, want)
	}
}

func clickBaudCustomAction(t *testing.T, tt *gui.Tester, s *controller) {
	t.Helper()
	trigger, found := tt.AccessByRole(gui.RolePopUpButton, s.tr("波特率", "Baud rate"))
	if !found || trigger.Disabled {
		t.Fatal("baud picker is missing or disabled")
	}
	tt.ClickAt(trigger.Bounds.X+trigger.Bounds.W/2, trigger.Bounds.Y+trigger.Bounds.H/2)
	option, found := tt.AccessByRole(gui.RoleOption, s.tr("自定义...", "Custom..."))
	if !found || option.Disabled || option.Offscreen {
		t.Fatal("custom baud action is missing or unavailable")
	}
	tt.ClickAt(option.Bounds.X+option.Bounds.W/2, option.Bounds.Y+option.Bounds.H/2)
	tt.Frame()
	if !s.customBaudOpen {
		t.Fatal("custom baud action did not open the editor")
	}
}

func typeCustomBaud(t *testing.T, tt *gui.Tester, s *controller, value string) {
	t.Helper()
	if err := tt.Click(s.tr("波特率数值", "Baud rate value")); err != nil {
		t.Fatal(err)
	}
	tt.Key(gui.Ctrl, gui.KeyA)
	tt.Type(value)
	if s.customBaudText != value {
		t.Fatalf("custom editor contains %q, want %q", s.customBaudText, value)
	}
}

func TestMainCustomBaudConfirmationAppendsAndDeduplicatesPersistedValues(t *testing.T) {
	s, notified := baudSettingsFixture(t)
	for _, test := range []struct {
		input, selected string
		want            []string
	}{
		{"038400", "38400", []string{"38400", "115200", "250000", "500000"}},
		{"750000", "750000", []string{"38400", "115200", "250000", "500000", "750000"}},
		{"0250000", "250000", []string{"38400", "115200", "250000", "500000", "750000"}},
	} {
		s.openCustomBaud(false)
		s.customBaudText = test.input
		s.applyCustomBaud()
		if s.customBaudOpen || s.customBaudError != (localizedText{}) {
			t.Fatal("valid custom confirmation did not close the editor cleanly")
		}
		completeSettingsSave(t, s, notified)
		stored := readSavedSettings(t, s.app.SettingsPath)
		requireBaudRates(t, stored.BaudRates, test.want)
		if stored.Port.Baud != test.selected || !reflect.DeepEqual(*s.settings, stored) || s.app.SettingsDirty {
			t.Fatalf("confirmation did not persist numeric baud and accumulated list: selected=%q rates=%v dirty=%v", stored.Port.Baud, stored.BaudRates, s.app.SettingsDirty)
		}
	}
	original := baudSettingsSnapshot(s)
	s.openCustomBaud(false)
	s.customBaudText = "250000"
	s.applyCustomBaud()
	if !reflect.DeepEqual(*s.settings, original) {
		t.Fatal("reconfirming the selected custom baud changed its list")
	}
	requireBaudUnwritten(t, s, notified, original)
}

func TestInvalidCustomBaudKeepsSettingsAndPropertyDraft(t *testing.T) {
	for _, forPort := range []bool{false, true} {
		name := "main"
		if forPort {
			name = "properties"
		}
		t.Run(name, func(t *testing.T) {
			for _, value := range []string{"", "not-a-number", "1.5", "0", "-9600", "4294967296"} {
				t.Run(value, func(t *testing.T) {
					s, notified := baudSettingsFixture(t)
					original := baudSettingsSnapshot(s)
					s.openPortSettings()
					draft, ratesDraft := s.portDraft, slices.Clone(s.portBaudRatesDraft)
					s.openCustomBaud(forPort)
					s.customBaudText = value
					s.applyCustomBaud()
					if !s.customBaudOpen || s.customBaudError == (localizedText{}) {
						t.Fatal("invalid baud did not retain the editor and show an error")
					}
					if !reflect.DeepEqual(*s.settings, original) || s.portDraft != draft || !slices.Equal(s.portBaudRatesDraft, ratesDraft) || s.app.SettingsDirty {
						t.Fatal("invalid baud changed settings or the property draft")
					}
					requireBaudUnwritten(t, s, notified, original)
				})
			}
		})
	}
}

func TestCustomBaudCancelAndEscapeThroughUI(t *testing.T) {
	for _, forPort := range []bool{false, true} {
		name := "main"
		if forPort {
			name = "properties"
		}
		t.Run(name, func(t *testing.T) {
			for _, escape := range []bool{false, true} {
				dismissal := "Cancel"
				if escape {
					dismissal = "Escape"
				}
				t.Run(dismissal, func(t *testing.T) {
					s, notified := baudSettingsFixture(t)
					original := baudSettingsSnapshot(s)
					s.configureLog()
					tt := gui.NewTester(s.view, 800, 600)
					if forPort {
						if err := tt.Click(s.tr("串口配置", "Port configuration")); err != nil {
							t.Fatal(err)
						}
					}
					draft, ratesDraft := s.portDraft, slices.Clone(s.portBaudRatesDraft)
					clickBaudCustomAction(t, tt, s)
					if !reflect.DeepEqual(*s.settings, original) || s.portDraft != draft {
						t.Fatal("custom action entered a sentinel into baud settings")
					}
					typeCustomBaud(t, tt, s, "38400")
					if escape {
						tt.Key(0, gui.KeyEscape)
					} else {
						// The outer property dialog has another Cancel button.
						tt.Key(0, gui.KeyTab)
						tt.Key(0, gui.KeyTab)
						tt.Key(0, gui.KeyEnter)
					}
					if s.customBaudOpen || !reflect.DeepEqual(*s.settings, original) || s.portDraft != draft || !slices.Equal(s.portBaudRatesDraft, ratesDraft) || s.app.SettingsDirty {
						t.Fatal("dismissing the custom editor committed an edit")
					}
					if s.portSettingsOpen != forPort {
						t.Fatal("dismissing the inner editor also dismissed its property dialog")
					}
					requireBaudUnwritten(t, s, notified, original)
				})
			}
		})
	}
}

func TestPropertyCustomBaudCommitsOnlyAtOuterConfirmation(t *testing.T) {
	for _, dismissal := range []string{"OK", "Cancel", "Escape"} {
		t.Run(dismissal, func(t *testing.T) {
			s, notified := baudSettingsFixture(t)
			original := baudSettingsSnapshot(s)
			s.configureLog()
			tt := gui.NewTester(s.view, 800, 600)
			if err := tt.Click(s.tr("串口配置", "Port configuration")); err != nil {
				t.Fatal(err)
			}
			for _, value := range []string{"038400", "750000"} {
				clickBaudCustomAction(t, tt, s)
				typeCustomBaud(t, tt, s, value)
				// Confirm within the inner dialog's focus scope, not the outer OK.
				tt.Key(0, gui.KeyTab)
				tt.Key(0, gui.KeyEnter)
				if s.customBaudOpen || !s.portSettingsOpen {
					t.Fatal("inner confirmation did not return to the property dialog")
				}
			}
			staged := []string{"38400", "115200", "250000", "500000", "750000"}
			requireBaudRates(t, s.portBaudRatesDraft, staged)
			if s.portDraft.Baud != "750000" || !reflect.DeepEqual(*s.settings, original) || s.app.SettingsDirty || !reflect.DeepEqual(readSavedSettings(t, s.app.SettingsPath), original) {
				t.Fatal("inner confirmations did not remain isolated from active and saved settings")
			}
			if dismissal == "Escape" {
				tt.Key(0, gui.KeyEscape)
			} else {
				label := s.tr("取消", "Cancel")
				if dismissal == "OK" {
					label = s.tr("确定", "OK")
				}
				if err := tt.Click(label); err != nil {
					t.Fatal(err)
				}
			}
			if s.portSettingsOpen {
				t.Fatal("outer dismissal left the property dialog open")
			}
			if dismissal == "OK" {
				completeSettingsSave(t, s, notified)
				stored := readSavedSettings(t, s.app.SettingsPath)
				requireBaudRates(t, stored.BaudRates, staged)
				if stored.Port.Baud != "750000" || !reflect.DeepEqual(*s.settings, stored) || s.app.SettingsDirty {
					t.Fatal("outer confirmation did not persist the accumulated list and selected baud together")
				}
			} else {
				if !reflect.DeepEqual(*s.settings, original) || s.app.SettingsDirty {
					t.Fatal("outer cancellation committed the staged custom baud list")
				}
				s.openPortSettings()
				if s.portDraft != original.Port || !slices.Equal(s.portBaudRatesDraft, original.BaudRates) {
					t.Fatal("reopening properties retained the canceled custom draft")
				}
				requireBaudUnwritten(t, s, notified, original)
			}
		})
	}
}

func TestPropertyBaudRatesDraftOwnsItsSlice(t *testing.T) {
	s, notified := baudSettingsFixture(t)
	original := baudSettingsSnapshot(s)
	s.openPortSettings()
	s.portBaudRatesDraft[0] = "38400"
	if !reflect.DeepEqual(*s.settings, original) {
		t.Fatal("editing the property list mutated active settings through a shared slice")
	}
	s.portSettingsOpen = false
	s.openPortSettings()
	if !slices.Equal(s.portBaudRatesDraft, original.BaudRates) {
		t.Fatal("reopening properties retained the discarded list mutation")
	}
	requireBaudUnwritten(t, s, notified, original)
}

func TestPortSelectionPreservesGlobalBaudAndFraming(t *testing.T) {
	s, notified := baudSettingsFixture(t)
	s.settings.Port.Baud = "250000"
	s.settings.Port.DataBits, s.settings.Port.Parity, s.settings.Port.StopBits = "7", "Even", "2"
	s.settings.Port.RTS = true
	original := baudSettingsSnapshot(s)
	s.app.SetPorts([]serialport.Info{
		{Name: "COM17", Description: "Original adapter"},
		{Name: "COM29", Description: "Another adapter"},
	})
	s.app.PortChoice = "COM29 Another adapter"
	s.app.SelectPort()
	want := original
	want.Port.Name = "COM29"
	if !reflect.DeepEqual(*s.settings, want) {
		t.Fatal("selecting another device changed global custom bauds, framing, or control lines")
	}
	s.app.SaveUsedSettings()
	completeSettingsSave(t, s, notified)
	if !reflect.DeepEqual(readSavedSettings(t, s.app.SettingsPath), want) {
		t.Fatal("device selection did not persist global serial parameters unchanged")
	}
	s.app.SetPorts([]serialport.Info{{Name: "COM99"}})
	want.Port.Name = "COM99"
	if !reflect.DeepEqual(*s.settings, want) {
		t.Fatal("device refresh fallback reset global serial parameters")
	}
}

func openBaudDeleteThroughUI(t *testing.T, tt *gui.Tester, s *controller, value string, forPort bool) {
	t.Helper()
	original := baudSettingsSnapshot(s)
	draft, ratesDraft, revision := s.portDraft, slices.Clone(s.portBaudRatesDraft), s.app.SettingsRevision
	trigger, found := tt.AccessByRole(gui.RolePopUpButton, s.tr("波特率", "Baud rate"))
	if !found || trigger.Disabled {
		t.Fatal("baud picker is missing or disabled")
	}
	tt.ClickAt(trigger.Bounds.X+trigger.Bounds.W/2, trigger.Bounds.Y+trigger.Bounds.H/2)
	option, found := tt.AccessByRole(gui.RoleOption, value)
	if !found || option.Disabled || option.Offscreen {
		t.Fatalf("numeric baud option %q is missing or unavailable", value)
	}
	tt.RightClickAt(option.Bounds.X+option.Bounds.W/2, option.Bounds.Y+option.Bounds.H/2)
	if !reflect.DeepEqual(*s.settings, original) || s.portDraft != draft || !slices.Equal(s.portBaudRatesDraft, ratesDraft) || s.app.SettingsRevision != revision {
		t.Fatal("right-clicking a baud option selected it or changed settings")
	}
	if got, want := tt.Menu(), []string{s.tr("删除", "Delete")}; !slices.Equal(got, want) {
		t.Fatalf("baud context menu = %v, want %v", got, want)
	}
	if err := tt.ChooseMenuItem(s.tr("删除", "Delete")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !s.baudDeleteOpen || s.baudDeleteValue != value || s.baudDeleteForPort != forPort {
		t.Fatal("the Delete menu action did not request confirmation for its numeric option")
	}
	if !reflect.DeepEqual(*s.settings, original) || s.portDraft != draft || !slices.Equal(s.portBaudRatesDraft, ratesDraft) || s.app.SettingsRevision != revision || s.app.SettingsDirty {
		t.Fatal("requesting deletion committed an edit before confirmation")
	}
}

func confirmBaudDeleteThroughUI(t *testing.T, tt *gui.Tester, s *controller) {
	t.Helper()
	if err := tt.Click(s.tr("删除", "Delete")); err != nil {
		t.Fatal(err)
	}
	if s.baudDeleteOpen || s.baudDeleteError != (localizedText{}) {
		t.Fatal("confirming deletion did not close the dialog cleanly")
	}
}

func cancelBaudDeleteThroughUI(t *testing.T, tt *gui.Tester, s *controller) {
	t.Helper()
	// Find Cancel inside the inner modal's focus scope, not the outer dialog.
	tt.Key(0, gui.KeyTab)
	for range 3 {
		if tt.Focused(s.tr("取消", "Cancel")) {
			tt.Key(0, gui.KeyEnter)
			return
		}
		tt.Key(0, gui.KeyTab)
	}
	t.Fatal("the deletion dialog has no focusable Cancel button")
}

func TestNumericBaudOptionsRemainNormallySelectable(t *testing.T) {
	for _, forPort := range []bool{false, true} {
		name := "main"
		if forPort {
			name = "properties"
		}
		t.Run(name, func(t *testing.T) {
			s, notified := baudSettingsFixture(t)
			original := baudSettingsSnapshot(s)
			s.configureLog()
			tt := gui.NewTester(s.view, 800, 600)
			if forPort {
				if err := tt.Click(s.tr("串口配置", "Port configuration")); err != nil {
					t.Fatal(err)
				}
			}
			trigger, found := tt.AccessByRole(gui.RolePopUpButton, s.tr("波特率", "Baud rate"))
			if !found || trigger.Disabled {
				t.Fatal("baud picker is missing or disabled")
			}
			tt.ClickAt(trigger.Bounds.X+trigger.Bounds.W/2, trigger.Bounds.Y+trigger.Bounds.H/2)
			option, found := tt.AccessByRole(gui.RoleOption, "250000")
			if !found || option.Disabled || option.Offscreen {
				t.Fatal("numeric baud option is missing or unavailable")
			}
			tt.ClickAt(option.Bounds.X+option.Bounds.W/2, option.Bounds.Y+option.Bounds.H/2)
			if s.baudDeleteOpen || tt.Menu() != nil {
				t.Fatal("normal selection opened the deletion UI")
			}
			if forPort {
				if s.portDraft.Baud != "250000" || !reflect.DeepEqual(*s.settings, original) || s.app.SettingsDirty {
					t.Fatal("normal property selection did not stay in the draft")
				}
				if err := tt.Click(s.tr("确定", "OK")); err != nil {
					t.Fatal(err)
				}
			} else {
				if s.settings.Port.Baud != "250000" || !s.app.SettingsDirty || readSavedSettings(t, s.app.SettingsPath).Port.Baud != original.Port.Baud {
					t.Fatal("normal selection did not remain dirty until use")
				}
				s.app.SaveUsedSettings()
			}
			completeSettingsSave(t, s, notified)
			stored := readSavedSettings(t, s.app.SettingsPath)
			if stored.Port.Baud != "250000" || !reflect.DeepEqual(stored, *s.settings) || !slices.Equal(stored.BaudRates, original.BaudRates) {
				t.Fatal("ordinary clicking did not save the selected baud without changing the list")
			}
		})
	}
}

func TestDeletingUnselectedBaudPreservesSelection(t *testing.T) {
	s, notified := baudSettingsFixture(t)
	s.settings.BaudRates = []string{"9600", "4294967295"}
	s.settings.Port.Baud = "4294967295"
	if err := config.Save(s.app.SettingsPath, *s.settings); err != nil {
		t.Fatal(err)
	}
	s.configureLog()
	tt := gui.NewTester(s.view, 800, 600)
	openBaudDeleteThroughUI(t, tt, s, "9600", false)
	confirmBaudDeleteThroughUI(t, tt, s)
	completeSettingsSave(t, s, notified)
	stored := readSavedSettings(t, s.app.SettingsPath)
	requireBaudRates(t, stored.BaudRates, []string{"4294967295"})
	if stored.Port.Baud != "4294967295" || !reflect.DeepEqual(stored, *s.settings) || s.app.SettingsDirty {
		t.Fatal("deleting an unselected baud changed the selection or failed to persist")
	}
	restarted := newController(&internal.App{Settings: stored})
	restarted.app.InitializeBaudRates()
	requireBaudRates(t, restarted.settings.BaudRates, []string{"4294967295"})
}

func TestMainBaudDeletionSelectsFirstThenDisablesOpenWhenEmpty(t *testing.T) {
	s, notified := baudSettingsFixture(t)
	s.settings.BaudRates = []string{"9600", "115200", "250000"}
	if err := config.Save(s.app.SettingsPath, *s.settings); err != nil {
		t.Fatal(err)
	}
	s.configureLog()
	tt := gui.NewTester(s.view, 800, 600)
	for _, test := range []struct {
		deleted, selected string
		rates             []string
	}{
		{"115200", "9600", []string{"9600", "250000"}},
		{"9600", "250000", []string{"250000"}},
		{"250000", "", []string{}},
	} {
		openBaudDeleteThroughUI(t, tt, s, test.deleted, false)
		confirmBaudDeleteThroughUI(t, tt, s)
		completeSettingsSave(t, s, notified)
		stored := readSavedSettings(t, s.app.SettingsPath)
		requireBaudRates(t, stored.BaudRates, test.rates)
		if stored.Port.Baud != test.selected || !reflect.DeepEqual(stored, *s.settings) || s.app.SettingsDirty {
			t.Fatalf("deleting %s selected %q, want %q", test.deleted, stored.Port.Baud, test.selected)
		}
		tt.Frame()
		picker, found := tt.AccessByRole(gui.RolePopUpButton, s.tr("波特率", "Baud rate"))
		if !found || picker.Value != test.selected {
			t.Fatalf("baud picker does not display the surviving selection %q", test.selected)
		}
		button, found := tt.AccessByRole(gui.RoleButton, s.tr("打开", "Open"))
		if !found || button.Disabled != (test.selected == "") {
			t.Fatal("Open availability does not follow whether a baud remains")
		}
	}
	restarted := newController(&internal.App{Settings: readSavedSettings(t, s.app.SettingsPath)})
	restarted.app.InitializeBaudRates()
	if restarted.settings.Port.Baud != "" || len(restarted.settings.BaudRates) != 0 {
		t.Fatal("restarting revived an option after deleting the entire list")
	}
}

func TestBaudDeleteCancelAndEscapeDoNotSelectOrSave(t *testing.T) {
	for _, forPort := range []bool{false, true} {
		name := "main"
		if forPort {
			name = "properties"
		}
		t.Run(name, func(t *testing.T) {
			for _, dismissal := range []string{"Cancel", "Escape"} {
				t.Run(dismissal, func(t *testing.T) {
					s, notified := baudSettingsFixture(t)
					original := baudSettingsSnapshot(s)
					s.configureLog()
					tt := gui.NewTester(s.view, 800, 600)
					if forPort {
						if err := tt.Click(s.tr("串口配置", "Port configuration")); err != nil {
							t.Fatal(err)
						}
					}
					draft, ratesDraft, revision := s.portDraft, slices.Clone(s.portBaudRatesDraft), s.app.SettingsRevision
					openBaudDeleteThroughUI(t, tt, s, "250000", forPort)
					if dismissal == "Escape" {
						tt.Key(0, gui.KeyEscape)
					} else {
						cancelBaudDeleteThroughUI(t, tt, s)
					}
					if s.baudDeleteOpen || s.portSettingsOpen != forPort || !reflect.DeepEqual(*s.settings, original) || s.portDraft != draft || !slices.Equal(s.portBaudRatesDraft, ratesDraft) || s.app.SettingsRevision != revision || s.app.SettingsDirty {
						t.Fatal("dismissing deletion selected an option, saved an edit, or dismissed the outer dialog")
					}
					requireBaudUnwritten(t, s, notified, original)
				})
			}
		})
	}
}

func TestPropertyBaudDeletionCommitsOnlyAtOuterConfirmation(t *testing.T) {
	for _, dismissal := range []string{"OK", "Cancel", "Escape"} {
		t.Run(dismissal, func(t *testing.T) {
			s, notified := baudSettingsFixture(t)
			original := baudSettingsSnapshot(s)
			s.configureLog()
			tt := gui.NewTester(s.view, 800, 600)
			if err := tt.Click(s.tr("串口配置", "Port configuration")); err != nil {
				t.Fatal(err)
			}
			openBaudDeleteThroughUI(t, tt, s, "115200", true)
			confirmBaudDeleteThroughUI(t, tt, s)
			requireBaudRates(t, s.portBaudRatesDraft, []string{"250000", "500000"})
			if !s.portSettingsOpen || s.portDraft.Baud != "250000" || !reflect.DeepEqual(*s.settings, original) || s.app.SettingsDirty || !reflect.DeepEqual(readSavedSettings(t, s.app.SettingsPath), original) {
				t.Fatal("property deletion did not remain isolated in the draft with its fallback selection")
			}
			if dismissal == "Escape" {
				tt.Key(0, gui.KeyEscape)
			} else {
				label := s.tr("取消", "Cancel")
				if dismissal == "OK" {
					label = s.tr("确定", "OK")
				}
				if err := tt.Click(label); err != nil {
					t.Fatal(err)
				}
			}
			if s.portSettingsOpen {
				t.Fatal("outer dismissal left properties open")
			}
			if dismissal == "OK" {
				completeSettingsSave(t, s, notified)
				stored := readSavedSettings(t, s.app.SettingsPath)
				requireBaudRates(t, stored.BaudRates, []string{"250000", "500000"})
				if stored.Port.Baud != "250000" || !reflect.DeepEqual(stored, *s.settings) || s.app.SettingsDirty {
					t.Fatal("outer OK did not persist deletion and fallback selection together")
				}
			} else {
				if !reflect.DeepEqual(*s.settings, original) || s.app.SettingsDirty {
					t.Fatal("outer cancellation committed the deletion")
				}
				if err := tt.Click(s.tr("串口配置", "Port configuration")); err != nil {
					t.Fatal(err)
				}
				if s.portDraft != original.Port || !slices.Equal(s.portBaudRatesDraft, original.BaudRates) {
					t.Fatal("reopening properties retained a canceled deletion")
				}
				requireBaudUnwritten(t, s, notified, original)
			}
		})
	}
}

func TestBaudDigitsCenteredInTextArea(t *testing.T) {
	for _, value := range []string{"9600", "115200", "1000000"} {
		t.Run(value, func(t *testing.T) {
			s := newController(&internal.App{Settings: config.Settings{
				Language: "en", Port: serialport.Settings{Baud: value}, BaudRates: []string{value},
			}})
			tt := gui.NewTester(func(c *gui.Context) {
				c.SetTheme(lightTheme)
				gui.Row(c).Padding(10).Children(func() { s.baudPicker(c, false, controlHeight) })
			}, 180, 60)
			tt.SetScale(2)
			picker, found := tt.AccessByRole(gui.RolePopUpButton, "Baud rate")
			if !found {
				t.Fatal("baud selector is missing")
			}
			left := picker.Bounds.X + baudPickerBorderWidth + baudPickerPaddingX
			right := picker.Bounds.X + picker.Bounds.W - baudPickerBorderWidth - baudPickerPaddingX - baudPickerArrowSize - baudPickerGap
			img := tt.Image()
			minX, maxX := img.Bounds().Max.X, -1
			for y := int((picker.Bounds.Y + 4) * 2); y < int((picker.Bounds.Y + picker.Bounds.H - 4) * 2); y++ {
				for x := int(left * 2); x < int(right * 2); x++ {
					pixel := img.RGBAAt(x, y)
					if pixel.R < 100 && pixel.G < 100 && pixel.B < 100 {
						minX, maxX = min(minX, x), max(maxX, x)
					}
				}
			}
			if maxX < minX {
				t.Fatal("rendered baud digits are missing")
			}
			inkCenter := float32(minX+maxX+1) / 4
			textAreaCenter := (left + right) / 2
			if inkCenter < textAreaCenter-1 || inkCenter > textAreaCenter+1 {
				t.Errorf("rendered digits are not centered in the text area: inkCenter=%.2f textAreaCenter=%.2f", inkCenter, textAreaCenter)
			}
		})
	}
}
