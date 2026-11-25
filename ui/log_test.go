package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"

	gui "github.com/egoist/mygo/ui"
)

func receiveMaxY(l *receiveLog) float64 {
	return max(0, l.view.LineTop(l.text.LineCount())-l.view.ViewportHeight())
}

func TestReceiveLogSplitText(t *testing.T) {
	for _, tc := range []struct {
		encoding string
		data     []byte
	}{
		{"UTF-8", []byte("中")},
		{"GB18030", []byte{0xd6, 0xd0}},
	} {
		t.Run(tc.encoding, func(t *testing.T) {
			var l receiveLog
			l.configure(logOptions{encoding: tc.encoding})
			l.append([]internal.Event{{Data: tc.data[:1]}})
			l.append([]internal.Event{{Data: append(tc.data[1:len(tc.data):len(tc.data)], '\r')}})
			l.append([]internal.Event{{Data: []byte("\nOK")}})
			if got := l.text.String(); got != "中\nOK" {
				t.Fatalf("receive display = %q; want %q", got, "中\nOK")
			}
		})
	}
}

func TestReceiveLogSessionBoundaryFlushesAndReplays(t *testing.T) {
	for _, tc := range []struct {
		encoding      string
		first, second []byte
		want, hex     string
	}{
		{"UTF-8", []byte{0xe4}, []byte{0xb8, 0xad}, "���", "E4 B8 AD "},
		{"GB18030", []byte{0xd6}, []byte{0xd0}, "��", "D6 D0 "},
	} {
		t.Run(tc.encoding, func(t *testing.T) {
			var l receiveLog
			options := logOptions{encoding: tc.encoding}
			l.configure(options)
			l.append([]internal.Event{{Data: tc.first}})
			if got := l.text.String(); got != "" {
				t.Fatalf("incomplete character displayed before disconnect: %q", got)
			}
			l.append([]internal.Event{{SessionEnd: true}})
			if got := l.text.String(); got != "�" {
				t.Fatalf("disconnect did not flush incomplete character: %q", got)
			}
			l.append([]internal.Event{{Data: tc.second}, {SessionEnd: true}})
			if got := l.text.String(); got != tc.want {
				t.Fatalf("sessions were joined: got %q, want %q", got, tc.want)
			}
			options.showSent = true
			l.configure(options)
			if got := l.text.String(); got != "RX < "+tc.want {
				t.Fatalf("reformat lost session boundaries: %q", got)
			}
			options.showSent = false
			l.configure(options)
			if got := l.text.String(); got != tc.want {
				t.Fatalf("rebuild lost session boundaries: %q", got)
			}
			options.hex = true
			l.configure(options)
			hex := l.text.String()
			options.hex = false
			l.configure(options)
			if hex != tc.hex || l.text.String() != tc.want {
				t.Fatalf("HEX toggle changed session history: hex=%q text=%q", hex, l.text.String())
			}
		})
	}
}

func TestReceiveLogSessionBoundaryResetsCR(t *testing.T) {
	for _, encoding := range []string{"UTF-8", "GB18030"} {
		t.Run(encoding, func(t *testing.T) {
			var l receiveLog
			options := logOptions{encoding: encoding}
			l.configure(options)
			l.append([]internal.Event{{Data: []byte("\r")}, {SessionEnd: true}, {Data: []byte("\n")}})
			if got := l.text.String(); got != "\n\n" {
				t.Fatalf("cross-session CR/LF = %q, want two newlines", got)
			}
			options.hex = true
			l.configure(options)
			options.hex = false
			l.configure(options)
			if got := l.text.String(); got != "\n\n" {
				t.Fatalf("replayed cross-session CR/LF = %q, want two newlines", got)
			}
		})
	}
}

func TestReceiveLogSessionBoundarySurvivesEviction(t *testing.T) {
	const limit = 200_000
	options := logOptions{encoding: "UTF-8", limit: limit}
	var l receiveLog
	l.configure(options)
	l.append([]internal.Event{
		{Data: []byte(strings.Repeat("x", 17_000))},
		{Data: []byte{0xe4}}, {SessionEnd: true},
		{Data: []byte{0xb8, 0xad}},
		{Data: []byte(strings.Repeat("y", limit-16_384-3))},
	})
	l.append([]internal.Event{{Data: []byte("z")}})
	got := l.text.String()
	if !strings.HasPrefix(got, "���") {
		t.Fatalf("eviction lost retained session boundary: %q", got[:min(len(got), 20)])
	}
	l.rebuild()
	if l.text.String() != got {
		t.Fatal("rebuild after eviction differs from live display")
	}
}

func TestReceiveLogRendersSavedBytesInHex(t *testing.T) {
	var l receiveLog
	l.configure(logOptions{encoding: "UTF-8", gap: 20 * time.Millisecond})
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	l.append([]internal.Event{
		{At: start, Data: []byte{0x00, 0xff}},
		{At: start.Add(time.Millisecond), Data: []byte{0x01}},
		{At: start.Add(25 * time.Millisecond), Data: []byte{0x80}},
	})
	l.configure(logOptions{hex: true, gap: 20 * time.Millisecond})
	if got := l.text.String(); got != "00 FF 01 \n80 " {
		t.Fatalf("hex display = %q; want separate packets with original bytes", got)
	}
}

func TestReceiveLogNewDataScrollsToBottom(t *testing.T) {
	s := newController(&internal.App{})
	s.settings.Language = "zh-CN"
	s.log.configure(logOptions{encoding: "UTF-8"})
	s.log.append([]internal.Event{{Data: []byte(strings.Repeat("received line\n", 120))}})
	tt := gui.NewTester(s.receivePane, 400, 160)
	box, found := tt.Find("接收窗口")
	if !found {
		t.Fatal("receive window is missing")
	}
	if receiveMaxY(&s.log) <= 0 {
		t.Fatal("receive history does not overflow the window")
	}
	tt.ClickAt(box.X+10, box.Y+10)
	tt.Key(gui.Ctrl, gui.KeyHome)
	tt.Scroll(box.X+box.W/2, box.Y+box.H/2, 0, -100_000)
	if s.log.view.ScrollY() >= receiveMaxY(&s.log) {
		t.Fatal("could not scroll away from the latest data")
	}
	previousY := s.log.view.ScrollY()
	s.settings.Language = "en"
	tt.Frame()
	if s.log.view.ScrollY() != previousY {
		t.Fatal("changing language moved the receive history")
	}
	s.log.append(nil)
	tt.Frame()
	if s.log.view.ScrollY() != previousY {
		t.Fatal("an empty receive batch moved the history")
	}
	for batch := range 4 {
		data := strings.Repeat("新的接收数据, wrapped incoming line with enough text to span several visual rows. ", 80) + "\nlatest incoming line\n"
		s.log.append([]internal.Event{{Data: []byte(data)}})
		tt.Frame()
		if s.log.view.ScrollY() != receiveMaxY(&s.log) {
			t.Fatalf("batch %d did not scroll to the bottom: Y=%g, MaxY=%g", batch, s.log.view.ScrollY(), receiveMaxY(&s.log))
		}
		tt.Frame()
		if s.log.view.ScrollY() != receiveMaxY(&s.log) {
			t.Fatalf("batch %d returned to the old caret on a later frame", batch)
		}
	}
}

func clickLogCheckbox(t *testing.T, tt *gui.Tester, label string) {
	t.Helper()
	checkbox, found := tt.AccessByRole(gui.RoleCheckBox, label)
	if !found {
		t.Fatalf("checkbox %q not found", label)
	}
	x := checkbox.Bounds.X + checkbox.Bounds.H/2
	y := checkbox.Bounds.Y + checkbox.Bounds.H/2
	tt.Press(x, y)
	tt.Release(x, y)
	tt.Frame()
}

func TestTimestampPacketToggleReformatsHistory(t *testing.T) {
	path := t.TempDir() + "/config.toml"
	s, notified := settingsSaveFixture(t, path)
	s.settings.PacketGap = "20"
	s.configureLog()
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s.log.append([]internal.Event{
		{At: start, Data: []byte("ab")},
		{At: start.Add(19 * time.Millisecond), Data: []byte("cd")},
		{At: start.Add(39 * time.Millisecond), Data: []byte("ef")},
	})
	tt := gui.NewTester(s.view, 800, 600)
	if got := s.log.text.String(); got != "abcdef" {
		t.Fatalf("disabled packet display = %q", got)
	}
	label := s.tr("时间戳", "Timestamp")
	clickLogCheckbox(t, tt, label)
	want := "[12:00:00.000] abcd\n[12:00:00.039] ef"
	if got := s.log.text.String(); got != want {
		t.Fatalf("enabled packet display = %q; want %q", got, want)
	}
	completeSettingsSave(t, s, notified)
	if !readSavedSettings(t, path).Timestamp {
		t.Fatal("enabled timestamp packet display was not saved")
	}
	clickLogCheckbox(t, tt, label)
	if got := s.log.text.String(); got != "abcdef" {
		t.Fatalf("disabling packet display did not restore continuous data: %q", got)
	}
	completeSettingsSave(t, s, notified)
	if readSavedSettings(t, path).Timestamp {
		t.Fatal("disabled timestamp packet display was not saved")
	}
}

func TestReceiveLogUsesSelectedByteCapacity(t *testing.T) {
	for _, preset := range config.DisplayBufferPresets() {
		t.Run(preset.Label, func(t *testing.T) {
			var l receiveLog
			l.configure(logOptions{encoding: "UTF-8", limit: preset.Bytes})
			half := preset.Bytes / 2
			first := internal.Event{Data: []byte(strings.Repeat("a", half))}
			second := internal.Event{Data: []byte(strings.Repeat("b", half))}
			l.append([]internal.Event{first, second})
			if l.bytes != preset.Bytes || l.text.Len() != preset.Bytes {
				t.Fatalf("exact capacity lost data: bytes=%d text=%d", l.bytes, l.text.Len())
			}
			l.append([]internal.Event{{Data: []byte("c")}})
			if l.bytes != half+1 || l.text.String() != string(second.Data)+"c" {
				t.Fatalf("overflow did not evict exactly the oldest whole event: bytes=%d", l.bytes)
			}
		})
	}
}

func TestReceiveLogKeepsLargeCapacityWithSmallPackets(t *testing.T) {
	presets := config.DisplayBufferPresets()
	limit := presets[len(presets)-1].Bytes
	var l receiveLog
	l.configure(logOptions{encoding: "UTF-8", limit: limit})
	events := make([]internal.Event, 10_000)
	for i := range events {
		events[i] = internal.Event{Data: []byte{'x'}}
	}
	l.append(events)
	l.append([]internal.Event{{Data: []byte(strings.Repeat("y", limit-len(events)))}})
	if l.bytes != limit || l.text.String() != strings.Repeat("x", len(events))+strings.Repeat("y", limit-len(events)) {
		t.Fatalf("small packets prematurely truncated selected capacity: bytes=%d", l.bytes)
	}
	l.append([]internal.Event{{Data: []byte{'z'}}})
	if l.bytes != 1 || l.text.String() != "z" {
		t.Fatalf("overflow did not evict oldest whole events and keep the newest byte: bytes=%d", l.bytes)
	}
}

func TestReceiveLogLimitChangesTrimImmediately(t *testing.T) {
	presets := config.DisplayBufferPresets()
	var l receiveLog
	options := logOptions{encoding: "UTF-8", limit: presets[1].Bytes}
	l.configure(options)
	l.append([]internal.Event{
		{Data: []byte(strings.Repeat("a", presets[0].Bytes))},
		{Data: []byte(strings.Repeat("b", presets[0].Bytes))},
	})
	options.limit = presets[0].Bytes
	l.configure(options)
	if l.bytes != presets[0].Bytes || l.text.String() != strings.Repeat("b", presets[0].Bytes) {
		t.Fatalf("lower limit did not trim and rebuild immediately: bytes=%d", l.bytes)
	}
	options.limit = presets[1].Bytes
	l.configure(options)
	if l.bytes != presets[0].Bytes || l.text.String() != strings.Repeat("b", presets[0].Bytes) {
		t.Fatal("raising the limit restored discarded history")
	}
}

func TestReceiveLogKeepsNewestOversizedEvent(t *testing.T) {
	limit := config.DisplayBufferPresets()[0].Bytes
	var l receiveLog
	l.configure(logOptions{encoding: "UTF-8", limit: limit})
	oversized := internal.Event{Data: []byte(strings.Repeat("x", limit+1))}
	l.append([]internal.Event{{Data: []byte("old")}, oversized})
	if l.bytes != limit+1 || l.text.String() != string(oversized.Data) {
		t.Fatal("newest oversized event was truncated or discarded")
	}
	l.append([]internal.Event{{Data: []byte("new")}})
	if l.bytes != 3 || l.text.String() != "new" {
		t.Fatal("oversized event was not evicted after newer data arrived")
	}
}

func TestReceiveLogEmptyEventsDoNotConsumeHistory(t *testing.T) {
	var l receiveLog
	l.configure(logOptions{encoding: "UTF-8"})
	l.append([]internal.Event{{Data: []byte("data")}})
	l.scrollToEnd = false
	l.append(make([]internal.Event, 10_000))
	if l.bytes != 4 || l.text.String() != "data" || l.scrollToEnd {
		t.Fatal("empty events consumed history or changed the rendered log")
	}
	l.clear()
	if l.bytes != 0 || len(l.history.blocks) != 0 || l.text.String() != "" {
		t.Fatal("clear did not release retained history")
	}
}

func TestReceiveLogAutoScrollCanPauseAndResume(t *testing.T) {
	s := newController(&internal.App{})
	s.settings.Language = "en"
	s.configureLog()
	s.log.append([]internal.Event{{Data: []byte(strings.Repeat("received line\n", 120))}})
	tt := gui.NewTester(s.receivePane, 400, 160)
	box, found := tt.Find("Receive log")
	if !found || receiveMaxY(&s.log) <= 0 {
		t.Fatal("receive history does not overflow the window")
	}
	tt.ClickAt(box.X+10, box.Y+10)
	tt.Key(gui.Ctrl, gui.KeyHome)
	tt.Scroll(box.X+box.W/2, box.Y+box.H/2, 0, -100_000)
	s.autoScroll = false
	s.autoScrollChanged()
	tt.Frame()
	previousY := s.log.view.ScrollY()
	previousText := s.log.text.String()
	s.log.append([]internal.Event{{Data: []byte(strings.Repeat("new data\n", 40))}})
	tt.Frame()
	if s.log.text.String() == previousText || !strings.Contains(s.log.text.String(), "new data") {
		t.Fatal("received text was not appended while auto scroll was disabled")
	}
	if s.log.view.ScrollY() < previousY-2 || s.log.view.ScrollY() > previousY+2 || s.log.view.ScrollY() >= receiveMaxY(&s.log) {
		t.Fatalf("disabled auto scroll did not preserve the viewport: before=%v after=%v max=%v", previousY, s.log.view.ScrollY(), receiveMaxY(&s.log))
	}
	s.log.view.SetScrollY(receiveMaxY(&s.log) / 2)
	tt.Frame()
	middleY := s.log.view.ScrollY()
	beforeMixedText := s.log.text.Len()
	s.log.append([]internal.Event{{Data: []byte(strings.Repeat("long incoming payload with variable line width ", 100) + "\n")}})
	tt.Frame()
	if s.log.text.Len() <= beforeMixedText || !strings.Contains(s.log.text.String(), "variable line width") {
		t.Fatal("later receive data was not appended while auto scroll was disabled")
	}
	maxDrift := s.log.view.ViewportHeight()
	if s.log.view.ScrollY() < middleY-maxDrift || s.log.view.ScrollY() > middleY+maxDrift || s.log.view.ScrollY() >= receiveMaxY(&s.log)-s.log.view.ViewportHeight()/2 {
		t.Fatalf("disabled auto scroll moved the middle viewport to the latest data: before=%v after=%v max=%v", middleY, s.log.view.ScrollY(), receiveMaxY(&s.log))
	}
	s.autoScroll = true
	s.autoScrollChanged()
	tt.Frame()
	if s.log.view.ScrollY() != receiveMaxY(&s.log) {
		t.Fatalf("resuming auto scroll did not reveal the latest data: Y=%g, MaxY=%g", s.log.view.ScrollY(), receiveMaxY(&s.log))
	}
}

func TestReceiveLogAutoScrollWaitsForSelectionToClear(t *testing.T) {
	s := newController(&internal.App{})
	s.settings.Language = "en"
	s.configureLog()
	s.log.append([]internal.Event{{Data: []byte(strings.Repeat("received line\n", 120))}})

	tt := gui.NewTester(s.receivePane, 400, 160)
	box, found := tt.Find("Receive log")
	if !found || receiveMaxY(&s.log) <= 0 {
		t.Fatal("receive history does not overflow the window")
	}
	leftX, rightX, y := box.X+10, box.X+80, box.Y+10
	tt.Press(leftX, y)
	tt.Move(rightX, y)
	tt.Release(rightX, y)

	tt.SetClipboard("selection not copied")
	tt.Key(gui.Ctrl, gui.KeyC)
	selectedText := tt.Clipboard()
	if selectedText == "selection not copied" || selectedText == "" {
		t.Fatal("mouse drag did not select receive text")
	}

	s.log.append([]internal.Event{{Data: []byte(strings.Repeat("new data\n", 40))}})
	tt.Frame()
	if !s.autoScroll || s.log.autoScrollDisabled || !s.log.scrollToEnd {
		t.Fatal("receiving data disabled auto scroll or lost its pending follow request")
	}
	if s.log.view.ScrollY() >= receiveMaxY(&s.log) {
		t.Fatalf("auto scroll followed the latest data during text selection: Y=%v, MaxY=%v", s.log.view.ScrollY(), receiveMaxY(&s.log))
	}

	tt.SetClipboard("selection cleared")
	tt.Key(gui.Ctrl, gui.KeyC)
	if got := tt.Clipboard(); got != selectedText {
		t.Fatalf("new data changed the selected text: got %q, want %q", got, selectedText)
	}

	// Receive another batch between mouse down and up, while the caret
	// has moved to the click position but the gesture is still active.
	x, y := box.X+200, box.Y+60
	tt.Press(x, y)
	s.log.append([]internal.Event{{Data: []byte(strings.Repeat("during click\n", 40))}})
	tt.Frame()
	if !s.log.scrollToEnd || s.log.view.ScrollY() >= receiveMaxY(&s.log) {
		t.Fatal("auto scroll resumed before the click was released")
	}
	tt.Release(x, y)
	tt.Frame()
	if !s.autoScroll || s.log.autoScrollDisabled || s.log.scrollToEnd || s.log.view.ScrollY() != receiveMaxY(&s.log) {
		t.Fatalf("auto scroll did not resume after clearing selection: enabled=%v disabled=%v pending=%v Y=%v MaxY=%v", s.autoScroll, s.log.autoScrollDisabled, s.log.scrollToEnd, s.log.view.ScrollY(), receiveMaxY(&s.log))
	}
	for _, selection := range s.log.view.Selections() {
		if selection.Anchor != selection.Head {
			t.Fatalf("single click did not clear the actual selection: %+v", selection)
		}
	}
	tt.SetClipboard("no selection")
	tt.Key(gui.Ctrl, gui.KeyC)
	if got := tt.Clipboard(); got != "no selection" {
		t.Fatalf("single click left text selected: %q", got)
	}
	tt.Key(gui.Ctrl, gui.KeyInsert)
	if got := tt.Clipboard(); got != "no selection" {
		t.Fatalf("alternate copy shortcut copied without a selection: %q", got)
	}
	tt.Command("copy")
	if got := tt.Clipboard(); got != "no selection" {
		t.Fatalf("copy command copied without a selection: %q", got)
	}
}

func TestReceiveLogEvictionPreservesRetainedSelection(t *testing.T) {
	s := newController(&internal.App{})
	s.settings.Language = "en"
	s.settings.DisplayBufferLimit = 200_000
	s.configureLog()
	l := &s.log
	old := strings.Repeat("old line\n", 1889)
	const selected = "retained selection"
	fill := strings.Repeat("line\n", 36_500)
	l.append([]internal.Event{{Data: []byte(old)}, {Data: []byte(selected + "\n")}, {Data: []byte(fill)}})
	tt := gui.NewTester(s.receivePane, 400, 160)
	l.view.Select(len(old), len(old)+len(selected))
	tt.Frame()
	l.append([]internal.Event{{Data: []byte(strings.Repeat("new\n", 250))}})
	tt.Frame()
	head := l.text.Slice(0, len(selected))
	if l.bytes > l.options.limit || head != selected {
		t.Fatalf("test did not evict exactly the old event: bytes=%d, head=%q", l.bytes, head)
	}
	selections := l.view.Selections()
	if len(selections) != 1 || selections[0].Anchor != 0 || selections[0].Head != len(selected) {
		t.Fatalf("eviction changed the surviving selection: %+v", selections)
	}
	if !l.scrollToEnd || l.view.ScrollY() >= receiveMaxY(l) {
		t.Fatal("auto-scroll should wait for the surviving selection to clear")
	}
}

func TestReceiveLogManualScrollWhenSelectionIsEvicted(t *testing.T) {
	const oldLines = 1550
	for _, tc := range []struct {
		name         string
		before, want int
	}{
		{name: "viewport evicted", before: 100, want: 0},
		{name: "viewport retained", before: oldLines + 35, want: 35},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newController(&internal.App{})
			s.settings.Language, s.settings.DisplayBufferLimit = "en", 200_000
			s.configureLog()
			s.autoScroll = false
			s.autoScrollChanged()
			l := &s.log
			old := strings.Repeat("old record\n", oldLines)
			keep := strings.Repeat("retained\n", 20_250)
			l.append([]internal.Event{{Data: []byte(old)}, {Data: []byte(keep)}})
			tt := gui.NewTester(s.receivePane, 400, 160)
			start := 100 * len("old record\n")
			l.view.Select(start, start+len("old record"))
			tt.Frame()
			l.view.SetScrollY(l.view.LineTop(tc.before))
			tt.Frame()
			if !l.hasSelection() {
				t.Fatal("failed to select old text before eviction")
			}
			for range 5 {
				l.append([]internal.Event{{Data: []byte(strings.Repeat("new payload\n", 40))}})
				tt.Frame()
			}
			if l.text.Slice(0, len("retained\n")) != "retained\n" || l.hasSelection() {
				t.Fatal("eviction should remove the old text and collapse its selection")
			}
			want := l.view.LineTop(tc.want)
			if y := l.view.ScrollY(); y < want-2 || y > want+2 || l.scrollToEnd {
				t.Fatalf("viewport jumped after eviction: Y=%g, want %g, follow=%v", y, want, l.scrollToEnd)
			}
		})
	}
}

func TestReceiveLogManualScrollPreservesSelection(t *testing.T) {
	for _, backwards := range []bool{false, true} {
		name := "forward"
		if backwards {
			name = "backward"
		}
		t.Run(name, func(t *testing.T) {
			s := newController(&internal.App{})
			s.settings.Language = "en"
			s.configureLog()
			s.log.append([]internal.Event{{Data: []byte(strings.Repeat("received history line\n", 120))}})
			tt := gui.NewTester(s.receivePane, 400, 160)
			box, found := tt.Find("Receive log")
			if !found || receiveMaxY(&s.log) <= 0 {
				t.Fatal("receive history does not overflow the window")
			}
			s.autoScroll = false
			s.autoScrollChanged()
			s.log.view.SetScrollY(receiveMaxY(&s.log) / 3)
			tt.Frame()
			startX, endX, y := box.X+12, box.X+100, box.Y+35
			if backwards {
				startX, endX = endX, startX
			}
			tt.Press(startX, y)
			tt.Move(endX, y)
			tt.Release(endX, y)
			tt.SetClipboard("nothing selected")
			tt.Key(gui.Ctrl, gui.KeyC)
			selectedText := tt.Clipboard()
			if selectedText == "nothing selected" || selectedText == "" {
				t.Fatal("mouse drag did not select historical receive text")
			}
			previousY := s.log.view.ScrollY()
			for batch := range 4 {
				s.log.append([]internal.Event{{Data: []byte(strings.Repeat("new incoming data\n", 40))}})
				tt.Frame()
				tt.Frame()
				tt.SetClipboard("selection lost")
				tt.Key(gui.Ctrl, gui.KeyC)
				if got := tt.Clipboard(); got != selectedText {
					t.Fatalf("batch %d changed the historical selection: got %q, want %q", batch, got, selectedText)
				}
				if s.log.view.ScrollY() < previousY-2 || s.log.view.ScrollY() > previousY+2 || s.log.view.ScrollY() >= receiveMaxY(&s.log) {
					t.Fatalf("batch %d moved the selected history viewport: before=%v after=%v max=%v", batch, previousY, s.log.view.ScrollY(), receiveMaxY(&s.log))
				}
			}
		})
	}
}

func TestReceiveLogManualScrollPreservesDrag(t *testing.T) {
	drag := func(receiving bool) string {
		s := newController(&internal.App{})
		s.settings.Language = "en"
		s.configureLog()
		s.log.append([]internal.Event{{Data: []byte(strings.Repeat("received history line\n", 120))}})
		tt := gui.NewTester(s.receivePane, 400, 160)
		box, found := tt.Find("Receive log")
		if !found || receiveMaxY(&s.log) <= 0 {
			t.Fatal("receive history does not overflow the window")
		}
		s.autoScroll = false
		s.autoScrollChanged()
		s.log.view.SetScrollY(receiveMaxY(&s.log) / 3)
		tt.Frame()
		previousY := s.log.view.ScrollY()
		appendDuringDrag := func() {
			if receiving {
				s.log.append([]internal.Event{{Data: []byte(strings.Repeat("incoming during drag\n", 40))}})
				tt.Frame()
				tt.Frame()
				if s.log.view.ScrollY() < previousY-2 || s.log.view.ScrollY() > previousY+2 || s.log.view.ScrollY() >= receiveMaxY(&s.log) {
					t.Fatalf("receiving during a drag moved the viewport: before=%v after=%v max=%v", previousY, s.log.view.ScrollY(), receiveMaxY(&s.log))
				}
			}
		}
		x, y := box.X+12, box.Y+35
		tt.Press(x, y)
		// Cover the temporarily empty selection immediately after mouse down.
		appendDuringDrag()
		tt.Move(x+70, y)
		appendDuringDrag()
		tt.Move(x+120, y+24)
		appendDuringDrag()
		tt.Release(x+120, y+24)
		appendDuringDrag()
		tt.SetClipboard("nothing selected")
		tt.Key(gui.Ctrl, gui.KeyC)
		selectedText := tt.Clipboard()
		if selectedText == "nothing selected" || selectedText == "" {
			t.Fatal("mouse drag did not select historical receive text")
		}
		return selectedText
	}
	want := drag(false)
	got := drag(true)
	if got != want {
		t.Fatalf("receiving changed the drag's anchor or selected text: got %q, want %q", got, want)
	}
}
