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
	s := &controller{}
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
	s, notified := settingsSaveFixture(t, path, config.Save)
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
