package internal

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fcying/SerialGo/communication/serialport"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal/i18n"
)

func connectedAppFixture(t *testing.T) *App {
	t.Helper()
	p := NewSession(&serialport.Port{}, 115200)
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	return &App{Settings: config.Settings{
		Language: "en", BaudRates: []string{"9600", "115200", "250000"},
		Port: serialport.Settings{Name: "COM17", Baud: "115200", DataBits: "8", Parity: "None", StopBits: "1"},
	}, session: p}
}

func TestPortSettingsConnectedFramingRemainsUnchanged(t *testing.T) {
	s := connectedAppFixture(t)
	before := s.Settings
	draft, rates := s.PortSettingsDraft()
	draft.Name, draft.Baud, draft.DataBits, draft.Parity, draft.StopBits = "friendly label", "9600", "7", "Odd", "2"
	if err := s.ApplyPortSettings(draft, rates[:1]); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Settings, before) || s.SettingsDirty || s.SettingsRevision != 0 {
		t.Fatal("connected properties changed raw name, framing or baud list")
	}
}

func TestPortRefreshErrorPreservesLastKnownSelection(t *testing.T) {
	app := &App{Settings: config.Settings{Port: serialport.Settings{Name: "COM7"}}}
	app.SetPorts([]serialport.Info{{Name: "COM7", Description: "USB adapter"}})
	portSettings := app.Settings.Port
	portLabels := slices.Clone(app.PortLabels)
	portChoice := app.PortChoice
	revision := app.SettingsRevision
	app.SettingsDirty = false
	app.PortBusy = true
	app.RefreshingPorts = true

	app.applyPortRefresh(nil, errors.New("temporary enumeration failure"))
	if app.PortBusy || app.RefreshingPorts || !app.StatusError {
		t.Fatal("failed enumeration did not finish with an error status")
	}
	if app.Settings.Port != portSettings || app.PortChoice != portChoice || !slices.Equal(app.PortLabels, portLabels) || app.SettingsDirty || app.SettingsRevision != revision {
		t.Fatal("failed enumeration changed the saved port, visible list or settings revision")
	}

	app.PortBusy = true
	app.RefreshingPorts = true
	app.applyPortRefresh([]serialport.Info{{Name: "COM7", Description: "updated adapter"}, {Name: "COM8"}}, nil)
	if app.PortBusy || app.RefreshingPorts || app.Settings.Port.Name != "COM7" || app.PortChoice != "COM7 updated adapter" || len(app.PortLabels) != 2 || app.StatusError {
		t.Fatalf("successful retry did not refresh the retained selection: %+v", app)
	}
}

func TestPortSettingsLineFailureKeepsSettingsAndStatus(t *testing.T) {
	for _, line := range []string{"DTR", "RTS"} {
		for _, dirty := range []bool{false, true} {
			t.Run(line+"/"+map[bool]string{false: "clean", true: "dirty"}[dirty], func(t *testing.T) {
				s := connectedAppFixture(t)
				s.SettingsDirty = dirty
				s.Status = Text{ZH: "原状态", EN: "Original status"}
				before, status, revision := s.Settings, s.Status, s.SettingsRevision
				draft, rates := s.PortSettingsDraft()
				if line == "DTR" {
					draft.DTR = true
				} else {
					draft.RTS = true
				}
				for range 2 {
					err := s.ApplyPortSettings(draft, rates)
					if err == nil || !strings.Contains(i18n.ErrorText(err, true), os.ErrClosed.Error()) || i18n.ErrorText(err, true) == i18n.ErrorText(err, false) {
						t.Fatalf("missing localized driver error: %v", err)
					}
					if !reflect.DeepEqual(s.Settings, before) || s.SettingsDirty != dirty || s.SettingsRevision != revision || s.Status != status || s.StatusError {
						t.Fatal("failed control-line operation changed settings or main status")
					}
				}
			})
		}
	}
}

func TestConnectedBaudCommandsCannotChangeSettingsOrDraft(t *testing.T) {
	for _, forPort := range []bool{false, true} {
		s := connectedAppFixture(t)
		before := s.Settings
		draft, rates := s.PortSettingsDraft()
		initialDraft, initialRates := draft, slices.Clone(rates)
		var err error
		if forPort {
			err = s.CustomBaud("38400", &draft, &rates)
		} else {
			err = s.CustomBaud("38400", nil, nil)
		}
		if err == nil {
			t.Fatal("connected custom baud accepted")
		}
		if forPort {
			err = s.DeleteBaud("115200", &draft, &rates)
		} else {
			err = s.DeleteBaud("115200", nil, nil)
		}
		if err == nil {
			t.Fatal("connected baud deletion accepted")
		}
		s.SelectBaud("9600")
		if !reflect.DeepEqual(s.Settings, before) || draft != initialDraft || !slices.Equal(rates, initialRates) || s.SettingsDirty {
			t.Fatal("connected baud command mutated settings or draft")
		}
	}
}

func TestPropertyBaudChangesStagedBeforeConnectionCannotCommit(t *testing.T) {
	for _, deletion := range []bool{false, true} {
		connected := connectedAppFixture(t)
		s := &App{Settings: connected.Settings}
		before := s.Settings
		draft, rates := s.PortSettingsDraft()
		var err error
		if deletion {
			err = s.DeleteBaud("115200", &draft, &rates)
		} else {
			err = s.CustomBaud("38400", &draft, &rates)
		}
		if err != nil {
			t.Fatal(err)
		}
		s.session = connected.session
		if err := s.ApplyPortSettings(draft, rates); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(s.Settings, before) || s.SettingsDirty {
			t.Fatal("connection allowed staged baud mutation to commit")
		}
	}
}

func TestPresetLimitAndActiveSendPreventStructuralEdits(t *testing.T) {
	s := &App{}
	for range config.PresetCount {
		if !s.AddPreset() {
			t.Fatal("preset added below limit was rejected")
		}
	}
	if s.AddPreset() || len(s.Settings.Presets) != config.PresetCount {
		t.Fatal("preset limit exceeded")
	}
	s.Settings.Presets = []config.Preset{{Label: "active", Data: "keep"}}
	s.cancelSend = func() {}
	revision := s.SettingsRevision
	if s.AddPreset() || s.DeletePreset(0) || s.DeletePreset(-1) || s.SettingsRevision != revision || len(s.Settings.Presets) != 1 || s.Settings.Presets[0].Data != "keep" {
		t.Fatal("active send allowed structural edits")
	}
}

type appTestView struct {
	callbacks chan func()
	records   []Event
}

func (v *appTestView) Dispatch(fn func())          { v.callbacks <- fn }
func (v *appTestView) AppendEvents(events []Event) { v.records = append(v.records, events...) }

func awaitAppCallback(t *testing.T, v *appTestView) {
	t.Helper()
	select {
	case fn := <-v.callbacks:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("application worker did not post completion")
	}
}

func TestAppDrainsSnapshotAndOwnsCountsAndCaptureFailure(t *testing.T) {
	v := &appTestView{}
	p := &Session{ctx: context.Background(), events: make(chan Event, 1)}
	s := &App{view: v, session: p, CapturePath: "capture.bin"}
	p.emit(Event{Data: []byte{1, 2}, Sent: true})
	p.emit(Event{Data: make([]byte, displayBufferSize)})
	p.emit(Event{Data: []byte("latest")})
	if len(p.Events()) != 0 {
		t.Fatal("ordinary display data occupied the error channel")
	}
	p.events <- Event{Err: io.ErrShortWrite}
	s.drain(p)
	if s.Tx != 2 || s.Rx != uint64(displayBufferSize+len("latest")) || len(v.records) != 1 || string(v.records[0].Data) != "latest" || s.CapturePath != "" || !s.StatusError {
		t.Fatal("display overflow lost counters, retained stale data, or hid an error")
	}
	s.ClearCounts()
	p.emit(Event{Data: []byte("pending")})
	s.ClearCounts()
	s.drain(p)
	if s.Tx != 0 || s.Rx != 0 {
		t.Fatal("pending pre-reset display bytes were counted after reset")
	}
	p.emit(Event{Data: []byte("new")})
	s.drain(p)
	if s.Tx != 0 || s.Rx != 3 {
		t.Fatal("post-reset receive bytes were not counted")
	}
}

func TestAppPumpDrainsPendingDisplayEvents(t *testing.T) {
	v := &appTestView{callbacks: make(chan func(), 1)}
	p := &Session{events: make(chan Event, 1)}
	s := &App{view: v, session: p, closed: make(chan struct{})}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		s.pump(p, stop)
		close(done)
	}()
	defer func() {
		close(stop)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("application event pump did not stop")
		}
	}()
	p.emit(Event{Data: []byte("received")})
	awaitAppCallback(t, v)
	if s.Rx != uint64(len("received")) || len(v.records) != 1 || string(v.records[0].Data) != "received" {
		t.Fatal("event pump did not drain and display queued receive data")
	}
}

func TestAppPumpCoalescesPendingDisplayDispatch(t *testing.T) {
	v := &appTestView{callbacks: make(chan func(), 16)}
	p := &Session{events: make(chan Event, 1)}
	s := &App{view: v, session: p, closed: make(chan struct{})}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		s.pump(p, stop)
		close(done)
	}()
	defer func() {
		close(stop)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("application event pump did not stop")
		}
	}()

	p.emit(Event{Data: []byte("first")})
	var first func()
	select {
	case first = <-v.callbacks:
	case <-time.After(5 * time.Second):
		t.Fatal("application event pump did not dispatch receive data")
	}
	time.Sleep(10 * eventPumpInterval)
	if len(v.callbacks) != 0 {
		t.Fatalf("queued %d duplicate UI updates while the first was pending", len(v.callbacks))
	}
	first()
	if s.Rx != uint64(len("first")) || len(v.records) != 1 || string(v.records[0].Data) != "first" {
		t.Fatal("pending UI update did not drain its display batch")
	}

	p.emit(Event{Data: []byte("next")})
	awaitAppCallback(t, v)
	if s.Rx != uint64(len("firstnext")) || len(v.records) != 2 || string(v.records[1].Data) != "next" {
		t.Fatal("event pump did not schedule a new update after draining the previous one")
	}
}

func TestSaveLogAndCaptureFilesBelongToApp(t *testing.T) {
	dir := t.TempDir()
	v := &appTestView{callbacks: make(chan func(), 8)}
	s := &App{view: v}
	logPath := filepath.Join(dir, "display.txt")
	s.SaveLog(logPath, "displayed data")
	if !s.FileBusy {
		t.Fatal("display save did not expose pending work")
	}
	awaitAppCallback(t, v)
	data, err := os.ReadFile(logPath)
	if err != nil || string(data) != "displayed data" || s.StatusError {
		t.Fatalf("display save failed: %q %v", data, err)
	}
	s.CapturePath = logPath
	s.SaveLog(logPath, "must not overwrite")
	if !s.FileBusy {
		t.Fatal("active capture path check did not expose pending work")
	}
	awaitAppCallback(t, v)
	if s.FileBusy {
		t.Fatal("active capture path check left save work pending")
	}
	data, err = os.ReadFile(logPath)
	if err != nil || string(data) != "displayed data" {
		t.Fatal("display save overwrote active capture")
	}
	aliasPath := filepath.Join(dir, "capture-alias.bin")
	if err := os.Link(logPath, aliasPath); err != nil {
		t.Fatalf("create hard-link alias: %v", err)
	}
	s.SaveLog(aliasPath, "must not overwrite through an alias")
	if !s.FileBusy {
		t.Fatal("active capture alias check did not expose pending work")
	}
	awaitAppCallback(t, v)
	if s.FileBusy {
		t.Fatal("active capture alias check left save work pending")
	}
	data, err = os.ReadFile(logPath)
	if err != nil || string(data) != "displayed data" {
		t.Fatal("display save overwrote the active capture through a path alias")
	}
	s.CapturePath = ""
	s.session = &Session{ctx: context.Background()}
	accept := s.CaptureTarget()
	s.session = &Session{ctx: context.Background()}
	stalePath := filepath.Join(dir, "stale.bin")
	accept(stalePath)
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatal("stale capture dialog created a file")
	}
	capturePath := filepath.Join(dir, "rx.bin")
	s.CaptureTarget()(capturePath)
	awaitAppCallback(t, v)
	if s.CapturePath != capturePath || s.session.capture == nil {
		t.Fatal("capture did not transfer file ownership to session")
	}
	s.session.enqueueCapture([]byte("stop flush"))
	s.StopCapture()
	awaitAppCallback(t, v)
	if s.CapturePath != "" || s.session.capture != nil {
		t.Fatal("capture stop retained file ownership")
	}
	data, err = os.ReadFile(capturePath)
	if err != nil || string(data) != "stop flush" {
		t.Fatalf("stop did not flush buffered capture: %q %v", data, err)
	}
	s.jobs.Wait()
}

func TestStartupSettingsFallbackWarnsWithoutMaskingErrors(t *testing.T) {
	settings := config.Settings{Encoding: "ASCII", PacketGap: "0"}
	warning := normalizeStartupSettings(&settings)
	if settings.Encoding != "UTF-8" || settings.PacketGap != "20" {
		t.Fatalf("invalid settings were not reset to defaults: encoding=%q packet gap=%q", settings.Encoding, settings.PacketGap)
	}
	if warning.ZH == "" || warning.EN == "" {
		t.Fatal("startup normalization did not create a bilingual warning")
	}
	app := &App{Status: Text{ZH: "未找到串口", EN: "No serial ports found"}, settingsWarning: warning}
	app.showSettingsWarning()
	if app.Status != warning || app.StatusError || app.settingsWarning != (Text{}) {
		t.Fatalf("startup warning status = %+v; want %+v", app.Status, warning)
	}
	portError := Text{ZH: "刷新失败", EN: "Refresh failed"}
	app = &App{Status: portError, StatusError: true, settingsWarning: warning}
	app.showSettingsWarning()
	if app.Status != portError || app.settingsWarning != warning {
		t.Fatal("startup warning overwrote a port error or was lost")
	}
}

func TestStartupSettingsNormalizationKeepsValidValues(t *testing.T) {
	settings := config.Settings{Encoding: "GB18030", PacketGap: "86400000"}
	if warning := normalizeStartupSettings(&settings); warning != (Text{}) {
		t.Fatalf("valid startup settings produced a warning: %+v", warning)
	}
	if settings.Encoding != "GB18030" || settings.PacketGap != "86400000" {
		t.Fatalf("valid startup settings changed: %+v", settings)
	}
}

func TestSendValidationAndCancellationRemainFrameworkIndependent(t *testing.T) {
	p := &Session{ctx: context.Background(), events: make(chan Event, 1)}
	s := &App{session: p, Settings: config.Settings{SendHex: true, Encoding: "UTF-8"}}
	s.SendText("not hex")
	if !s.StatusError || s.Sending() {
		t.Fatal("invalid send started a job")
	}
	s.StatusError = false
	s.Settings.SendHex = false
	s.Settings.Interval = "0"
	s.Repeat = true
	s.SendText("hello")
	if !s.StatusError || s.Sending() {
		t.Fatal("invalid repeat interval started a job")
	}
	s.StatusError = false
	s.Settings.Presets = []config.Preset{{Enabled: true, Hex: true, Data: "zz"}}
	s.SendPresets(-1)
	if !s.StatusError || s.Sending() {
		t.Fatal("invalid preset started a job")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelSend = cancel
	s.StopSending()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("stop did not cancel active send")
	}
}

func TestShutdownCollectsBackgroundSaveAndFlushesLatestDraft(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	v := &appTestView{callbacks: make(chan func(), 8)}
	s := &App{SettingsPath: path, Settings: config.Settings{Language: "en"}}
	s.Startup(v)
	s.Settings.SendText = "used"
	s.SettingsChanged()
	s.SaveUsedSettings()
	select {
	case <-v.callbacks:
	case <-time.After(5 * time.Second):
		t.Fatal("settings writer did not post acknowledgement")
	}
	s.Settings.SendText = "final draft"
	s.SettingsChanged()
	if err := s.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if stored := readSavedSettings(t, path); stored.SendText != "final draft" || s.SettingsDirty {
		t.Fatal("shutdown did not flush latest draft after background write")
	}
}

func TestShutdownReclaimsPendingOpenPortWithoutFrontendCallback(t *testing.T) {
	port := &serialport.Port{}
	s := &App{closed: make(chan struct{})}
	s.jobs.Add(1)
	go func() {
		defer s.jobs.Done()
		s.pendingPortMu.Lock()
		s.pendingPort = port
		s.pendingPortMu.Unlock()
	}()
	if err := s.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if s.pendingPort != nil {
		t.Fatal("shutdown retained pending opened port")
	}
	if _, err := port.Write([]byte{1}); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("pending port was not closed: %v", err)
	}
}

func TestCaptureWriteFailureStopsAndReports(t *testing.T) {
	writeErr := errors.New("disk full")
	file := &captureTestFile{writeErr: writeErr}
	p := &Session{ctx: context.Background(), events: make(chan Event, 4)}
	v := &appTestView{}
	s := &App{view: v, session: p, CapturePath: "rx.bin"}
	if err := p.setCapture(file); err != nil {
		t.Fatal(err)
	}
	p.enqueueCapture([]byte("pending"))
	var event Event
	select {
	case event = <-p.Events():
	case <-time.After(captureTestTimeout):
		t.Fatal("capture write failure was not reported")
	}
	if !errors.Is(event.Err, writeErr) || event.ReadFailed {
		t.Fatalf("capture failure event = %+v", event)
	}
	p.events <- event
	s.drain(p)
	if s.CapturePath != "" || !s.StatusError {
		t.Fatal("capture failure did not stop recording and report an error")
	}
	p.captureMu.Lock()
	active := p.capture != nil
	p.captureMu.Unlock()
	if active {
		t.Fatal("capture remained active after a write failure")
	}
	if err := p.SetCapture(nil); err != nil {
		t.Fatal(err)
	}
	_, closed := file.snapshot()
	if !closed {
		t.Fatal("failed capture file remained open")
	}
}

func TestShutdownFlushesBufferedCaptureAndReportsFailure(t *testing.T) {
	writeErr := errors.New("disk full")
	file := &captureTestFile{writeErr: writeErr}
	p := NewSession(&serialport.Port{}, 115200)
	s := &App{closed: make(chan struct{}), session: p, stopPump: make(chan struct{})}
	if err := p.setCapture(file); err != nil {
		t.Fatal(err)
	}
	p.enqueueCapture([]byte("pending"))
	if err := s.Shutdown(); !errors.Is(err, writeErr) {
		t.Fatalf("shutdown did not return capture flush failure: %v", err)
	}
	_, closed := file.snapshot()
	if !closed {
		t.Fatal("shutdown left the capture file open")
	}
}
func TestShutdownReclaimsCaptureWhenCompletionIsDiscarded(t *testing.T) {
	v := &appTestView{callbacks: make(chan func(), 8)}
	s := &App{closed: make(chan struct{}), view: v}
	p := NewSession(&serialport.Port{}, 115200)
	s.session, s.stopPump = p, make(chan struct{})
	path := filepath.Join(t.TempDir(), "rx.bin")
	s.CaptureTarget()(path)
	awaitSessionCapture(t, p)
	p.enqueueCapture([]byte("shutdown flush"))
	// Shutdown must join the open/SetCapture worker even if its posted
	// completion never reaches the frontend, and reclaim any transferred file.
	if err := s.Shutdown(); err != nil {
		t.Fatal(err)
	}
	p.captureMu.Lock()
	retained := p.capture
	p.captureMu.Unlock()
	if retained != nil {
		t.Fatal("shutdown retained capture file after discarded callback")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "shutdown flush" {
		t.Fatalf("shutdown did not flush buffered capture: %q %v", data, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func TestAsyncFileUseSavesAcceptedSnapshotNotLaterDraft(t *testing.T) {
	dir := t.TempDir()
	v := &appTestView{callbacks: make(chan func(), 8)}
	s := &App{SettingsPath: filepath.Join(dir, "config.toml"), Settings: config.Settings{SendText: "accepted", Presets: []config.Preset{{Data: "accepted preset"}}, BaudRates: []string{"115200"}}}
	s.Startup(v)
	s.SettingsChanged()
	s.SaveLog(filepath.Join(dir, "log.txt"), "display")
	s.Settings.SendText = "later draft"
	s.Settings.Presets[0].Data = "later preset"
	s.Settings.BaudRates[0] = "9600"
	s.SettingsChanged()
	awaitAppCallback(t, v)
	awaitAppCallback(t, v)
	saved := readSavedSettings(t, s.SettingsPath)
	if saved.SendText != "accepted" || saved.Presets[0].Data != "accepted preset" || !slices.Equal(saved.BaudRates, []string{"115200"}) || !s.SettingsDirty {
		t.Fatal("background completion saved newer drafts or lost immutable settings snapshot")
	}
	if err := s.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if saved := readSavedSettings(t, s.SettingsPath); saved.SendText != "later draft" {
		t.Fatal("shutdown did not flush later draft")
	}
}

func TestAppSendFormatUsesHexCRLFAndConfiguredEncoding(t *testing.T) {
	s := &App{Settings: config.Settings{Encoding: "GB18030", SendHex: true, CRLF: true}}
	data, err := Encode("01 03 FF", s.format(s.Settings.SendHex, s.Settings.CRLF))
	if err != nil || string(data) != string([]byte{1, 3, 255, '\r', '\n'}) {
		t.Fatalf("HEX+CRLF format = % x, %v", data, err)
	}
	data, err = Encode("中", s.format(false, true))
	if err != nil || string(data) != string([]byte{0xD6, 0xD0, '\r', '\n'}) {
		t.Fatalf("configured text encoding = % x, %v", data, err)
	}
}

func TestPendingPortOperationRejectsSettingsMutation(t *testing.T) {
	s := &App{Settings: config.Settings{Port: serialport.Settings{Name: "COM1", Baud: "9600"}, BaudRates: []string{"9600", "115200"}}}
	s.SetPorts([]serialport.Info{{Name: "COM1"}, {Name: "COM2"}})
	s.PortBusy = true
	before := s.Settings
	s.PortChoice = "COM2"
	s.SelectPort()
	s.SelectBaud("115200")
	draft, rates := s.PortSettingsDraft()
	draft.Baud = "115200"
	if s.ApplyPortSettings(draft, rates) == nil || s.CustomBaud("38400", nil, nil) == nil || s.DeleteBaud("9600", nil, nil) == nil {
		t.Fatal("pending serial operation accepted a settings mutation")
	}
	if !reflect.DeepEqual(s.Settings, before) || s.SettingsDirty {
		t.Fatal("pending operation settings snapshot diverged")
	}
}

func TestShutdownWaitsForLogSaveWithDiscardedCompletion(t *testing.T) {
	v := &appTestView{callbacks: make(chan func(), 8)}
	s := &App{view: v, closed: make(chan struct{})}
	path := filepath.Join(t.TempDir(), "display.txt")
	s.SaveLog(path, "immutable display snapshot")
	if err := s.Shutdown(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "immutable display snapshot" {
		t.Fatalf("shutdown abandoned pending file write: %q %v", data, err)
	}
}
