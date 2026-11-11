package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/fcying/SerialGo/communication/serialport"
	"github.com/fcying/SerialGo/config"

	"github.com/BurntSushi/toml"
	gui "github.com/egoist/mygo/ui"
)

func baudSettingsFixture(t *testing.T) (*controller, <-chan struct{}) {
	t.Helper()
	s, notified := settingsSaveFixture(t, filepath.Join(t.TempDir(), "config.toml"), config.Save)
	s.settings.Port = serialport.Settings{
		Name: "COM17", Baud: "115200", DataBits: "8", Parity: "None", StopBits: "1", DTR: true,
	}
	s.settings.BaudRates = []string{"115200", "250000", "500000"}
	if err := config.Save(s.path, s.settings); err != nil {
		t.Fatal(err)
	}
	return s, notified
}

func baudSettingsSnapshot(s *controller) config.Settings {
	settings := s.settings
	settings.BaudRates = slices.Clone(settings.BaudRates)
	return settings
}

func requireBaudRates(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("baud rates = %v, want %v", got, want)
	}
}

func requireBaudUnwritten(t *testing.T, s *controller, notified <-chan struct{}, want config.Settings) {
	t.Helper()
	s.stopSettingsSaver()
	select {
	case <-notified:
		t.Fatal("unconfirmed baud edit invoked the settings writer")
	default:
	}
	if got := readSavedSettings(t, s.path); !reflect.DeepEqual(got, want) {
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

func TestBaudRateChoicesNormalizeOnlyConfiguredValues(t *testing.T) {
	for _, test := range []struct {
		name  string
		rates []string
		want  []string
	}{
		{"absent", nil, nil},
		{"explicit empty", []string{}, nil},
		{"numeric sorting", []string{"500000", "38400", "250000"}, []string{"38400", "250000", "500000"}},
		{"canonical duplicates", []string{"00115200", "0250000", "250000", "250000"}, []string{"115200", "250000"}},
		{"invalid values removed", []string{"0", "-1", "4294967296", "invalid", "250000"}, []string{"250000"}},
		{"maximum", []string{"4294967295"}, []string{"4294967295"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := baudRateChoices(test.rates); !slices.Equal(got, test.want) {
				t.Fatalf("baud choices = %v, want %v", got, test.want)
			}
		})
	}
}

func TestInitializeBaudRatesNormalizesAndHonorsDeletedValues(t *testing.T) {
	for _, test := range []struct {
		name, current, wantCurrent string
		rates, wantRates           []string
	}{
		{"retained selection", "38400", "38400", []string{"250000", "38400"}, []string{"38400", "250000"}},
		{"deleted current selects first", "38400", "250000", []string{"500000", "250000"}, []string{"250000", "500000"}},
		{"explicit empty stays empty", "38400", "", []string{}, nil},
		{"absent stays empty", "115200", "", nil, nil},
		{"canonical deduplication", "0038400", "38400", []string{"0038400", "38400", "0250000"}, []string{"38400", "250000"}},
		{"invalid selection", "Custom...", "250000", []string{"250000"}, []string{"250000"}},
		{"invalid values removed", "38400", "250000", []string{"0", "-1", "4294967296", "invalid", "250000"}, []string{"250000"}},
		{"maximum selected", "4294967295", "4294967295", []string{"4294967295"}, []string{"4294967295"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := &controller{settings: config.Settings{BaudRates: slices.Clone(test.rates)}}
			s.settings.Port.Baud = test.current
			s.initializeBaudRates()
			if s.settings.Port.Baud != test.wantCurrent {
				t.Fatalf("startup selected baud = %q, want %q", s.settings.Port.Baud, test.wantCurrent)
			}
			requireBaudRates(t, s.settings.BaudRates, test.wantRates)
		})
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
		stored := readSavedSettings(t, s.path)
		requireBaudRates(t, stored.BaudRates, test.want)
		if stored.Port.Baud != test.selected || !reflect.DeepEqual(s.settings, stored) || s.settingsDirty {
			t.Fatalf("confirmation did not persist numeric baud and accumulated list: selected=%q rates=%v dirty=%v", stored.Port.Baud, stored.BaudRates, s.settingsDirty)
		}
	}
	original := baudSettingsSnapshot(s)
	s.openCustomBaud(false)
	s.customBaudText = "250000"
	s.applyCustomBaud()
	if !reflect.DeepEqual(s.settings, original) {
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
					if !reflect.DeepEqual(s.settings, original) || s.portDraft != draft || !slices.Equal(s.portBaudRatesDraft, ratesDraft) || s.settingsDirty {
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
						if err := tt.Click(s.tr("串口设置", "Port settings")); err != nil {
							t.Fatal(err)
						}
					}
					draft, ratesDraft := s.portDraft, slices.Clone(s.portBaudRatesDraft)
					clickBaudCustomAction(t, tt, s)
					if !reflect.DeepEqual(s.settings, original) || s.portDraft != draft {
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
					if s.customBaudOpen || !reflect.DeepEqual(s.settings, original) || s.portDraft != draft || !slices.Equal(s.portBaudRatesDraft, ratesDraft) || s.settingsDirty {
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
			if err := tt.Click(s.tr("串口设置", "Port settings")); err != nil {
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
			if s.portDraft.Baud != "750000" || !reflect.DeepEqual(s.settings, original) || s.settingsDirty || !reflect.DeepEqual(readSavedSettings(t, s.path), original) {
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
				stored := readSavedSettings(t, s.path)
				requireBaudRates(t, stored.BaudRates, staged)
				if stored.Port.Baud != "750000" || !reflect.DeepEqual(s.settings, stored) || s.settingsDirty {
					t.Fatal("outer confirmation did not persist the accumulated list and selected baud together")
				}
			} else {
				if !reflect.DeepEqual(s.settings, original) || s.settingsDirty {
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
	if !reflect.DeepEqual(s.settings, original) {
		t.Fatal("editing the property list mutated active settings through a shared slice")
	}
	s.portSettingsOpen = false
	s.openPortSettings()
	if !slices.Equal(s.portBaudRatesDraft, original.BaudRates) {
		t.Fatal("reopening properties retained the discarded list mutation")
	}
	requireBaudUnwritten(t, s, notified, original)
}

func TestConnectedCustomBaudConfirmationCannotChangeSettingsOrDraft(t *testing.T) {
	for _, forPort := range []bool{false, true} {
		name := "main"
		if forPort {
			name = "properties"
		}
		t.Run(name, func(t *testing.T) {
			s, notified := baudSettingsFixture(t)
			original := baudSettingsSnapshot(s)
			s.openPortSettings()
			draft, ratesDraft := s.portDraft, slices.Clone(s.portBaudRatesDraft)
			s.openCustomBaud(forPort)
			s.customBaudText = "38400"
			closedPortSettingsSession(t, s)
			s.applyCustomBaud()
			if !reflect.DeepEqual(s.settings, original) || s.portDraft != draft || !slices.Equal(s.portBaudRatesDraft, ratesDraft) || s.settingsDirty {
				t.Fatal("confirmation after connecting changed baud settings or drafts")
			}
			requireBaudUnwritten(t, s, notified, original)
		})
	}
}

func TestConnectedMainBaudPickerIsDisabled(t *testing.T) {
	s, _ := baudSettingsFixture(t)
	closedPortSettingsSession(t, s)
	s.configureLog()
	tt := gui.NewTester(s.view, 800, 600)
	field, found := tt.AccessByRole(gui.RolePopUpButton, s.tr("波特率", "Baud rate"))
	if !found || !field.Disabled {
		t.Fatal("connected main baud picker is missing or enabled")
	}
}

func TestPortSelectionPreservesGlobalBaudAndFraming(t *testing.T) {
	s, notified := baudSettingsFixture(t)
	s.settings.Port.Baud = "250000"
	s.settings.Port.DataBits, s.settings.Port.Parity, s.settings.Port.StopBits = "7", "Even", "2"
	s.settings.Port.RTS = true
	original := baudSettingsSnapshot(s)
	s.setPorts([]serialport.Info{
		{Name: "COM17", Description: "Original adapter"},
		{Name: "COM29", Description: "Another adapter"},
	})
	s.portChoice = "COM29 Another adapter"
	s.selectPort()
	want := original
	want.Port.Name = "COM29"
	if !reflect.DeepEqual(s.settings, want) {
		t.Fatal("selecting another device changed global custom bauds, framing, or control lines")
	}
	s.saveUsedSettings()
	completeSettingsSave(t, s, notified)
	if !reflect.DeepEqual(readSavedSettings(t, s.path), want) {
		t.Fatal("device selection did not persist global serial parameters unchanged")
	}
	s.setPorts([]serialport.Info{{Name: "COM99"}})
	want.Port.Name = "COM99"
	if !reflect.DeepEqual(s.settings, want) {
		t.Fatal("device refresh fallback reset global serial parameters")
	}
}

func TestPropertyCustomBaudStagedBeforeConnectionCannotCommit(t *testing.T) {
	s, notified := baudSettingsFixture(t)
	original := baudSettingsSnapshot(s)
	s.openPortSettings()
	s.openCustomBaud(true)
	s.customBaudText = "38400"
	s.applyCustomBaud()
	if s.portDraft.Baud != "38400" {
		t.Fatal("custom baud was not staged before connection")
	}
	requireBaudRates(t, s.portBaudRatesDraft, []string{"38400", "115200", "250000", "500000"})
	closedPortSettingsSession(t, s)
	s.applyPortSettings()
	if !reflect.DeepEqual(s.settings, original) || s.settingsDirty {
		t.Fatal("connecting before outer confirmation committed a staged baud or custom list")
	}
	requireBaudUnwritten(t, s, notified, original)
}

func TestDeletedBaudRatesDoNotReturnOnRestart(t *testing.T) {
	for _, test := range []struct {
		list, selected string
	}{
		{"[]", ""},
		{"[\"250000\"]", "250000"},
	} {
		t.Run(test.list, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			source := "BaudRates = " + test.list + "\n[Port]\nBaud = \"115200\"\n"
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			s := &controller{}
			if _, err := toml.DecodeFile(path, &s.settings); err != nil {
				t.Fatal(err)
			}
			s.initializeBaudRates()
			if s.settings.Port.Baud != test.selected || slices.Contains(s.settings.BaudRates, "115200") || slices.Contains(baudRateChoices(s.settings.BaudRates), "115200") {
				t.Fatal("startup revived a deleted baud")
			}
			if err := config.Save(path, s.settings); err != nil {
				t.Fatal(err)
			}
			restarted := &controller{settings: readSavedSettings(t, path)}
			restarted.initializeBaudRates()
			if restarted.settings.Port.Baud != test.selected || !slices.Equal(restarted.settings.BaudRates, s.settings.BaudRates) {
				t.Fatal("deletion did not survive persistence and a second restart")
			}
		})
	}
}

func openBaudDeleteThroughUI(t *testing.T, tt *gui.Tester, s *controller, value string, forPort bool) {
	t.Helper()
	original := baudSettingsSnapshot(s)
	draft, ratesDraft, revision := s.portDraft, slices.Clone(s.portBaudRatesDraft), s.settingsRevision
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
	if !reflect.DeepEqual(s.settings, original) || s.portDraft != draft || !slices.Equal(s.portBaudRatesDraft, ratesDraft) || s.settingsRevision != revision {
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
	if !reflect.DeepEqual(s.settings, original) || s.portDraft != draft || !slices.Equal(s.portBaudRatesDraft, ratesDraft) || s.settingsRevision != revision || s.settingsDirty {
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
				if err := tt.Click(s.tr("串口设置", "Port settings")); err != nil {
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
				if s.portDraft.Baud != "250000" || !reflect.DeepEqual(s.settings, original) || s.settingsDirty {
					t.Fatal("normal property selection did not stay in the draft")
				}
				if err := tt.Click(s.tr("确定", "OK")); err != nil {
					t.Fatal(err)
				}
			} else {
				if s.settings.Port.Baud != "250000" || !s.settingsDirty || readSavedSettings(t, s.path).Port.Baud != original.Port.Baud {
					t.Fatal("normal selection did not remain dirty until use")
				}
				s.saveUsedSettings()
			}
			completeSettingsSave(t, s, notified)
			stored := readSavedSettings(t, s.path)
			if stored.Port.Baud != "250000" || !reflect.DeepEqual(stored, s.settings) || !slices.Equal(stored.BaudRates, original.BaudRates) {
				t.Fatal("ordinary clicking did not save the selected baud without changing the list")
			}
		})
	}
}

func TestDeletingUnselectedBaudPreservesSelection(t *testing.T) {
	s, notified := baudSettingsFixture(t)
	s.settings.BaudRates = []string{"9600", "4294967295"}
	s.settings.Port.Baud = "4294967295"
	if err := config.Save(s.path, s.settings); err != nil {
		t.Fatal(err)
	}
	s.configureLog()
	tt := gui.NewTester(s.view, 800, 600)
	openBaudDeleteThroughUI(t, tt, s, "9600", false)
	confirmBaudDeleteThroughUI(t, tt, s)
	completeSettingsSave(t, s, notified)
	stored := readSavedSettings(t, s.path)
	requireBaudRates(t, stored.BaudRates, []string{"4294967295"})
	if stored.Port.Baud != "4294967295" || !reflect.DeepEqual(stored, s.settings) || s.settingsDirty {
		t.Fatal("deleting an unselected baud changed the selection or failed to persist")
	}
	restarted := &controller{settings: stored}
	restarted.initializeBaudRates()
	requireBaudRates(t, baudRateChoices(restarted.settings.BaudRates), []string{"4294967295"})
}

func TestMainBaudDeletionSelectsFirstThenDisablesOpenWhenEmpty(t *testing.T) {
	s, notified := baudSettingsFixture(t)
	s.settings.BaudRates = []string{"9600", "115200", "250000"}
	if err := config.Save(s.path, s.settings); err != nil {
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
		stored := readSavedSettings(t, s.path)
		requireBaudRates(t, stored.BaudRates, test.rates)
		if stored.Port.Baud != test.selected || !reflect.DeepEqual(stored, s.settings) || s.settingsDirty {
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
	restarted := &controller{settings: readSavedSettings(t, s.path)}
	restarted.initializeBaudRates()
	if restarted.settings.Port.Baud != "" || len(baudRateChoices(restarted.settings.BaudRates)) != 0 {
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
						if err := tt.Click(s.tr("串口设置", "Port settings")); err != nil {
							t.Fatal(err)
						}
					}
					draft, ratesDraft, revision := s.portDraft, slices.Clone(s.portBaudRatesDraft), s.settingsRevision
					openBaudDeleteThroughUI(t, tt, s, "250000", forPort)
					if dismissal == "Escape" {
						tt.Key(0, gui.KeyEscape)
					} else {
						cancelBaudDeleteThroughUI(t, tt, s)
					}
					if s.baudDeleteOpen || s.portSettingsOpen != forPort || !reflect.DeepEqual(s.settings, original) || s.portDraft != draft || !slices.Equal(s.portBaudRatesDraft, ratesDraft) || s.settingsRevision != revision || s.settingsDirty {
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
			if err := tt.Click(s.tr("串口设置", "Port settings")); err != nil {
				t.Fatal(err)
			}
			openBaudDeleteThroughUI(t, tt, s, "115200", true)
			confirmBaudDeleteThroughUI(t, tt, s)
			requireBaudRates(t, s.portBaudRatesDraft, []string{"250000", "500000"})
			if !s.portSettingsOpen || s.portDraft.Baud != "250000" || !reflect.DeepEqual(s.settings, original) || s.settingsDirty || !reflect.DeepEqual(readSavedSettings(t, s.path), original) {
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
				stored := readSavedSettings(t, s.path)
				requireBaudRates(t, stored.BaudRates, []string{"250000", "500000"})
				if stored.Port.Baud != "250000" || !reflect.DeepEqual(stored, s.settings) || s.settingsDirty {
					t.Fatal("outer OK did not persist deletion and fallback selection together")
				}
			} else {
				if !reflect.DeepEqual(s.settings, original) || s.settingsDirty {
					t.Fatal("outer cancellation committed the deletion")
				}
				if err := tt.Click(s.tr("串口设置", "Port settings")); err != nil {
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

func TestConnectedBaudDeletionCannotBeRequestedOrCommitted(t *testing.T) {
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
				if err := tt.Click(s.tr("串口设置", "Port settings")); err != nil {
					t.Fatal(err)
				}
			}
			draft, ratesDraft, revision := s.portDraft, slices.Clone(s.portBaudRatesDraft), s.settingsRevision
			openBaudDeleteThroughUI(t, tt, s, "115200", forPort)
			closedPortSettingsSession(t, s)
			s.applyBaudDelete()
			if !reflect.DeepEqual(s.settings, original) || s.portDraft != draft || !slices.Equal(s.portBaudRatesDraft, ratesDraft) || s.settingsRevision != revision || s.settingsDirty {
				t.Fatal("connecting before deletion confirmation changed settings or the draft")
			}
			s.baudDeleteOpen = false
			s.openBaudDelete("250000", forPort)
			if s.baudDeleteOpen || !reflect.DeepEqual(s.settings, original) || s.portDraft != draft || !slices.Equal(s.portBaudRatesDraft, ratesDraft) || s.settingsRevision != revision || s.settingsDirty {
				t.Fatal("connected deletion request opened a dialog or changed settings")
			}
			requireBaudUnwritten(t, s, notified, original)
		})
	}
}

func TestPropertyBaudDeletionStagedBeforeConnectionCannotCommit(t *testing.T) {
	s, notified := baudSettingsFixture(t)
	original := baudSettingsSnapshot(s)
	s.configureLog()
	tt := gui.NewTester(s.view, 800, 600)
	if err := tt.Click(s.tr("串口设置", "Port settings")); err != nil {
		t.Fatal(err)
	}
	openBaudDeleteThroughUI(t, tt, s, "115200", true)
	confirmBaudDeleteThroughUI(t, tt, s)
	requireBaudRates(t, s.portBaudRatesDraft, []string{"250000", "500000"})
	closedPortSettingsSession(t, s)
	tt.Frame()
	if err := tt.Click(s.tr("确定", "OK")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.settings, original) || s.settingsDirty {
		t.Fatal("connecting before outer confirmation committed a staged deletion")
	}
	requireBaudUnwritten(t, s, notified, original)
}
