package internal

import (
	"bytes"
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

func TestApplyControlLinesRestoresPreviousSettingsAfterPartialFailure(t *testing.T) {
	previous := serialport.Settings{DTR: false, RTS: false}
	next := serialport.Settings{DTR: true, RTS: true}
	actual := previous
	applyErr := errors.New("RTS control line failed")
	calls := 0
	setLines := func(dtr, rts bool) error {
		calls++
		if calls == 1 {
			actual.DTR = dtr
			return applyErr
		}
		actual.DTR, actual.RTS = dtr, rts
		return nil
	}

	err := applyControlLines(setLines, previous, next)
	if !errors.Is(err, applyErr) {
		t.Fatalf("applyControlLines() error = %v, want original error %v", err, applyErr)
	}
	if calls != 2 {
		t.Fatalf("SetLines() calls = %d, want initial apply and rollback", calls)
	}
	if actual.DTR != previous.DTR || actual.RTS != previous.RTS {
		t.Fatalf("restored lines = DTR %t, RTS %t; want DTR %t, RTS %t", actual.DTR, actual.RTS, previous.DTR, previous.RTS)
	}
	if !strings.Contains(i18n.ErrorText(err, true), "previous configured state was reapplied") {
		t.Fatalf("error does not report successful rollback: %v", err)
	}
}

func TestApplyControlLinesReportsFailedRestore(t *testing.T) {
	previous := serialport.Settings{DTR: false, RTS: false}
	next := serialport.Settings{DTR: true, RTS: true}
	actual := previous
	applyErr := errors.New("RTS control line failed")
	restoreErr := errors.New("DTR restore failed")
	calls := 0
	setLines := func(dtr, rts bool) error {
		calls++
		if calls == 1 {
			actual.DTR = dtr
			return applyErr
		}
		return restoreErr
	}

	err := applyControlLines(setLines, previous, next)
	if !errors.Is(err, applyErr) || !errors.Is(err, restoreErr) {
		t.Fatalf("applyControlLines() error = %v, want both apply and restore errors", err)
	}
	if calls != 2 {
		t.Fatalf("SetLines() calls = %d, want initial apply and rollback", calls)
	}
	if actual.DTR == previous.DTR {
		t.Fatal("test setup did not leave DTR in its partially applied state")
	}
	if !strings.Contains(i18n.ErrorText(err, true), "hardware state may not match configuration") {
		t.Fatalf("error does not report uncertain hardware state: %v", err)
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
	p.emit(Event{Data: []byte("pending RX")})
	p.emit(Event{Data: []byte("pending TX"), Sent: true})
	s.ClearReceiveLog()
	s.drain(p)
	if s.Tx != 0 || s.Rx != 0 || len(v.records) != 1 {
		t.Fatal("pending records or counters reappeared after clearing the log")
	}
	p.emit(Event{Data: []byte("new RX")})
	p.emit(Event{Data: []byte("new TX"), Sent: true})
	s.drain(p)
	if s.Tx != 6 || s.Rx != 6 || len(v.records) != 3 || string(v.records[1].Data) != "new RX" || string(v.records[2].Data) != "new TX" {
		t.Fatal("post-clear records or counters were lost")
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

func TestSaveAndCaptureDoNotOverwriteActiveSendSourceAlias(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "send.bin")
	aliasPath := filepath.Join(dir, "send-alias.bin")
	want := []byte("serial payload split across chunks")
	if err := os.WriteFile(sourcePath, want, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(sourcePath, aliasPath); err != nil {
		t.Fatalf("create hard-link alias: %v", err)
	}

	view := &appTestView{callbacks: make(chan func(), 8)}
	connection := &Session{ctx: context.Background()}
	s := &App{view: view, session: connection, FilePath: sourcePath}
	started := make(chan struct{})
	release := make(chan struct{})
	released := false
	sent := make(chan []byte, 1)
	defer func() {
		if !released {
			close(release)
		}
		s.jobs.Wait()
		if err := connection.SetCapture(nil); err != nil {
			t.Error(err)
		}
	}()

	s.startSending(Text{ZH: "发送文件", EN: "File send"}, sourcePath, func(_ context.Context, _ *Session) error {
		file, err := os.Open(sourcePath)
		if err != nil {
			return err
		}
		defer file.Close()
		first := make([]byte, 1)
		n, err := file.Read(first)
		if err != nil {
			return err
		}
		close(started)
		<-release
		rest, err := io.ReadAll(file)
		if err != nil {
			return err
		}
		sent <- append(first[:n], rest...)
		return nil
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("file-send worker did not open its source")
	}

	s.SelectFile(filepath.Join(dir, "another-file.bin"))
	s.SaveLog(aliasPath, "overwrite")
	awaitAppCallback(t, view)
	if s.Status.EN != "Do not overwrite the file currently being sent" {
		t.Fatalf("save collision status = %q", s.Status.EN)
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil || !slices.Equal(data, want) {
		t.Fatalf("display save changed send source: %q %v", data, err)
	}

	s.CaptureTarget()(aliasPath)
	awaitAppCallback(t, view)
	if s.CapturePath != "" || s.FileBusy || s.Status.EN != "Do not overwrite the file currently being sent" {
		t.Fatalf("capture collision state: path=%q busy=%t status=%q", s.CapturePath, s.FileBusy, s.Status.EN)
	}
	data, err = os.ReadFile(sourcePath)
	if err != nil || !slices.Equal(data, want) {
		t.Fatalf("capture changed send source: %q %v", data, err)
	}

	close(release)
	released = true
	select {
	case got := <-sent:
		if !slices.Equal(got, want) {
			t.Fatalf("file-send data = %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("file-send worker did not finish after release")
	}
	awaitAppCallback(t, view)
	if s.sendingFilePath != "" {
		t.Fatalf("completed file send retained source path %q", s.sendingFilePath)
	}
}

func TestSendFileCannotStartWhileFileOutputIsBusy(t *testing.T) {
	s := &App{
		Settings: config.Settings{FileDelay: "1"},
		session:  &Session{},
		FilePath: "send.bin",
		FileBusy: true,
	}
	if s.CanSendFile() {
		t.Fatal("file send was enabled during a file output operation")
	}
	s.SendFile()
	if s.cancelSend != nil || s.sendingFilePath != "" || s.StatusError {
		t.Fatal("file send started while a file output operation was busy")
	}
}

type appFileSendPort struct{}

func (appFileSendPort) Read([]byte) (int, error) { return 0, io.EOF }

func (appFileSendPort) Write(data []byte) (int, error) { return len(data), nil }

func (appFileSendPort) SetLines(bool, bool) error { return nil }

func (appFileSendPort) Close() error { return nil }

func TestFileSendProgressTerminalStates(t *testing.T) {
	tests := []struct {
		name            string
		err             error
		visible, failed bool
		status          Text
	}{
		{name: "complete", visible: false, status: Text{ZH: "发送文件完成", EN: "File send completed"}},
		{name: "cancelled", err: context.Canceled, visible: true, status: Text{ZH: "已取消", EN: "Cancelled"}},
		{name: "failed", err: errors.New("write failed"), visible: true, failed: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			view := &appTestView{callbacks: make(chan func(), 1)}
			p := &Session{ctx: context.Background()}
			s := &App{view: view, session: p}
			s.startSending(Text{ZH: "发送文件", EN: "File send"}, filepath.Join("firmware", "firmware.bin"), func(_ context.Context, session *Session) error {
				session.emit(Event{Data: []byte("abc"), Sent: true})
				return tc.err
			})
			awaitAppCallback(t, view)
			if s.FileProgress.Active || s.FileProgress.Visible != tc.visible || s.FileProgress.Error != tc.failed {
				t.Fatalf("file progress terminal state = %+v", s.FileProgress)
			}
			if s.FileProgress.Name != "firmware.bin" || s.FileProgress.SentBytes != 3 {
				t.Fatalf("file progress identity/count = %+v", s.FileProgress)
			}
			if tc.status != (Text{}) && s.FileProgress.Status != tc.status {
				t.Fatalf("file progress status = %+v, want %+v", s.FileProgress.Status, tc.status)
			}
			if tc.failed && !strings.Contains(s.FileProgress.Status.EN, "write failed") {
				t.Fatalf("file failure reason was not retained: %+v", s.FileProgress.Status)
			}
		})
	}
}

func TestAppFileProgressUsesIndependentSuccessfulByteCount(t *testing.T) {
	p := &Session{}
	p.beginFileSendProgress()
	s := &App{session: p, FileProgress: FileSendProgress{Active: true}, fileProgressUpdatedAt: time.Now()}
	p.emit(Event{Data: []byte("abc"), Sent: true})
	s.deliverDisplay(p)
	if s.FileProgress.SentBytes != 0 || s.Tx != 3 {
		t.Fatalf("progress updated before interval or TX count wrong: progress=%d TX=%d", s.FileProgress.SentBytes, s.Tx)
	}
	s.ClearReceiveLog()
	p.emit(Event{Data: []byte("de"), Sent: true})
	s.fileProgressUpdatedAt = time.Now().Add(-fileSendProgressInterval)
	s.deliverDisplay(p)
	if s.Tx != 2 || s.FileProgress.SentBytes != 5 {
		t.Fatalf("clearing S changed file progress: progress=%d TX=%d", s.FileProgress.SentBytes, s.Tx)
	}
	p.endFileSendProgress()
}

func TestFileProgressUpdatesDuringLongChunkDelay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "firmware.bin")
	err := os.WriteFile(path, bytes.Repeat([]byte{0x5A}, 768), 0600)
	if err != nil {
		t.Fatal(err)
	}
	view := &appTestView{callbacks: make(chan func(), 16)}
	p := &Session{port: appFileSendPort{}, ctx: context.Background(), events: make(chan Event, 1), writeChunk: 256}
	stop := make(chan struct{})
	s := &App{Settings: config.Settings{FileDelay: "1000"}, view: view, session: p, FilePath: path, closed: make(chan struct{})}
	s.SendFile()
	go s.pump(p, stop)
	defer func() {
		s.StopSending()
		s.jobs.Wait()
		close(stop)
	}()
	deadline := time.NewTimer(900 * time.Millisecond)
	defer deadline.Stop()
	for s.FileProgress.SentBytes != 256 {
		select {
		case fn := <-view.callbacks:
			fn()
		case <-deadline.C:
			t.Fatalf("first chunk was not reflected during the delay: %+v TX=%d", s.FileProgress, s.Tx)
		}
	}
	if s.FileProgress.TotalBytes != 768 || s.Tx != 256 {
		t.Fatalf("first chunk progress = %+v TX=%d", s.FileProgress, s.Tx)
	}
}

func TestFileProgressDisconnectUsesDisconnectReason(t *testing.T) {
	for _, tc := range []struct {
		name    string
		readErr error
		failed  bool
		want    string
	}{
		{name: "manual", want: "Cancelled"},
		{name: "read failure", readErr: errors.New("device vanished"), failed: true, want: "device vanished"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			readDone := make(chan struct{})
			close(readDone)
			p := &Session{port: appFileSendPort{}, ctx: ctx, cancel: cancel, readDone: readDone, events: make(chan Event, 1)}
			p.beginFileSendProgress()
			view := &appTestView{callbacks: make(chan func(), 1)}
			s := &App{view: view, session: p, stopPump: make(chan struct{}), FileProgress: FileSendProgress{Active: true}, StatusError: true, Status: Text{EN: "unrelated export failure"}}
			if tc.readErr != nil {
				p.emit(Event{Err: tc.readErr, ReadFailed: true})
				s.drain(p)
			} else {
				s.TogglePort()
			}
			s.jobs.Wait()
			awaitAppCallback(t, view)
			if s.FileProgress.Active || !s.FileProgress.Visible || s.FileProgress.Error != tc.failed || !strings.Contains(s.FileProgress.Status.EN, tc.want) {
				t.Fatalf("disconnect progress = %+v", s.FileProgress)
			}
			if strings.Contains(s.FileProgress.Status.EN, "export failure") {
				t.Fatalf("disconnect used an unrelated error: %+v", s.FileProgress)
			}
		})
	}
}

func TestDisconnectBeforeFileSendCompletionCallback(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		visible bool
		failed  bool
		status  string
	}{
		{name: "complete", status: "File send completed"},
		{name: "cancelled", err: context.Canceled, visible: true, status: "Cancelled"},
		{name: "failed", err: errors.New("write failed"), visible: true, failed: true, status: "write failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			readDone := make(chan struct{})
			close(readDone)
			p := &Session{port: appFileSendPort{}, ctx: ctx, cancel: cancel, readDone: readDone, events: make(chan Event, 1)}
			view := &appTestView{callbacks: make(chan func(), 2)}
			s := &App{view: view, session: p, stopPump: make(chan struct{})}
			s.startSending(Text{ZH: "发送文件", EN: "File send"}, "firmware.bin", func(_ context.Context, p *Session) error {
				p.emit(Event{Data: []byte("abc"), Sent: true})
				return tc.err
			})
			s.jobs.Wait()
			s.TogglePort()
			s.jobs.Wait()
			awaitAppCallback(t, view)
			awaitAppCallback(t, view)
			progress := s.FileProgress
			if progress.Active || progress.Visible != tc.visible || progress.Error != tc.failed || progress.SentBytes != 3 || !strings.Contains(progress.Status.EN, tc.status) {
				t.Fatalf("disconnect progress = %+v", progress)
			}
		})
	}
}

func TestSuccessfulFileSendDrainDisconnectKeepsResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	readDone := make(chan struct{})
	close(readDone)
	p := &Session{port: appFileSendPort{}, ctx: ctx, cancel: cancel, readDone: readDone, events: make(chan Event, 1)}
	view := &appTestView{callbacks: make(chan func(), 2)}
	s := &App{view: view, session: p, stopPump: make(chan struct{})}
	s.startSending(Text{ZH: "发送文件", EN: "File send"}, "firmware.bin", func(_ context.Context, p *Session) error {
		p.emit(Event{Data: []byte("abc"), Sent: true})
		return nil
	})
	s.jobs.Wait()
	p.emit(Event{Err: errors.New("device vanished"), ReadFailed: true})
	awaitAppCallback(t, view)
	s.jobs.Wait()
	awaitAppCallback(t, view)
	if s.FileProgress.Active || s.FileProgress.Visible || s.FileProgress.Error || s.FileProgress.SentBytes != 3 || s.FileProgress.Status.EN != "File send completed" {
		t.Fatalf("successful file send was lost after read error: %+v", s.FileProgress)
	}
	if !s.StatusError || !strings.Contains(s.Status.EN, "device vanished") {
		t.Fatalf("serial read error was not reported: %+v", s.Status)
	}
}

func TestAppFileSendReportsTotalAndSuccessfulBytes(t *testing.T) {
	data := bytes.Repeat([]byte{0x5A}, 700)
	path := filepath.Join(t.TempDir(), "firmware.bin")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	view := &appTestView{callbacks: make(chan func(), 4)}
	p := &Session{port: appFileSendPort{}, ctx: context.Background(), writeChunk: 100}
	s := &App{Settings: config.Settings{FileDelay: "0"}, view: view, session: p, FilePath: path}
	s.SendFile()
	awaitAppCallback(t, view)
	awaitAppCallback(t, view)
	s.jobs.Wait()
	if s.FileProgress.Name != "firmware.bin" || s.FileProgress.TotalBytes != uint64(len(data)) || s.FileProgress.SentBytes != uint64(len(data)) {
		t.Fatalf("completed file progress = %+v, want %d bytes", s.FileProgress, len(data))
	}
	if s.FileProgress.Visible || s.FileProgress.Active || s.Tx != uint64(len(data)) {
		t.Fatalf("completed file progress visibility/counters = %+v TX=%d", s.FileProgress, s.Tx)
	}
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
	s.StatusError = false
	s.Settings.ParseSendEscapes = true
	s.Settings.CRLF = false
	s.Repeat = false
	s.SendText(`bad\q`)
	if !s.StatusError || s.Sending() {
		t.Fatal("invalid text escape started a send")
	}
	s.StatusError = false
	s.Settings.Presets = []config.Preset{{Enabled: true, Data: `bad\x`}}
	s.SendPresets(-1)
	if !s.StatusError || s.Sending() {
		t.Fatal("invalid preset escape started a send")
	}
	s.StatusError = false
	s.Settings.ChecksumAlgorithm = "XOR8"
	s.Settings.ChecksumHeaderBytes, s.Settings.ChecksumTailBytes = "1", "1"
	s.SendText("A")
	if !s.StatusError || s.Sending() {
		t.Fatal("invalid checksum frame bounds started a send")
	}
	s.StatusError = false
	s.Settings.Presets = []config.Preset{{Enabled: true, Data: "A"}}
	s.SendPresets(-1)
	if !s.StatusError || s.Sending() {
		t.Fatal("invalid preset checksum frame bounds started a send")
	}
	s.StatusError = false
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
	data, err := s.encodeSendData("01 03 FF", true, true)
	if err != nil || string(data) != string([]byte{1, 3, 255, '\r', '\n'}) {
		t.Fatalf("HEX+CRLF format = % x, %v", data, err)
	}
	data, err = s.encodeSendData("中", false, true)
	if err != nil || string(data) != string([]byte{0xD6, 0xD0, '\r', '\n'}) {
		t.Fatalf("configured text encoding = % x, %v", data, err)
	}
	s.Settings.ParseSendEscapes = true
	data, err = s.encodeSendData(`中\x00`, false, false)
	if err != nil || string(data) != string([]byte{0xD6, 0xD0, 0}) {
		t.Fatalf("escaped text with configured encoding = % x, %v", data, err)
	}
	data, err = s.encodeSendData("5C7830", true, false)
	if err != nil || string(data) != string([]byte{'\\', 'x', '0'}) {
		t.Fatalf("HEX mode parsed text escapes = % x, %v", data, err)
	}
}

func TestAppSendChecksumExcludesFrameEdgesBeforeCRLF(t *testing.T) {
	s := &App{Settings: config.Settings{
		Encoding:            "UTF-8",
		ParseSendEscapes:    true,
		ChecksumAlgorithm:   "XOR8",
		ChecksumHeaderBytes: "1",
		ChecksumTailBytes:   "1",
	}}
	data, err := s.encodeSendData(`A\x42Z`, false, true)
	want := []byte{'A', 'B', 0x42, 'Z', '\r', '\n'}
	if err != nil || string(data) != string(want) {
		t.Fatalf("escaped send with checksum = % X, %v; want % X", data, err, want)
	}
}

func TestAppSendChecksumBeforeCRLF(t *testing.T) {
	s := &App{Settings: config.Settings{
		Encoding:          "UTF-8",
		ChecksumAlgorithm: "XOR8",
	}}
	data, err := s.encodeSendData("AT", false, true)
	want := []byte{'A', 'T', 0x15, '\r', '\n'}
	if err != nil || string(data) != string(want) {
		t.Fatalf("send with checksum and CRLF = % X, %v; want % X", data, err, want)
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

func TestDisconnectDeliversFinalReadWriteAndSendingJobRecords(t *testing.T) {
	port := newBarrierSessionPort()
	p := newSession(port, 115200, nil)
	v := &appTestView{callbacks: make(chan func(), 8)}
	s := &App{view: v, session: p, stopPump: make(chan struct{}), closed: make(chan struct{})}
	sendReturned := make(chan struct{})
	releaseTerminal := make(chan struct{})
	defer func() {
		releaseSessionBarrier(port.releaseRead)
		releaseSessionBarrier(port.releaseWrite)
		releaseSessionBarrier(releaseTerminal)
		_ = s.Shutdown()
	}()
	awaitSessionBarrier(t, port.readStarted)
	s.startSending(Text{ZH: "发送", EN: "Send"}, "", func(ctx context.Context, session *Session) error {
		err := session.Send(ctx, []byte("final TX"))
		close(sendReturned)
		<-releaseTerminal
		// A sending job can still produce data after its final Send returned.
		session.emit(Event{Data: []byte("terminal TX"), Sent: true})
		return err
	})
	awaitSessionBarrier(t, port.writeStarted)
	s.TogglePort()
	awaitSessionBarrier(t, port.closeStarted)
	if s.Connected() || !s.PortBusy || len(v.records) != 0 {
		t.Fatal("disconnect did not detach the old session before its final producers")
	}
	releaseSessionBarrier(port.releaseRead)
	releaseSessionBarrier(port.releaseWrite)
	awaitSessionBarrier(t, sendReturned)
	select {
	case <-v.callbacks:
		t.Fatal("disconnect posted completion before the sending job reached terminal state")
	default:
	}
	releaseSessionBarrier(releaseTerminal)
	s.jobs.Wait()
	for len(v.callbacks) > 0 {
		awaitAppCallback(t, v)
	}
	if s.Rx != uint64(len("final RX")) || s.Tx != uint64(len("final TX")+len("terminal TX")) || len(v.records) != 3 {
		t.Fatalf("final records were lost: rx=%d tx=%d records=%d", s.Rx, s.Tx, len(v.records))
	}
	if s.PortBusy || s.Sending() || s.Connected() || s.StatusError || p.hasDisplay() {
		t.Fatal("terminal delivery retained busy state, redrained errors, or left queued data")
	}
	if err := s.Shutdown(); err != nil {
		t.Fatal(err)
	}
}

type blockingCaptureCloseFile struct {
	*os.File
	closeStarted chan struct{}
	releaseClose chan struct{}
}

func (f *blockingCaptureCloseFile) Close() error {
	close(f.closeStarted)
	<-f.releaseClose
	return f.File.Close()
}

func TestDisconnectProtectsCaptureAndHardLinkUntilFileCloseCompletes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.bin")
	alias := filepath.Join(filepath.Dir(path), "capture-link.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(file, "original"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := os.Link(path, alias); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	blocked := &blockingCaptureCloseFile{File: file, closeStarted: make(chan struct{}), releaseClose: make(chan struct{})}
	p := NewSession(&serialport.Port{}, 115200)
	v := &appTestView{callbacks: make(chan func(), 8)}
	s := &App{view: v, session: p, stopPump: make(chan struct{}), closed: make(chan struct{}), CapturePath: path}
	defer func() {
		releaseSessionBarrier(blocked.releaseClose)
		_ = s.Shutdown()
	}()
	if err := p.setCapture(blocked); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	p.enqueueCapture([]byte("pending"))
	s.disconnect(nil)
	awaitSessionBarrier(t, blocked.closeStarted)
	if s.CapturePath != "" || s.FileBusy || !s.PortBusy {
		t.Fatal("capture close did not expose the expected detached UI state")
	}
	for _, target := range []string{path, alias} {
		s.SaveLog(target, "must not overwrite")
		if !s.FileBusy {
			t.Fatal("export was disabled instead of checking its destination")
		}
		awaitAppCallback(t, v)
		if s.FileBusy || s.Status.EN != "Do not overwrite the active receive file" {
			t.Fatalf("closing capture was not protected: %q", s.Status.EN)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "originalpending" {
			t.Fatalf("export truncated the closing capture: %q %v", data, err)
		}
	}
	releaseSessionBarrier(blocked.releaseClose)
	awaitAppCallback(t, v)
	if s.PortBusy {
		t.Fatal("completed disconnect kept the port busy")
	}
	// Protection ends with the actual close, even if its UI status was superseded.
	s.SaveLog(alias, "after close")
	awaitAppCallback(t, v)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "after close" {
		t.Fatalf("completed close retained a stale export reservation: %q %v", data, err)
	}
}

func TestCaptureTerminalErrorsSurviveDiscardedFrontendCallbacks(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		for _, report := range []bool{false, true} {
			name := "stop"
			if disconnect {
				name = "disconnect"
			}
			if report {
				name += "-reported"
			} else {
				name += "-discarded"
			}
			t.Run(name, func(t *testing.T) {
				writeErr := errors.New("terminal flush failure")
				file := &captureTestFile{writeErr: writeErr, writeStarted: make(chan struct{}, 1), blockWrite: make(chan struct{})}
				p := NewSession(&serialport.Port{}, 115200)
				v := &appTestView{callbacks: make(chan func(), 8)}
				s := &App{view: v, session: p, stopPump: make(chan struct{}), closed: make(chan struct{}), CapturePath: "capture.bin"}
				defer func() {
					releaseSessionBarrier(file.blockWrite)
					_ = s.Shutdown()
				}()
				if err := p.setCapture(file); err != nil {
					t.Fatal(err)
				}
				p.captureMu.Lock()
				capture := p.captureWorker
				p.captureMu.Unlock()
				p.enqueueCapture([]byte("buffered"))
				if disconnect {
					s.disconnect(nil)
				} else {
					s.StopCapture()
				}
				awaitSessionBarrier(t, capture.stop)
				awaitSessionBarrier(t, file.writeStarted)
				releaseSessionBarrier(file.blockWrite)
				if report {
					awaitAppCallback(t, v)
					if !s.StatusError || !strings.Contains(s.Status.EN, writeErr.Error()) {
						t.Fatalf("terminal failure was not reported: %q", s.Status.EN)
					}
				}
				err := s.Shutdown()
				if report && err != nil {
					t.Fatalf("Shutdown repeated an acknowledged UI error: %v", err)
				}
				if !report && !errors.Is(err, writeErr) {
					t.Fatalf("Shutdown lost the discarded terminal error: %v", err)
				}
				_, closed := file.snapshot()
				if !closed {
					t.Fatal("Shutdown left the terminal capture file open")
				}
			})
		}
	}
}

func TestDisconnectCompletionDoesNotApplyOldErrorsToNewConnection(t *testing.T) {
	closeErr := errors.New("old close failure")
	port := newBarrierSessionPort()
	port.closeErr = closeErr
	p := newSession(port, 115200, nil)
	v := &appTestView{callbacks: make(chan func(), 8)}
	s := &App{view: v, session: p, stopPump: make(chan struct{}), closed: make(chan struct{})}
	defer func() {
		releaseSessionBarrier(port.releaseRead)
		releaseSessionBarrier(port.releaseWrite)
		_ = s.Shutdown()
	}()
	awaitSessionBarrier(t, port.readStarted)
	s.disconnect(nil)
	awaitSessionBarrier(t, port.closeStarted)
	releaseSessionBarrier(port.releaseRead)
	s.jobs.Wait()
	// Simulate a newer connection accepting ownership before an old callback.
	current := NewSession(&serialport.Port{}, 115200)
	s.session, s.stopPump = current, make(chan struct{})
	s.PortBusy = true
	s.CapturePath = "new capture.bin"
	s.Status = Text{EN: "new connection"}
	awaitAppCallback(t, v)
	if s.session != current || !s.PortBusy || s.CapturePath != "new capture.bin" || s.Status.EN != "new connection" || s.StatusError {
		t.Fatal("old disconnect completion mutated the new connection")
	}
	if s.Rx != uint64(len("final RX")) || len(v.records) != 1 {
		t.Fatal("isolating old session errors also discarded its final receive record")
	}
	if err := s.Shutdown(); !errors.Is(err, closeErr) {
		t.Fatalf("unreported stale-session close error was lost: %v", err)
	}
}

func TestStaleCaptureFailureKeepsReplacementDestinationProtected(t *testing.T) {
	p := &Session{ctx: context.Background(), events: make(chan Event, 4)}
	failed := &captureTestFile{writeErr: errors.New("previous capture failed")}
	if err := p.setCapture(failed); err != nil {
		t.Fatal(err)
	}
	p.enqueueCapture(make([]byte, captureBufferSize))
	var old Event
	select {
	case old = <-p.Events():
	case <-time.After(captureTestTimeout):
		t.Fatal("previous capture did not report its failure")
	}
	if err := p.SetCapture(nil); err != nil {
		t.Fatal(err)
	}
	v := &appTestView{callbacks: make(chan func(), 8)}
	s := &App{view: v, session: p}
	path := filepath.Join(t.TempDir(), "replacement.bin")
	defer func() {
		s.jobs.Wait()
		_ = p.SetCapture(nil)
	}()
	s.CaptureTarget()(path)
	awaitAppCallback(t, v)
	p.events <- old
	s.drain(p)
	if s.CapturePath != path || !slices.Contains(s.capturePaths(), path) {
		t.Fatal("an old failure cleared the replacement capture reservation")
	}
	s.SaveLog(path, "must not overwrite")
	awaitAppCallback(t, v)
	data, err := os.ReadFile(path)
	if err != nil || len(data) != 0 || s.Status.EN != "Do not overwrite the active receive file" {
		t.Fatalf("replacement capture was not protected: %q %v", data, err)
	}
}

func TestCaptureAcknowledgementKeepsOtherUnreportedFailures(t *testing.T) {
	port := newBarrierSessionPort()
	p := newSession(port, 115200, nil)
	defer func() {
		releaseSessionBarrier(port.releaseRead)
		releaseSessionBarrier(port.releaseWrite)
		_ = p.Close()
	}()
	awaitSessionBarrier(t, port.readStarted)
	firstErr := errors.New("first capture failure")
	secondErr := errors.New("second capture failure")
	var failures []Event
	for _, writeErr := range []error{firstErr, secondErr} {
		if err := p.setCapture(&captureTestFile{writeErr: writeErr}); err != nil {
			t.Fatal(err)
		}
		p.enqueueCapture(make([]byte, captureBufferSize))
		select {
		case event := <-p.Events():
			if !errors.Is(event.Err, writeErr) {
				t.Fatalf("capture failure = %v, want %v", event.Err, writeErr)
			}
			failures = append(failures, event)
		case <-time.After(captureTestTimeout):
			t.Fatal("capture worker did not report its failure")
		}
	}
	p.acknowledgeCaptureError(failures[0].capture)
	releaseSessionBarrier(port.releaseRead)
	err := p.Close()
	if errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Fatalf("Close did not retain exactly the unacknowledged capture error: %v", err)
	}
}

func TestClearReceiveLogDiscardsDetachedSessionDataBeforeFinalDelivery(t *testing.T) {
	for _, settled := range []bool{false, true} {
		name := "close-blocked"
		if settled {
			name = "completion-queued"
		}
		t.Run(name, func(t *testing.T) {
			port := newBarrierSessionPort()
			p := newSession(port, 115200, nil)
			v := &appTestView{callbacks: make(chan func(), 8)}
			s := &App{view: v, session: p, stopPump: make(chan struct{}), closed: make(chan struct{}), Rx: 123, Tx: 456}
			defer func() {
				releaseSessionBarrier(port.releaseRead)
				releaseSessionBarrier(port.releaseWrite)
				_ = s.Shutdown()
			}()
			awaitSessionBarrier(t, port.readStarted)
			p.emit(Event{Data: []byte("queued RX")})
			p.emit(Event{Data: []byte("queued TX"), Sent: true})
			s.disconnect(nil)
			awaitSessionBarrier(t, port.closeStarted)
			// Include data queued after detach but before the user's clear.
			p.emit(Event{Data: []byte("detached TX"), Sent: true})
			if settled {
				releaseSessionBarrier(port.releaseRead)
				s.jobs.Wait()
			}
			if s.Connected() || !s.PortBusy {
				t.Fatal("test did not clear during the detached session's lifetime")
			}
			s.ClearReceiveLog()
			if s.Rx != 0 || s.Tx != 0 || p.hasDisplay() {
				t.Fatal("clear retained detached session records or byte counts")
			}
			var wantRx, wantTx uint64
			wantRecords := 0
			if !settled {
				// Just like an active connection, new data after clear survives.
				p.emit(Event{Data: []byte("after-clear TX"), Sent: true})
				releaseSessionBarrier(port.releaseRead)
				s.jobs.Wait()
				wantRx = uint64(len("final RX"))
				wantTx = uint64(len("after-clear TX"))
				wantRecords = 2
			}
			awaitAppCallback(t, v)
			if s.Rx != wantRx || s.Tx != wantTx || len(v.records) != wantRecords {
				t.Fatalf("final delivery resurrected cleared data: rx=%d tx=%d records=%d", s.Rx, s.Tx, len(v.records))
			}
			if s.PortBusy || len(s.closingSessions) != 0 || p.hasDisplay() {
				t.Fatal("final delivery retained the detached session")
			}
		})
	}
}
