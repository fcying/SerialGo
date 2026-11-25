package internal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fcying/SerialGo/communication/serialport"
	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal/i18n"
)

type liveSettingsTestPort struct {
	readFailure chan error
	closed      chan struct{}
	closeOnce   sync.Once
	beforeClose func()
	parameters  func(next, previous serialport.Settings) error
	lines       func(bool, bool) error
	writes      []int
}

func (p *liveSettingsTestPort) Read([]byte) (int, error) {
	select {
	case err := <-p.readFailure:
		return 0, err
	case <-p.closed:
		return 0, os.ErrClosed
	}
}

func (p *liveSettingsTestPort) Write(data []byte) (int, error) {
	p.writes = append(p.writes, len(data))
	return len(data), nil
}

func (p *liveSettingsTestPort) SetParameters(next, previous serialport.Settings) error {
	if p.parameters != nil {
		return p.parameters(next, previous)
	}
	return nil
}

func (p *liveSettingsTestPort) SetLines(dtr, rts bool) error {
	if p.lines != nil {
		return p.lines(dtr, rts)
	}
	return nil
}

func (p *liveSettingsTestPort) Close() error {
	p.closeOnce.Do(func() {
		if p.beforeClose != nil {
			p.beforeClose()
		}
		close(p.closed)
	})
	return nil
}

func liveSettingsFixture(t *testing.T) (*App, *appTestView, *liveSettingsTestPort) {
	t.Helper()
	backend := &liveSettingsTestPort{closed: make(chan struct{}), readFailure: make(chan error, 1)}
	p := newSession(backend, 115200, nil)
	v := &appTestView{callbacks: make(chan func(), 8)}
	s := &App{Settings: config.Settings{
		Language: "en", BaudRates: []string{"9600", "115200", "250000"},
		Port: serialport.Settings{Name: "COM17", Baud: "115200", DataBits: "8", Parity: "None", StopBits: "1"},
	}, session: p, view: v, stopPump: make(chan struct{})}
	t.Cleanup(func() {
		_ = p.Close()
		s.jobs.Wait()
	})
	return s, v, backend
}

func TestConnectedPortSettingsCommitAfterHardwareAndRetainPortName(t *testing.T) {
	s, v, port := liveSettingsFixture(t)
	previous := s.Settings.Port
	draft, rates := s.PortSettingsDraft()
	draft.Name, draft.Baud, draft.DataBits, draft.Parity, draft.StopBits = "friendly label", "9600", "7", "Odd", "2"
	draft.DTR, draft.RTS = true, true
	want := draft
	want.Name = previous.Name
	rates = append(rates, "500000")
	wantRates := slices.Clone(rates)
	entered, release := make(chan struct{}), make(chan struct{})
	defer releaseSessionBarrier(release)
	port.parameters = func(next, prior serialport.Settings) error {
		close(entered)
		<-release
		if next != want || prior != previous {
			return fmt.Errorf("SetParameters(%+v, %+v), want (%+v, %+v)", next, prior, want, previous)
		}
		return nil
	}
	var gotLines [2]bool
	port.lines = func(dtr, rts bool) error { gotLines = [2]bool{dtr, rts}; return nil }
	var called bool
	var result error
	submitted := make(chan struct{})
	go func() {
		s.ApplyPortSettingsAsync(draft, rates, func(err error) { called, result = true, err })
		close(submitted)
	}()
	awaitSessionBarrier(t, submitted) // must return without waiting for the backend
	awaitSessionBarrier(t, entered)
	if !s.PortBusy || called || s.Settings.Port != previous || s.SettingsDirty || s.SettingsRevision != 0 {
		t.Fatal("a live request committed or completed before the driver accepted it")
	}
	var rejected error
	s.ApplyPortSettingsAsync(draft, rates, func(err error) { rejected = err })
	if !errors.Is(rejected, errPortSettingsBusy) {
		t.Fatalf("overlapping apply error = %v; want busy", rejected)
	}
	s.TogglePort()
	if !s.Connected() || !s.PortBusy {
		t.Fatal("a pending apply did not gate the close command")
	}
	rates[0] = "1" // the worker must own an immutable snapshot
	close(release)
	awaitAppCallback(t, v)
	if !called || result != nil || s.PortBusy || s.Settings.Port != want || gotLines != [2]bool{true, true} {
		t.Fatalf("live settings result = %v, busy = %v, settings = %+v, lines = %v", result, s.PortBusy, s.Settings.Port, gotLines)
	}
	if !s.SettingsDirty || s.SettingsRevision != 1 || !slices.Equal(s.Settings.BaudRates, wantRates) {
		t.Fatal("accepted settings and baud choices were not committed as one revision")
	}
	if err := s.session.Send(context.Background(), make([]byte, 200)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(port.writes, []int{96, 96, 8}) {
		t.Fatalf("write lengths = %v; want chunks at the new 9600 baud", port.writes)
	}
}

func TestConnectedPortSettingsCanChangeOnlyStopBits(t *testing.T) {
	s, v, port := liveSettingsFixture(t)
	previous := s.Settings.Port
	draft, rates := s.PortSettingsDraft()
	draft.StopBits = "2"
	var calls int
	port.parameters = func(next, prior serialport.Settings) error {
		calls++
		if next != draft || prior != previous {
			return fmt.Errorf("SetParameters(%+v, %+v), want (%+v, %+v)", next, prior, draft, previous)
		}
		return nil
	}
	port.lines = func(dtr, rts bool) error { return errors.New("unexpected modem line change") }
	var result error
	s.ApplyPortSettingsAsync(draft, rates, func(err error) { result = err })
	awaitAppCallback(t, v)
	if result != nil || calls != 1 || s.Settings.Port != draft || s.SettingsRevision != 1 {
		t.Fatalf("stop-bit-only change: %v, %d calls, settings = %+v, revision = %d", result, calls, s.Settings.Port, s.SettingsRevision)
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

func TestConnectedPortSettingsFailureRollsBackAndPreservesAppState(t *testing.T) {
	applyErr := errors.New("driver rejected proposed settings")
	lineRestoreErr := errors.New("DTR restore failed")
	framingRestoreErr := errors.New("framing restore failed")
	for _, tc := range []struct {
		name                        string
		failParams, failBothRestore bool
		dirty                       bool
	}{
		{name: "initial parameter failure"},
		{name: "line failure with restoration", dirty: true},
		{name: "line and framing restoration failures", failBothRestore: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, v, port := liveSettingsFixture(t)
			s.SettingsDirty = tc.dirty
			s.Status = Text{ZH: "原状态", EN: "Original status"}
			before, status, revision := s.Settings, s.Status, s.SettingsRevision
			draft, rates := s.PortSettingsDraft()
			draft.Baud, draft.DataBits, draft.Parity, draft.DTR, draft.RTS = "9600", "7", "Even", true, true
			var calls []string
			port.parameters = func(next, previous serialport.Settings) error {
				if next == draft && previous == before.Port {
					calls = append(calls, "parameters")
					if tc.failParams {
						return applyErr
					}
					return nil
				}
				if next == before.Port && previous == draft {
					calls = append(calls, "restore parameters")
					if tc.failBothRestore {
						return framingRestoreErr
					}
					return nil
				}
				return fmt.Errorf("unexpected parameter call: (%+v, %+v)", next, previous)
			}
			port.lines = func(dtr, rts bool) error {
				if dtr && rts {
					calls = append(calls, "lines")
					return applyErr
				}
				if !dtr && !rts {
					calls = append(calls, "restore lines")
					if tc.failBothRestore {
						return lineRestoreErr
					}
					return nil
				}
				return fmt.Errorf("unexpected line call: (%v, %v)", dtr, rts)
			}
			var result error
			s.ApplyPortSettingsAsync(draft, slices.Delete(rates, 2, 3), func(err error) { result = err })
			awaitAppCallback(t, v)
			if !errors.Is(result, applyErr) || i18n.ErrorText(result, true) == i18n.ErrorText(result, false) {
				t.Fatalf("missing localized driver error: %v", result)
			}
			wantCalls := []string{"parameters"}
			chunk := 200
			if !tc.failParams {
				wantCalls = append(wantCalls, "lines", "restore lines", "restore parameters")
			}
			if tc.failBothRestore {
				chunk = 96
				if !errors.Is(result, lineRestoreErr) || !errors.Is(result, framingRestoreErr) || !strings.Contains(i18n.ErrorText(result, true), "hardware state may not match configuration") {
					t.Fatalf("incomplete rollback result: %v", result)
				}
			} else if !tc.failParams && !strings.Contains(i18n.ErrorText(result, true), "previous configured state was reapplied") {
				t.Fatalf("successful rollback not conveyed: %v", result)
			}
			if !slices.Equal(calls, wantCalls) || s.PortBusy || !reflect.DeepEqual(s.Settings, before) || s.SettingsDirty != tc.dirty || s.SettingsRevision != revision || s.Status != status || s.StatusError {
				t.Fatalf("failed apply changed app state or issued unexpected calls: %v (want %v)", calls, wantCalls)
			}
			if err := s.session.Send(context.Background(), make([]byte, 200)); err != nil || len(port.writes) == 0 || port.writes[0] != chunk {
				t.Fatalf("write lengths after rollback = %v, error = %v; want first chunk %d", port.writes, err, chunk)
			}
		})
	}
}

func TestConnectedBaudActionsCommitOnlyOnSuccess(t *testing.T) {
	s, v, port := liveSettingsFixture(t)
	before := s.Settings
	fail := errors.New("unsupported baud")
	port.parameters = func(next, previous serialport.Settings) error {
		if next.Baud == "38400" {
			return fail
		}
		return nil
	}
	draft, rates := s.PortSettingsDraft()
	if err := s.CustomBaud(" 038400 ", &draft, &rates); err != nil || draft.Baud != "38400" || !slices.Equal(rates, []string{"9600", "38400", "115200", "250000"}) {
		t.Fatalf("connected draft custom baud: %+v, %v, %v", draft, rates, err)
	}
	if s.PortBusy || !reflect.DeepEqual(s.Settings, before) || s.SettingsDirty {
		t.Fatal("editing a draft changed live settings")
	}
	if err := s.CustomBaud("38400", nil, nil); err != nil {
		t.Fatal(err)
	}
	awaitAppCallback(t, v)
	if !s.StatusError || !strings.Contains(s.Status.EN, fail.Error()) || !reflect.DeepEqual(s.Settings, before) || s.SettingsDirty {
		t.Fatal("failed custom baud was not reported or changed app settings")
	}
	s.SelectBaud("9600")
	awaitAppCallback(t, v)
	if s.Settings.Port.Baud != "9600" || s.StatusError || s.SettingsRevision != 1 {
		t.Fatal("live picker did not apply an existing baud")
	}
	if err := s.CustomBaud("57600", nil, nil); err != nil {
		t.Fatal(err)
	}
	awaitAppCallback(t, v)
	if s.Settings.Port.Baud != "57600" || !slices.Equal(s.Settings.BaudRates, []string{"9600", "57600", "115200", "250000"}) || s.SettingsRevision != 2 {
		t.Fatal("successful custom baud did not atomically update selection and list")
	}
}

func TestLiveBaudDisconnectRetainsDriverAndReadErrors(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "obsolete success"
		if fail {
			name = "driver and rollback failure"
		}
		t.Run(name, func(t *testing.T) {
			s, v, port := liveSettingsFixture(t)
			p, before := s.session, s.Settings.Port
			entered, release := make(chan struct{}), make(chan struct{})
			closeEntered, releaseClose := make(chan struct{}), make(chan struct{})
			defer releaseSessionBarrier(release)
			defer releaseSessionBarrier(releaseClose)
			port.parameters = func(serialport.Settings, serialport.Settings) error {
				close(entered)
				<-release
				if fail {
					return errors.Join(errors.New("baud driver failed"), &serialport.ParametersRestoreError{Err: errors.New("baud rollback failed")})
				}
				return nil
			}
			port.beforeClose = func() {
				close(closeEntered)
				<-releaseClose
			}
			s.SelectBaud("9600")
			awaitSessionBarrier(t, entered)
			port.readFailure <- errors.New("serial read failed")
			awaitSessionBarrier(t, p.readDone)
			s.drain(p)
			awaitSessionBarrier(t, p.ctx.Done())
			close(release)
			awaitSessionBarrier(t, closeEntered)
			awaitAppCallback(t, v)
			if !s.StatusError || !strings.Contains(s.Status.EN, "serial read failed") || s.Settings.Port != before || s.SettingsDirty {
				t.Fatalf("disconnect reason or settings lost: status=%+v settings=%+v", s.Status, s.Settings.Port)
			}
			if fail && (!strings.Contains(s.Status.EN, "baud driver failed") || !strings.Contains(s.Status.EN, "baud rollback failed")) {
				t.Fatalf("driver or rollback error lost: %+v", s.Status)
			}
			close(releaseClose)
			awaitAppCallback(t, v)
			if !s.StatusError || !strings.Contains(s.Status.EN, "serial read failed") || (fail && !strings.Contains(s.Status.EN, "baud rollback failed")) {
				t.Fatalf("completion lost an error: %+v", s.Status)
			}
		})
	}
}

func TestConnectedBaudDeletionProtectsSelectionOrStagesReplacement(t *testing.T) {
	s, v, backend := liveSettingsFixture(t)
	before := s.Settings.Port
	if err := s.DeleteBaud(before.Baud, nil, nil); err == nil || s.SettingsRevision != 0 {
		t.Fatal("live deletion removed a currently selected baud")
	}
	if err := s.DeleteBaud("250000", nil, nil); err != nil || s.Settings.Port != before || s.PortBusy || s.SettingsRevision != 1 {
		t.Fatalf("live deletion of an unselected baud = %v", err)
	}
	draft, rates := s.PortSettingsDraft()
	if err := s.DeleteBaud("115200", &draft, &rates); err != nil || draft.Baud != "9600" || !slices.Equal(rates, []string{"9600"}) {
		t.Fatalf("draft did not replace deleted selection: %+v, %v, %v", draft, rates, err)
	}
	if err := s.DeleteBaud("9600", &draft, &rates); err == nil || draft.Baud != "9600" || !slices.Equal(rates, []string{"9600"}) {
		t.Fatal("connected draft lost its final baud choice")
	}
	if s.Settings.Port != before || !slices.Equal(s.Settings.BaudRates, []string{"9600", "115200"}) {
		t.Fatal("deletion from a draft changed live settings")
	}
	fail := errors.New("baud rejected by driver")
	backend.parameters = func(next, previous serialport.Settings) error { return fail }
	var result error
	s.ApplyPortSettingsAsync(draft, rates, func(err error) { result = err })
	awaitAppCallback(t, v)
	if !errors.Is(result, fail) || s.Settings.Port != before || !slices.Equal(s.Settings.BaudRates, []string{"9600", "115200"}) || s.SettingsRevision != 1 {
		t.Fatal("failure committed a staged deletion or replacement baud")
	}
	backend.parameters = nil
	s.ApplyPortSettingsAsync(draft, rates, func(err error) { result = err })
	awaitAppCallback(t, v)
	if result != nil || s.Settings.Port != draft || !slices.Equal(s.Settings.BaudRates, rates) || s.SettingsRevision != 2 {
		t.Fatalf("success did not commit the deletion and replacement atomically: %v", result)
	}
}

func TestLiveSettingsCompletionAfterReadDisconnectCannotCommit(t *testing.T) {
	s, v, port := liveSettingsFixture(t)
	p, before := s.session, s.Settings.Port
	setEntered, releaseSet := make(chan struct{}), make(chan struct{})
	closeEntered, releaseClose := make(chan struct{}), make(chan struct{})
	defer releaseSessionBarrier(releaseSet)
	defer releaseSessionBarrier(releaseClose)
	port.parameters = func(serialport.Settings, serialport.Settings) error {
		close(setEntered)
		<-releaseSet
		return nil
	}
	port.beforeClose = func() {
		close(closeEntered)
		<-releaseClose
	}
	draft, rates := s.PortSettingsDraft()
	draft.Baud = "9600"
	var result error
	s.ApplyPortSettingsAsync(draft, rates, func(err error) {
		result = err
		s.ReportPortSettingsFailure(err)
	})
	awaitSessionBarrier(t, setEntered)
	port.readFailure <- errors.New("device disconnected")
	awaitSessionBarrier(t, p.readDone)
	s.drain(p)
	awaitSessionBarrier(t, p.ctx.Done())
	select {
	case <-closeEntered:
		t.Fatal("driver closed while its settings transaction was in flight")
	default:
	}
	close(releaseSet)
	awaitSessionBarrier(t, closeEntered)
	awaitAppCallback(t, v)
	if !errors.Is(result, errPortSettingsObsolete) || s.Connected() || !s.PortBusy || s.Settings.Port != before || s.SettingsDirty || !s.StatusError || !strings.Contains(s.Status.EN, "device disconnected") {
		t.Fatalf("obsolete completion changed disconnect or settings state: %v", result)
	}
	close(releaseClose)
	awaitAppCallback(t, v)
	if s.PortBusy || !s.StatusError || !strings.Contains(s.Status.EN, "device disconnected") {
		t.Fatalf("disconnect did not own the final busy state and error: %+v", s.Status)
	}
}

func TestPortSettingsFailurePreservesDisconnectAndReportsRollback(t *testing.T) {
	s := &App{}
	s.Fail("串口已断开", "Serial port disconnected", errors.New("device read failed"))
	err := errors.Join(errPortSettingsObsolete, errors.New("rollback failed"))
	s.ReportPortSettingsFailure(err)
	if !s.StatusError || !strings.Contains(s.Status.EN, "device read failed") || !strings.Contains(s.Status.EN, "rollback failed") {
		t.Fatalf("disconnect or rollback failure lost: %+v", s.Status)
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

func (appFileSendPort) SetParameters(serialport.Settings, serialport.Settings) error { return nil }

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

func TestDisconnectReportsInterruptedTextAndPresetWriteErrors(t *testing.T) {
	for _, label := range []Text{{ZH: "发送", EN: "Send"}, {ZH: "多条发送", EN: "Preset send"}} {
		t.Run(label.EN, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			readDone := make(chan struct{})
			close(readDone)
			p := &Session{port: appFileSendPort{}, ctx: ctx, cancel: cancel, readDone: readDone, events: make(chan Event, 1)}
			view := &appTestView{callbacks: make(chan func(), 2)}
			s := &App{view: view, session: p, stopPump: make(chan struct{})}
			started := make(chan struct{})
			release := make(chan struct{})
			s.startSending(label, "", func(_ context.Context, p *Session) error {
				p.emit(Event{Data: []byte("abc"), Sent: true})
				close(started)
				<-release
				return errors.New("serial write failed")
			})
			<-started
			s.TogglePort()
			close(release)
			s.jobs.Wait()
			awaitAppCallback(t, view)
			awaitAppCallback(t, view)
			if !s.StatusError || !strings.Contains(s.Status.EN, "serial write failed") || !strings.Contains(s.Status.EN, label.EN) || s.Tx != 3 {
				t.Fatalf("send failure not reported after disconnect: status=%+v tx=%d", s.Status, s.Tx)
			}
		})
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
	s := &App{SettingsPath: path, settingsStore: &testSettingsStore{}, Settings: config.Settings{Language: "en"}}
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
	s := &App{SettingsPath: filepath.Join(dir, "config.toml"), settingsStore: &testSettingsStore{}, Settings: config.Settings{SendText: "accepted", Presets: []config.Preset{{Data: "accepted preset"}}, BaudRates: []string{"115200"}}}
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
	var result error
	s.ApplyPortSettingsAsync(draft, rates, func(err error) { result = err })
	if !errors.Is(result, errPortSettingsBusy) || s.CustomBaud("38400", nil, nil) == nil || s.DeleteBaud("9600", nil, nil) == nil {
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

type failingLogFile struct {
	writeErr error
	closeErr error
	closed   bool
}

func (f *failingLogFile) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return len(p), nil
}

func (f *failingLogFile) Close() error {
	f.closed = true
	return f.closeErr
}

func TestWriteAndCloseLogRetainsBothErrors(t *testing.T) {
	writeErr := errors.New("log write failure")
	closeErr := errors.New("log close failure")
	for _, tc := range []struct {
		name         string
		write, close error
	}{
		{"write", writeErr, nil},
		{"close", nil, closeErr},
		{"both", writeErr, closeErr},
	} {
		for _, report := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reported=%t", tc.name, report), func(t *testing.T) {
				file := &failingLogFile{writeErr: tc.write, closeErr: tc.close}
				view := &appTestView{callbacks: make(chan func(), 1)}
				s := &App{view: view, closed: make(chan struct{})}
				s.jobs.Add(1)
				go func() {
					defer s.jobs.Done()
					s.completeLogSave(writeAndCloseLog(file, "data"), false, false, nil)
				}()
				if report {
					awaitAppCallback(t, view)
					if !s.StatusError {
						t.Fatal("save failure was not reported")
					}
				}
				err := s.Shutdown()
				if !file.closed {
					t.Fatal("log file was not closed")
				}
				if report && err != nil {
					t.Fatalf("Shutdown repeated reported error: %v", err)
				}
				if !report && ((tc.write != nil && !errors.Is(err, tc.write)) || (tc.close != nil && !errors.Is(err, tc.close))) {
					t.Fatalf("Shutdown lost write or close failure: %v", err)
				}
			})
		}
	}
}

func TestSaveLogFailureSurvivesDiscardedCallback(t *testing.T) {
	for _, report := range []bool{false, true} {
		t.Run(fmt.Sprintf("reported=%t", report), func(t *testing.T) {
			view := &appTestView{callbacks: make(chan func(), 1)}
			s := &App{view: view, closed: make(chan struct{})}
			path := filepath.Join(t.TempDir(), "missing", "display.txt")
			s.SaveLog(path, "data")
			if report {
				awaitAppCallback(t, view)
				if !s.StatusError || !strings.Contains(s.Status.EN, "Failed to save displayed data") {
					t.Fatalf("save error not reported: %q", s.Status.EN)
				}
			}
			err := s.Shutdown()
			if report && err != nil {
				t.Fatalf("Shutdown repeated reported error: %v", err)
			}
			if !report && (!errors.Is(err, os.ErrNotExist) || !strings.Contains(i18n.ErrorText(err, true), "Failed to save displayed data")) {
				t.Fatalf("Shutdown lost save error: %v", err)
			}
		})
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
	if s.Rx != uint64(len("final RX")) || s.Tx != uint64(len("final TX")+len("terminal TX")) || len(v.records) != 4 || !v.records[3].SessionEnd {
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
	if s.Rx != uint64(len("final RX")) || len(v.records) != 2 || !v.records[1].SessionEnd {
		t.Fatal("isolating old session errors also discarded its final receive record")
	}
	current.emit(Event{Data: []byte("new RX")})
	s.deliverDisplay(current)
	if len(v.records) != 3 || string(v.records[2].Data) != "new RX" || s.Rx != uint64(len("final RXnew RX")) {
		t.Fatal("new session data did not follow the old session boundary")
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
			wantRecords := 1 // The terminal boundary does not restore cleared data.
			if !settled {
				// Just like an active connection, new data after clear survives.
				p.emit(Event{Data: []byte("after-clear TX"), Sent: true})
				releaseSessionBarrier(port.releaseRead)
				s.jobs.Wait()
				wantRx = uint64(len("final RX"))
				wantTx = uint64(len("after-clear TX"))
				wantRecords = 3
			}
			awaitAppCallback(t, v)
			if s.Rx != wantRx || s.Tx != wantTx || len(v.records) != wantRecords {
				t.Fatalf("final delivery resurrected cleared data: rx=%d tx=%d records=%d", s.Rx, s.Tx, len(v.records))
			}
			if !v.records[len(v.records)-1].SessionEnd {
				t.Fatal("session boundary did not follow final delivery")
			}
			if s.PortBusy || len(s.closingSessions) != 0 || p.hasDisplay() {
				t.Fatal("final delivery retained the detached session")
			}
		})
	}
}

func TestProtectedOutputUsesOpenedFileIdentityAfterRename(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(fmt.Sprintf("send=%t", source), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "original")
			moved := filepath.Join(dir, "moved")
			if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			opened, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			info, err := opened.Stat()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(path, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
				t.Fatal(err)
			}
			s := &App{}
			sendPath := ""
			if source {
				sendPath = path
				s.sendingFileInfo = info
			} else {
				s.captureTargets = map[*Session]*captureTarget{&Session{}: {path: path, info: info}}
			}
			file, sameSend, sameCapture, err := s.createProtectedOutput(moved, sendPath, "", nil)
			if err != nil || file != nil || sameSend != source || sameCapture == source {
				t.Fatalf("identity collision: file=%v send=%t capture=%t err=%v", file, sameSend, sameCapture, err)
			}
			data, err := os.ReadFile(moved)
			if err != nil || string(data) != "preserve" {
				t.Fatalf("opened file was overwritten: %q %v", data, err)
			}
		})
	}
}

func TestProtectedOutputUsesCurrentStoreIdentity(t *testing.T) {
	root := t.TempDir()
	pinned := filepath.Join(root, "moved")
	old := filepath.Join(root, "app")
	for _, dir := range []string{pinned, old} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	current := filepath.Join(pinned, "config.toml")
	if err := os.WriteFile(current, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	store := &testSettingsStore{identity: func() (os.FileInfo, os.FileInfo, string, error) {
		dir, err := os.Stat(pinned)
		if err != nil {
			return nil, nil, "", err
		}
		file, err := os.Stat(current)
		if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
		return dir, file, "config.toml", err
	}}
	s := &App{settingsStore: store, protectedSettingsPath: filepath.Join(old, "config.toml")}
	link := filepath.Join(root, "hardlink")
	if err := os.Link(current, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{current, link} {
		file, _, _, err := s.createProtectedOutput(path, "", "", nil)
		if file != nil || err == nil {
			t.Fatalf("configuration output %q accepted: %v", path, err)
		}
	}
	if data, err := os.ReadFile(current); err != nil || string(data) != "replacement" {
		t.Fatalf("configuration changed: %q %v", data, err)
	}
	if err := os.Remove(current); err != nil {
		t.Fatal(err)
	}
	file, _, _, err := s.createProtectedOutput(current, "", "", nil)
	if file != nil || err == nil {
		t.Fatalf("missing configuration name accepted: %v", err)
	}
	if _, err := os.Stat(current); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing configuration was created: %v", err)
	}
}

func TestCaptureOpenFailureSurvivesDisconnectOrShutdown(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		t.Run(fmt.Sprintf("disconnect=%t", disconnect), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			readDone := make(chan struct{})
			close(readDone)
			p := &Session{port: appFileSendPort{}, ctx: ctx, cancel: cancel, readDone: readDone, events: make(chan Event, 1)}
			view := &appTestView{callbacks: make(chan func(), 4)}
			failure := errors.New("capture open failure")
			s := &App{view: view, session: p, stopPump: make(chan struct{}), settingsPathError: failure}
			s.startCapture(filepath.Join(t.TempDir(), "capture"), p)
			s.jobs.Wait()
			if disconnect {
				s.Status = Text{ZH: "其他错误", EN: "other failure"}
				s.StatusError = true
				s.disconnect(nil)
				(<-view.callbacks)()
				if !strings.Contains(s.Status.EN, "capture open failure") || !strings.Contains(s.Status.EN, "other failure") {
					t.Fatalf("errors not reported together: %+v", s.Status)
				}
				if err := s.Shutdown(); errors.Is(err, failure) {
					t.Fatalf("reported error repeated at shutdown: %v", err)
				}
			} else if err := s.Shutdown(); !errors.Is(err, failure) {
				t.Fatalf("shutdown lost capture error: %v", err)
			}
		})
	}
}

func TestCaptureOpenCancellationRetainsOtherErrors(t *testing.T) {
	failure := errors.New("close failure")
	err := captureOpenError(errors.Join(context.Canceled, failure))
	if !errors.Is(err, failure) || errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation filtering lost error: %v", err)
	}
	if err := captureOpenError(errors.Join(context.Canceled)); err != nil {
		t.Fatalf("normal cancellation reported: %v", err)
	}
}

func TestCaptureSetupFailureSurvivesShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	readDone := make(chan struct{})
	close(readDone)
	done := make(chan struct{})
	close(done)
	failure := errors.New("previous capture close failure")
	p := &Session{
		port: appFileSendPort{}, ctx: ctx, cancel: cancel, readDone: readDone,
		events:        make(chan Event, 1),
		captureWorker: &captureWriter{stopped: true, done: done, result: failure},
	}
	view := &appTestView{callbacks: make(chan func(), 4)}
	s := &App{view: view, session: p, stopPump: make(chan struct{})}
	s.startCapture(filepath.Join(t.TempDir(), "capture"), p)
	s.jobs.Wait()
	if err := s.Shutdown(); !errors.Is(err, failure) {
		t.Fatalf("shutdown lost capture setup error: %v", err)
	}
}

func TestShutdownRetainsUndispatchedSendErrors(t *testing.T) {
	for _, path := range []string{"", "source.bin"} {
		t.Run(fmt.Sprintf("file=%t", path != ""), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			readDone := make(chan struct{})
			close(readDone)
			p := &Session{port: appFileSendPort{}, ctx: ctx, cancel: cancel, readDone: readDone, events: make(chan Event, 1)}
			view := &appTestView{callbacks: make(chan func(), 4)}
			s := &App{view: view, session: p, stopPump: make(chan struct{})}
			failure := errors.New("undispatched send failure")
			s.startSending(Text{ZH: "发送", EN: "Send"}, path, func(context.Context, *Session) error { return failure })
			s.jobs.Wait()
			if err := s.Shutdown(); !errors.Is(err, failure) {
				t.Fatalf("Shutdown lost send failure: %v", err)
			}
		})
	}
}

func TestDrainReportsCaptureAndReadFailuresTogether(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	readDone := make(chan struct{})
	close(readDone)
	p := &Session{port: appFileSendPort{}, ctx: ctx, cancel: cancel, readDone: readDone, events: make(chan Event, 2)}
	s := &App{session: p, stopPump: make(chan struct{})}
	p.events <- Event{Err: errors.New("record failure")}
	p.events <- Event{Err: errors.New("read failure"), ReadFailed: true}
	s.drain(p)
	s.jobs.Wait()
	if !s.StatusError || !strings.Contains(s.Status.EN, "record failure") || !strings.Contains(s.Status.EN, "read failure") {
		t.Fatalf("combined failures missing: %+v", s.Status)
	}
}

func TestPortRefreshReportsLoadAndEnumerationFailures(t *testing.T) {
	s := &App{LoadError: errors.New("load failure")}
	s.applyPortRefresh(nil, errors.New("enumeration failure"))
	if !s.StatusError || !strings.Contains(s.Status.EN, "load failure") || !strings.Contains(s.Status.EN, "enumeration failure") {
		t.Fatalf("startup failures missing: %+v", s.Status)
	}
}

func TestDisconnectReportsFlushErrorAfterStatusChanges(t *testing.T) {
	writeErr := errors.New("flush failure after read error")
	file := &captureTestFile{writeErr: writeErr, writeStarted: make(chan struct{}, 1), blockWrite: make(chan struct{})}
	p := NewSession(&serialport.Port{}, 115200)
	v := &appTestView{callbacks: make(chan func(), 8)}
	s := &App{view: v, session: p, stopPump: make(chan struct{}), CapturePath: "capture"}
	defer func() {
		releaseSessionBarrier(file.blockWrite)
		_ = s.Shutdown()
	}()
	if err := p.setCapture(file); err != nil {
		t.Fatal(err)
	}
	p.enqueueCapture([]byte("buffered"))
	s.disconnect(errors.New("read failure"))
	awaitSessionBarrier(t, file.writeStarted)
	s.Fail("串口已断开", "Serial port disconnected", errors.New("read failure"))
	releaseSessionBarrier(file.blockWrite)
	awaitAppCallback(t, v)
	if !s.StatusError || !strings.Contains(s.Status.EN, "read failure") || !strings.Contains(s.Status.EN, writeErr.Error()) {
		t.Fatalf("disconnect lost a terminal error: %+v", s.Status)
	}
}

type blockingStatCapture struct {
	*os.File
	started chan struct{}
	release chan struct{}
}

func (f *blockingStatCapture) Stat() (os.FileInfo, error) {
	close(f.started)
	<-f.release
	return f.File.Stat()
}

func TestCaptureReservationDoesNotWaitForOutputFileIO(t *testing.T) {
	dir := t.TempDir()
	file, err := os.Create(filepath.Join(dir, "capture"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	blocked := &blockingStatCapture{File: file, started: make(chan struct{}), release: make(chan struct{})}
	defer releaseSessionBarrier(blocked.release)
	p := &Session{}
	s := &App{captureTargets: map[*Session]*captureTarget{p: {path: file.Name(), file: blocked}}}
	done := make(chan struct{})
	go func() {
		output, _, _, _ := s.createProtectedOutput(filepath.Join(dir, "output"), "", "", nil)
		if output != nil {
			_ = output.Close()
		}
		close(done)
	}()
	awaitSessionBarrier(t, blocked.started)
	reserved := make(chan struct{})
	go func() {
		s.protectCapture(&Session{}, "other", nil)
		close(reserved)
	}()
	awaitSessionBarrier(t, reserved)
	releaseSessionBarrier(blocked.release)
	awaitSessionBarrier(t, done)
}

func TestAppendedOperationErrorSurvivesSettingsSaveSuccess(t *testing.T) {
	saver := &settingsSaver{saveError: Text{ZH: "保存失败", EN: "save failed"}, results: make(chan settingsSaveResult, 1)}
	s := &App{saver: saver, settingsSaveError: true, Status: saver.saveError, StatusError: true, closed: make(chan struct{})}
	s.appendOperationError("关闭失败", "close failed", errors.New("flush failure"))
	saver.results <- settingsSaveResult{}
	s.collectSettingsResults(saver)
	if !s.StatusError || !strings.Contains(s.Status.EN, "flush failure") || s.settingsSaveError {
		t.Fatalf("save success lost operation failure: %+v", s.Status)
	}
}

func TestOutputRejectsConfigurationAndAliases(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	want := []byte("important configuration")
	if err := os.WriteFile(path, want, 0600); err != nil {
		t.Fatal(err)
	}
	paths := []string{path}
	hard := filepath.Join(dir, "hard-link")
	if err := os.Link(path, hard); err == nil {
		paths = append(paths, hard)
	}
	symlink := filepath.Join(dir, "symlink")
	if err := os.Symlink(path, symlink); err == nil {
		paths = append(paths, symlink)
	}
	for _, destination := range paths {
		for _, capture := range []bool{false, true} {
			v := &appTestView{callbacks: make(chan func(), 8)}
			p := &Session{ctx: context.Background()}
			s := &App{view: v, session: p, SettingsPath: path}
			if capture {
				s.startCapture(destination, p)
			} else {
				s.SaveLog(destination, "overwrite")
			}
			awaitAppCallback(t, v)
			if !s.StatusError {
				t.Fatalf("destination %q capture=%v was accepted", destination, capture)
			}
			if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
				t.Fatalf("configuration changed: %q, %v", got, err)
			}
			s.jobs.Wait()
		}
	}
	if len(paths) > 1 && paths[len(paths)-1] == symlink {
		s := &App{SettingsPath: symlink}
		file, _, _, err := s.createProtectedOutput(path, "", "", nil)
		if file != nil {
			file.Close()
		}
		if err == nil {
			t.Fatal("configuration symlink target was accepted")
		}
	}
}

func TestOutputRejectsDanglingConfigurationSymlinkChain(t *testing.T) {
	for _, capture := range []bool{false, true} {
		t.Run(fmt.Sprintf("capture=%v", capture), func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "config.toml")
			middle := filepath.Join(dir, "middle")
			output := filepath.Join(dir, "output")
			if err := os.Symlink("config.toml", middle); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if err := os.Symlink("middle", output); err != nil {
				t.Fatal(err)
			}
			v := &appTestView{callbacks: make(chan func(), 8)}
			p := &Session{ctx: context.Background()}
			s := &App{view: v, session: p, SettingsPath: configPath}
			if capture {
				s.startCapture(output, p)
			} else {
				s.SaveLog(output, "data")
			}
			awaitAppCallback(t, v)
			s.jobs.Wait()
			if !s.StatusError || (capture && s.CapturePath != "") {
				t.Fatal("dangling configuration link was accepted")
			}
			if _, err := os.Lstat(configPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("configuration unexpectedly created: %v", err)
			}
		})
	}
}

func TestOutputCreatesOrdinaryAndDanglingLinkTargets(t *testing.T) {
	dir := t.TempDir()
	s := &App{SettingsPath: filepath.Join(dir, "config.toml")}
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	if err := os.Symlink("target", link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, path := range []string{filepath.Join(dir, "ordinary"), link, link} {
		file, _, _, err := s.createProtectedOutput(path, "", "", nil)
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}
		if _, err := file.WriteString("data"); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "data" {
		t.Fatalf("link target = %q, %v", got, err)
	}
}

func TestOutputConcurrentDanglingLinkReplacement(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	link := filepath.Join(dir, "link")
	if err := os.Symlink("config.toml", link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	s := &App{SettingsPath: configPath}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-done:
				return
			default:
			}
			_ = os.Remove(link)
			_ = os.Symlink("safe", link)
			_ = os.Remove(link)
			_ = os.Symlink("config.toml", link)
		}
	}()
	for range 100 {
		file, _, _, _ := s.createProtectedOutput(link, "", "", nil)
		if file != nil {
			_ = file.Close()
		}
	}
	close(done)
	<-stopped
	if _, err := os.Lstat(configPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("configuration unexpectedly created: %v", err)
	}
}

func TestCancelledOutputDoesNotRemoveConcurrentReplacement(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "output")
	other, err := os.Create(filepath.Join(dir, "other"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	blocked := &blockingStatCapture{File: other, started: make(chan struct{}), release: make(chan struct{})}
	defer releaseSessionBarrier(blocked.release)
	own := &captureTarget{}
	s := &App{SettingsPath: filepath.Join(dir, "config.toml"), captureTargets: map[*Session]*captureTarget{
		{}: {file: blocked},
	}}
	done := make(chan error, 1)
	go func() {
		file, _, _, err := s.createProtectedOutput(output, "", "", own)
		if file != nil {
			_ = file.Close()
		}
		done <- err
	}()
	awaitSessionBarrier(t, blocked.started)
	if err := os.Remove(output); err != nil {
		releaseSessionBarrier(blocked.release)
		<-done
		t.Skipf("cannot replace open file: %v", err)
	}
	want := []byte("replacement belongs to another operation")
	if err := os.WriteFile(output, want, 0600); err != nil {
		releaseSessionBarrier(blocked.release)
		<-done
		t.Fatal(err)
	}
	own.state.Store(2)
	releaseSessionBarrier(blocked.release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if got, err := os.ReadFile(output); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("replacement changed: %q, %v", got, err)
	}
}

func TestConfigurationSaveAndProtectedOutputAreSerialized(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	alias := filepath.Join(dir, "config-link")
	want := []byte("original configuration")
	if err := os.WriteFile(path, want, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, alias); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	other, err := os.Create(filepath.Join(dir, "capture"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	blocked := &blockingStatCapture{File: other, started: make(chan struct{}), release: make(chan struct{})}
	defer releaseSessionBarrier(blocked.release)
	v := &appTestView{callbacks: make(chan func(), 8)}
	s := &App{view: v, SettingsPath: path, settingsStore: &testSettingsStore{}, closed: make(chan struct{}), captureTargets: map[*Session]*captureTarget{
		{}: {path: other.Name(), file: blocked},
	}}
	s.SaveLog(alias, "overwrite")
	awaitSessionBarrier(t, blocked.started)
	s.startSettingsSaver()
	s.saver.enqueue(&settingsSnapshot{settings: config.Settings{Language: "en"}})
	releaseSessionBarrier(blocked.release)
	awaitAppCallback(t, v)
	if err := s.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(alias); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("concurrent save allowed configuration truncation: %q, %v", got, err)
	}
}

func TestFailedConfigurationLoadStillProtectsExecutableConfig(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "serialgo")
	path := filepath.Join(dir, "config.toml")
	want := []byte("invalid = [toml")
	if err := os.WriteFile(exe, nil, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, want, 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "config-alias")
	if err := os.Link(path, alias); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	loadErr := errors.New("configuration decode failed")
	s := newAppFromLoad(config.Settings{}, "", loadErr, func() (string, error) { return exe, nil })
	if s.initialSettingsFile != nil {
		defer s.initialSettingsFile.Close()
	}
	if s.SettingsPath != "" || !errors.Is(s.LoadError, loadErr) {
		t.Fatal("failed load unexpectedly enabled settings persistence")
	}
	v := &appTestView{callbacks: make(chan func(), 8)}
	p := &Session{ctx: context.Background()}
	s.view, s.session = v, p
	s.SaveLog(alias, "overwrite")
	awaitAppCallback(t, v)
	if !s.StatusError {
		t.Fatal("export accepted an alias of an unreadable configuration")
	}
	s.startCapture(path, p)
	awaitAppCallback(t, v)
	if !s.StatusError || s.CapturePath != "" {
		t.Fatal("recording accepted an unreadable configuration")
	}
	s.SettingsDirty = true
	if err := s.FlushSettings(); err == nil {
		t.Fatal("failed load allowed settings to be saved")
	}
	s.jobs.Wait()
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("invalid configuration was modified: %q, %v", got, err)
	}
}

func TestOutputFailsClosedWhenExecutablePathCannotBeResolved(t *testing.T) {
	pathErr := errors.New("executable unavailable")
	s := newAppFromLoad(config.Settings{}, "", pathErr, func() (string, error) { return "", pathErr })
	file, _, _, err := s.createProtectedOutput(filepath.Join(t.TempDir(), "output"), "", "", nil)
	if file != nil {
		file.Close()
	}
	if !errors.Is(err, pathErr) {
		t.Fatalf("output did not fail closed: %v", err)
	}
}

func TestOutputProtectsLoadedIdentityAfterExternalReplacement(t *testing.T) {
	const helperEnv = "SERIALGO_CONFIG_IDENTITY_HELPER"
	mode := os.Getenv(helperEnv)
	if mode == "" {
		for _, failedLoad := range []bool{false, true} {
			t.Run(fmt.Sprintf("failed-load-%v", failedLoad), func(t *testing.T) {
				dir := t.TempDir()
				executable, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(executable)
				if err != nil {
					t.Fatal(err)
				}
				copyPath := filepath.Join(dir, filepath.Base(executable))
				if err := os.WriteFile(copyPath, data, 0700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, "config.toml")
				childMode := "valid"
				if failedLoad {
					childMode = "invalid"
					if err := os.WriteFile(path, []byte("invalid = [toml"), 0600); err != nil {
						t.Fatal(err)
					}
				} else if err := config.Save(path, config.Settings{}); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(path, filepath.Join(dir, "original-link")); err != nil {
					t.Skipf("hard links unavailable: %v", err)
				}
				cmd := exec.Command(copyPath, "-test.run=^TestOutputProtectsLoadedIdentityAfterExternalReplacement$")
				cmd.Env = append(os.Environ(), helperEnv+"="+childMode)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("identity helper failed: %v\n%s", err, output)
				}
			})
		}
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(exe), "config.toml")
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(filepath.Dir(exe), "original-link")
	settings, loadPath, loaded, loadErr := config.LoadWithIdentity()
	if (loadErr != nil) != (mode == "invalid") || loaded == nil {
		t.Fatalf("load mode %s: file=%v err=%v", mode, loaded, loadErr)
	}
	s := newAppFromIdentity(settings, loadPath, loadErr, os.Executable, loaded, true)
	defer loaded.Close()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	v := &appTestView{callbacks: make(chan func(), 8)}
	p := &Session{ctx: context.Background()}
	s.view, s.session = v, p
	s.SaveLog(alias, "overwrite")
	awaitAppCallback(t, v)
	if !s.StatusError {
		t.Fatal("export accepted the original configuration identity")
	}
	s.startCapture(alias, p)
	awaitAppCallback(t, v)
	if !s.StatusError || s.CapturePath != "" {
		t.Fatal("capture accepted the original configuration identity")
	}
	s.jobs.Wait()
	if got, err := os.ReadFile(alias); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("original configuration changed: %q, %v", got, err)
	}
}

func TestOutputProtectsSavedIdentityAfterExternalReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	s := newAppFromLoad(config.Settings{}, path, nil, os.Executable)
	s.settingsStore = &testSettingsStore{}
	s.SettingsDirty = true
	if err := s.FlushSettings(); err != nil {
		t.Fatal(err)
	}
	defer s.savedSettingsFile.Close()
	alias := filepath.Join(dir, "saved-link")
	if err := os.Link(path, alias); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	want, err := os.ReadFile(alias)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	v := &appTestView{callbacks: make(chan func(), 8)}
	s.view = v
	s.SaveLog(alias, "overwrite")
	awaitAppCallback(t, v)
	s.jobs.Wait()
	if !s.StatusError {
		t.Fatal("export accepted the saved configuration identity")
	}
	if got, err := os.ReadFile(alias); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("saved configuration changed: %q, %v", got, err)
	}
}

func TestOutputProtectsSavedDescriptorWhenPathChangesBeforeHandoff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	file, err := config.SaveWithIdentity(path, config.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	alias := filepath.Join(dir, "saved-link")
	if err := os.Link(path, alias); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	want, err := os.ReadFile(alias)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &App{SettingsPath: path}
	s.rememberSavedSettings(file)
	v := &appTestView{callbacks: make(chan func(), 8)}
	s.view = v
	s.SaveLog(alias, "overwrite")
	awaitAppCallback(t, v)
	s.jobs.Wait()
	if !s.StatusError {
		t.Fatal("export accepted the exchanged saved configuration")
	}
	if got, err := os.ReadFile(alias); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("saved configuration changed: %q, %v", got, err)
	}
}

func TestOutputProtectsInitialSameDirectorySymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "original")
	path := filepath.Join(dir, "config.toml")
	want := []byte("original configuration")
	if err := os.WriteFile(target, want, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("original", path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	loaded, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	s := newAppFromIdentity(config.Settings{}, path, nil, os.Executable, loaded, true)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	v := &appTestView{callbacks: make(chan func(), 8)}
	s.view = v
	s.SaveLog(target, "overwrite")
	awaitAppCallback(t, v)
	s.jobs.Wait()
	if !s.StatusError {
		t.Fatal("export accepted the original symlink target")
	}
	if got, err := os.ReadFile(target); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("original symlink target changed: %q, %v", got, err)
	}
}

func TestPendingCaptureCancelledBeforeDisconnectOrShutdown(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(fmt.Sprint("shutdown=", shutdown), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "capture")
			if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			p := NewSession(&serialport.Port{}, 115200)
			v := &appTestView{callbacks: make(chan func(), 8)}
			s := &App{view: v, session: p, stopPump: make(chan struct{}), closed: make(chan struct{})}
			s.fileIOMu.Lock()
			s.startCapture(path, p)
			target := s.protectCapture(p, "", nil)
			done := make(chan error, 1)
			if shutdown {
				go func() { done <- s.Shutdown() }()
			} else {
				s.disconnect(nil)
			}
			deadline := time.After(captureTestTimeout)
			for target.state.Load() != 2 {
				select {
				case <-deadline:
					s.fileIOMu.Unlock()
					t.Fatal("pending capture was not canceled")
				default:
					time.Sleep(time.Millisecond)
				}
			}
			s.fileIOMu.Unlock()
			if shutdown {
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			} else {
				s.jobs.Wait()
				if err := s.Shutdown(); err != nil {
					t.Fatal(err)
				}
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != "keep" {
				t.Fatalf("canceled capture damaged destination: %q, %v", got, err)
			}
		})
	}
}

func TestDisconnectSendFailurePreservesConcurrentCaptureCloseError(t *testing.T) {
	p := NewSession(&serialport.Port{}, 115200)
	v := &appTestView{callbacks: make(chan func(), 8)}
	s := &App{view: v, session: p, stopPump: make(chan struct{}), closed: make(chan struct{})}
	sendErr := errors.New("send failure")
	failure := s.retainTerminalError(sendErr)
	done := make(chan struct{})
	close(done)
	s.sendDone = &sendResult{done: done, err: sendErr, failure: failure}
	s.disconnect(nil)
	s.appendOperationError("关闭接收文件失败", "Failed to close receive file", errors.New("capture close failure"))
	awaitAppCallback(t, v)
	if !s.StatusError || !strings.Contains(s.Status.EN, "capture close failure") || !strings.Contains(s.Status.EN, "send failure") {
		t.Fatalf("disconnect lost a concurrent failure: %+v", s.Status)
	}
	if err := s.Shutdown(); err != nil {
		t.Fatal(err)
	}
}
