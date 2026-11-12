package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"

	gui "github.com/egoist/mygo/ui"
)

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
	if s.log.scroll.MaxY <= 0 {
		t.Fatal("receive history does not overflow the window")
	}
	tt.ClickAt(box.X+10, box.Y+10)
	tt.Key(gui.Ctrl, gui.KeyHome)
	tt.Scroll(box.X+box.W/2, box.Y+box.H/2, 0, -100_000)
	if s.log.scroll.Y >= s.log.scroll.MaxY {
		t.Fatal("could not scroll away from the latest data")
	}
	previousY := s.log.scroll.Y
	s.settings.Language = "en"
	tt.Frame()
	if s.log.scroll.Y != previousY {
		t.Fatal("changing language moved the receive history")
	}
	s.log.append(nil)
	tt.Frame()
	if s.log.scroll.Y != previousY {
		t.Fatal("an empty receive batch moved the history")
	}
	for batch := range 4 {
		data := strings.Repeat("新的接收数据, wrapped incoming line with enough text to span several visual rows. ", 80) + "\nlatest incoming line\n"
		s.log.append([]internal.Event{{Data: []byte(data)}})
		tt.Frame()
		if s.log.scroll.Y != s.log.scroll.MaxY {
			t.Fatalf("batch %d did not scroll to the bottom: Y=%g, MaxY=%g", batch, s.log.scroll.Y, s.log.scroll.MaxY)
		}
		tt.Frame()
		if s.log.scroll.Y != s.log.scroll.MaxY {
			t.Fatalf("batch %d returned to the old caret on a later frame", batch)
		}
	}
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
	if err := tt.Click("Options"); err != nil {
		t.Fatal(err)
	}
	label := "Timestamp and packets"
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	want := "[12:00:00.000] abcd\n[12:00:00.039] ef"
	if got := s.log.text.String(); got != want {
		t.Fatalf("enabled packet display = %q; want %q", got, want)
	}
	completeSettingsSave(t, s, notified)
	if !readSavedSettings(t, path).Timestamp {
		t.Fatal("enabled timestamp packet display was not saved")
	}
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
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
			if l.bytes != preset.Bytes || len(l.events) != 2 || l.text.Len() != preset.Bytes {
				t.Fatalf("exact capacity lost data: bytes=%d events=%d text=%d", l.bytes, len(l.events), l.text.Len())
			}
			l.append([]internal.Event{{Data: []byte("c")}})
			if l.bytes != half+1 || len(l.events) != 2 || l.text.String() != string(second.Data)+"c" {
				t.Fatalf("overflow did not evict exactly the oldest whole event: bytes=%d events=%d", l.bytes, len(l.events))
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
	if l.bytes != limit || len(l.events) != len(events)+1 || !strings.HasPrefix(l.text.String(), strings.Repeat("x", len(events))) {
		t.Fatalf("small packets prematurely truncated selected capacity: bytes=%d events=%d", l.bytes, len(l.events))
	}
	l.append([]internal.Event{{Data: []byte{'z'}}})
	if l.bytes > limit || l.events[len(l.events)-1].Data[0] != 'z' {
		t.Fatalf("overflow did not preserve the newest data within the selected limit: bytes=%d events=%d", l.bytes, len(l.events))
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
	if l.bytes != presets[0].Bytes || len(l.events) != 1 || l.text.String() != string(l.events[0].Data) || l.events[0].Data[0] != 'b' {
		t.Fatalf("lower limit did not trim and rebuild immediately: bytes=%d events=%d", l.bytes, len(l.events))
	}
	options.limit = presets[1].Bytes
	l.configure(options)
	if len(l.events) != 1 || l.bytes != presets[0].Bytes {
		t.Fatal("raising the limit restored discarded history")
	}
}

func TestReceiveLogKeepsNewestOversizedEvent(t *testing.T) {
	limit := config.DisplayBufferPresets()[0].Bytes
	var l receiveLog
	l.configure(logOptions{encoding: "UTF-8", limit: limit})
	oversized := internal.Event{Data: []byte(strings.Repeat("x", limit+1))}
	l.append([]internal.Event{{Data: []byte("old")}, oversized})
	if len(l.events) != 1 || l.bytes != limit+1 || l.text.String() != string(oversized.Data) {
		t.Fatal("newest oversized event was truncated or discarded")
	}
	l.append([]internal.Event{{Data: []byte("new")}})
	if len(l.events) != 1 || l.bytes != 3 || l.text.String() != "new" {
		t.Fatal("oversized event was not evicted after newer data arrived")
	}
}

func TestReceiveLogEmptyEventsDoNotConsumeHistory(t *testing.T) {
	var l receiveLog
	l.configure(logOptions{encoding: "UTF-8"})
	l.append([]internal.Event{{Data: []byte("data")}})
	l.scrollToEnd = false
	l.append(make([]internal.Event, 10_000))
	if l.bytes != 4 || len(l.events) != 1 || l.text.String() != "data" || l.scrollToEnd {
		t.Fatal("empty events consumed history or changed the rendered log")
	}
	l.clear()
	if l.bytes != 0 || len(l.events) != 0 || l.text.String() != "" {
		t.Fatal("clear did not release retained history")
	}
}

func TestReceiveLogAutoScrollCanPauseAndResume(t *testing.T) {
	s := newController(&internal.App{})
	s.settings.Language = "en"
	s.log.viewportHeight = 160
	s.configureLog()
	s.log.append([]internal.Event{{Data: []byte(strings.Repeat("received line\n", 120))}})
	tt := gui.NewTester(s.receivePane, 400, 160)
	box, found := tt.Find("Receive log")
	if !found || s.log.scroll.MaxY <= 0 {
		t.Fatal("receive history does not overflow the window")
	}
	tt.ClickAt(box.X+10, box.Y+10)
	tt.Key(gui.Ctrl, gui.KeyHome)
	tt.Scroll(box.X+box.W/2, box.Y+box.H/2, 0, -100_000)
	s.autoScroll = false
	s.autoScrollChanged()
	tt.Frame()
	previousY := s.log.scroll.Y
	previousText := s.log.text.String()
	s.log.append([]internal.Event{{Data: []byte(strings.Repeat("new data\n", 40))}})
	tt.Frame()
	if s.log.text.String() == previousText || !strings.Contains(s.log.text.String(), "new data") {
		t.Fatal("received text was not appended while auto scroll was disabled")
	}
	if s.log.scroll.Y < previousY-2 || s.log.scroll.Y > previousY+2 || s.log.scroll.Y >= s.log.scroll.MaxY {
		t.Fatalf("disabled auto scroll did not preserve the viewport: before=%v after=%v max=%v", previousY, s.log.scroll.Y, s.log.scroll.MaxY)
	}
	s.log.scroll.Y = s.log.scroll.MaxY / 2
	tt.Frame()
	middleY := s.log.scroll.Y
	beforeMixedText := s.log.text.Len()
	s.log.append([]internal.Event{{Data: []byte(strings.Repeat("long incoming payload with variable line width ", 100) + "\n")}})
	tt.Frame()
	if s.log.text.Len() <= beforeMixedText || !strings.Contains(s.log.text.String(), "variable line width") {
		t.Fatal("later receive data was not appended while auto scroll was disabled")
	}
	maxDrift := s.log.viewportHeight
	if s.log.scroll.Y < middleY-maxDrift || s.log.scroll.Y > middleY+maxDrift || s.log.scroll.Y >= s.log.scroll.MaxY-s.log.viewportHeight/2 {
		t.Fatalf("disabled auto scroll moved the middle viewport to the latest data: before=%v after=%v max=%v", middleY, s.log.scroll.Y, s.log.scroll.MaxY)
	}
	s.autoScroll = true
	s.autoScrollChanged()
	tt.Frame()
	if s.log.scroll.Y != s.log.scroll.MaxY {
		t.Fatalf("resuming auto scroll did not reveal the latest data: Y=%g, MaxY=%g", s.log.scroll.Y, s.log.scroll.MaxY)
	}
}
