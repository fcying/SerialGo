package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/fcying/SerialGo/internal"

	"github.com/egoist/mygo/ui"
	"golang.org/x/text/transform"
)

const historyLimit = 200_000
const recordLimit = 8192

const hexDigits = "0123456789ABCDEF"

type logOptions struct {
	hex, timestamp, showSent bool
	encoding                 string
	gap                      time.Duration
}

type receiveLog struct {
	options              logOptions
	events               []internal.Event
	bytes                int
	text                 ui.TextBuffer
	scroll               ui.ScrollState
	decoder              transform.Transformer
	pending              []byte
	last                 internal.Event
	started, newline, cr bool
}

func (l *receiveLog) clear() {
	l.events = nil
	l.bytes = 0
	l.rebuild()
}

func (l *receiveLog) configure(options logOptions) {
	if l.options != options || l.decoder == nil {
		l.options = options
		l.rebuild()
	}
}

func (l *receiveLog) rebuild() {
	l.decoder = internal.Decoder(l.options.encoding)
	l.pending = l.pending[:0]
	l.started, l.newline, l.cr = false, true, false
	l.last = internal.Event{}
	var out strings.Builder
	for _, event := range l.events {
		l.render(&out, event)
	}
	l.text.Set(out.String())
	l.scroll.Y = math.MaxFloat32
}

func (l *receiveLog) append(events []internal.Event) {
	if len(events) == 0 {
		return
	}
	for _, event := range events {
		l.events = append(l.events, event)
		l.bytes += len(event.Data)
	}
	if l.bytes > historyLimit || len(l.events) > recordLimit {
		i := 0
		for (l.bytes > historyLimit-16_384 || len(l.events)-i > recordLimit-1024) && i < len(l.events)-1 {
			l.bytes -= len(l.events[i].Data)
			i++
		}
		remaining := copy(l.events, l.events[i:])
		clear(l.events[remaining:])
		l.events = l.events[:remaining]
		l.rebuild()
		return
	}
	var out strings.Builder
	for _, event := range events {
		l.render(&out, event)
	}
	if out.Len() == 0 {
		return
	}
	end := l.text.Len()
	l.text.Replace(end, end, out.String())
	if l.scroll.Y >= l.scroll.MaxY-2 {
		l.scroll.Y = math.MaxFloat32
	}
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
