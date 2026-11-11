package ui

import (
	"testing"
	"time"

	"github.com/fcying/SerialGo/internal"
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
