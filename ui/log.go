package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"

	"github.com/egoist/mygo/ui"
	"golang.org/x/text/transform"
)

const hexDigits = "0123456789ABCDEF"

type logOptions struct {
	hex, timestamp, showSent bool
	encoding                 string
	gap                      time.Duration
	limit                    int
}

type receiveLog struct {
	options                                         logOptions
	events                                          []internal.Event
	bytes                                           int
	text                                            ui.TextBuffer
	scroll                                          ui.ScrollState
	decoder                                         transform.Transformer
	pending                                         []byte
	last                                            internal.Event
	started, newline, cr                            bool
	scrollToEnd, autoScrollDisabled, preserveScroll bool
	scrollAnchor                                    int
	viewportHeight                                  float32
}

func (l *receiveLog) clear() {
	l.events = nil
	l.bytes = 0
	l.rebuild()
}

func (l *receiveLog) configure(options logOptions) {
	options.limit = config.NormalizeDisplayBufferLimit(options.limit)
	if l.options != options || l.decoder == nil {
		l.options = options
		l.trim()
		l.rebuild()
	}
}

func (l *receiveLog) rebuild() {
	if l.autoScrollDisabled {
		l.captureScrollAnchor()
	}
	l.decoder = internal.Decoder(l.options.encoding)
	l.pending = l.pending[:0]
	l.started, l.newline, l.cr = false, true, false
	l.last = internal.Event{}
	var out strings.Builder
	for _, event := range l.events {
		l.render(&out, event)
	}
	l.text.Set(out.String())
	l.scrollToEnd = !l.autoScrollDisabled
}

func (l *receiveLog) captureScrollAnchor() {
	if l.preserveScroll {
		return
	}
	length := l.text.Len()
	if length == 0 {
		l.scrollAnchor = 0
	} else {
		totalHeight := l.scroll.MaxY + l.viewportHeight
		if totalHeight <= 0 {
			l.scrollAnchor = 0
		} else {
			center := min(max(l.scroll.Y+l.viewportHeight/2, 0), totalHeight)
			l.scrollAnchor = int(float32(length) * center / totalHeight)
			l.scrollAnchor = min(max(l.scrollAnchor, 0), length)
		}
	}
	l.preserveScroll = true
}

// trim evicts complete oldest events but always keeps the newest one, even
// when a single event exceeds the selected byte limit. Empty events are not
// retained, so metadata is bounded by the payload budget without an independent
// record count that would truncate large buffers receiving small packets.
func (l *receiveLog) trim() bool {
	if l.bytes <= l.options.limit {
		return false
	}
	// Leave room for incoming data instead of rebuilding a full buffer per byte.
	target := l.options.limit - 16_384
	i := 0
	for l.bytes > target && i < len(l.events)-1 {
		l.bytes -= len(l.events[i].Data)
		i++
	}
	if i == 0 {
		return false
	}
	remaining := copy(l.events, l.events[i:])
	clear(l.events[remaining:])
	l.events = l.events[:remaining]
	return true
}

func (l *receiveLog) append(events []internal.Event) {
	if len(events) == 0 {
		return
	}
	if l.decoder == nil {
		l.configure(l.options)
	}
	for _, event := range events {
		if len(event.Data) == 0 {
			continue
		}
		l.events = append(l.events, event)
		l.bytes += len(event.Data)
	}
	if l.trim() {
		l.rebuild()
		return
	}
	var out strings.Builder
	for _, event := range events {
		if len(event.Data) != 0 {
			l.render(&out, event)
		}
	}
	if out.Len() == 0 {
		return
	}
	if l.autoScrollDisabled {
		l.captureScrollAnchor()
	}
	end := l.text.Len()
	l.text.Replace(end, end, out.String())
	l.scrollToEnd = !l.autoScrollDisabled
}

func (l *receiveLog) render(out *strings.Builder, event internal.Event) {
	if event.Sent && !l.options.showSent {
		return
	}
	newPacket := !l.started || event.Sent != l.last.Sent || l.options.gap > 0 && event.At.Sub(l.last.At) >= l.options.gap
	if newPacket {
		if len(l.pending) != 0 {
			l.decode(out, nil, true)
		}
		l.decoder.Reset()
		if l.started && !l.newline {
			out.WriteByte('\n')
		}
		l.cr = false
		l.newline = true
		if l.options.timestamp {
			fmt.Fprintf(out, "[%s] ", event.At.Format("15:04:05.000"))
			l.newline = false
		}
		if l.options.showSent {
			if event.Sent {
				out.WriteString("TX > ")
			} else {
				out.WriteString("RX < ")
			}
			l.newline = false
		}
	}
	l.started = true
	l.last = event
	if l.options.hex {
		for _, b := range event.Data {
			out.WriteByte(hexDigits[b>>4])
			out.WriteByte(hexDigits[b&15])
			out.WriteByte(' ')
		}
		l.newline = false
		return
	}
	l.decode(out, event.Data, false)
}

func (l *receiveLog) decode(out *strings.Builder, data []byte, atEOF bool) {
	input := data
	if len(l.pending) > 0 {
		input = append(l.pending, data...)
	}
	var buf [4096]byte
	for {
		n, used, err := l.decoder.Transform(buf[:], input, atEOF)
		for _, r := range string(buf[:n]) {
			switch r {
			case '\r':
				out.WriteByte('\n')
				l.cr, l.newline = true, true
			case '\n':
				if !l.cr {
					out.WriteByte('\n')
				}
				l.cr, l.newline = false, true
			default:
				if r < ' ' && r != '\t' || r == 0x7f {
					r = '·'
				}
				out.WriteRune(r)
				l.cr, l.newline = false, false
			}
		}
		input = input[used:]
		if err != transform.ErrShortDst {
			break
		}
	}
	l.pending = append(l.pending[:0], input...)
}
