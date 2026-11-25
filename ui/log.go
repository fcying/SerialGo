package ui

import (
	"bytes"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"

	"github.com/egoist/mygo/ui/editor"
	"golang.org/x/text/transform"
)

const hexDigits = "0123456789ABCDEF"

type logOptions struct {
	hex, timestamp, showSent bool
	encoding                 string
	gap                      time.Duration
	limit                    int
	hexBytesPerLine          int
}

type receiveLog struct {
	logRenderer
	history                         receiveHistory
	bytes                           int
	text                            editor.Buffer
	view                            editor.State
	theme                           editor.Theme
	scrollToEnd, autoScrollDisabled bool
	sessionHasData                  bool
}

type logRenderer struct {
	options              logOptions
	decoder              transform.Transformer
	pending              []byte
	lastAt               time.Time
	hexOffsets           hexOffsets
	lastSent             bool
	started, newline, cr bool
}

func newLogRenderer(options logOptions, head hexOffsets) logRenderer {
	r := logRenderer{options: options, decoder: internal.Decoder(options.encoding), newline: true}
	if options.hex {
		r.hexOffsets = head
	}
	return r
}

// With no builder, replay counts the bytes to remove without materializing them.
type logOutput struct {
	text *strings.Builder
	size int
}

func (out *logOutput) writeByte(b byte) {
	if out.text != nil {
		out.text.WriteByte(b)
	} else {
		out.size++
	}
}

func (out *logOutput) writeString(s string) {
	if out.text != nil {
		out.text.WriteString(s)
	} else {
		out.size += len(s)
	}
}

func (out *logOutput) writeRune(r rune) {
	if out.text != nil {
		out.text.WriteRune(r)
	} else {
		out.size += utf8.RuneLen(r)
	}
}

func (l *receiveLog) clear() {
	l.history = receiveHistory{}
	l.bytes = 0
	l.sessionHasData = false
	l.rebuild()
}

func (l *receiveLog) configure(options logOptions) {
	options.limit = config.NormalizeDisplayBufferLimit(options.limit)
	if options.hex {
		options.hexBytesPerLine = config.NormalizeReceiveHexBytesPerLine(options.hexBytesPerLine)
	} else {
		options.hexBytesPerLine = 0
	}
	if l.options != options || l.decoder == nil {
		l.options = options
		l.trim(nil)
		l.rebuild()
	}
}

func (l *receiveLog) rebuild() {
	l.view.Buffer = &l.text
	l.logRenderer = newLogRenderer(l.options, l.history.headHexOffsets)
	var text strings.Builder
	out := logOutput{text: &text}
	for _, block := range l.history.blocks {
		for len(block.data) != 0 {
			l.render(&out, block.pop())
		}
	}
	l.text.Reset(text.String())
	l.scrollToEnd = !l.autoScrollDisabled
}

func (l *receiveLog) trim(evict func(internal.Event)) {
	if l.bytes > l.options.limit {
		// Evict whole events with headroom to batch prefix updates.
		l.bytes = l.history.trim(l.bytes, l.options.limit-16_384, evict)
	}
}

func (l *receiveLog) append(events []internal.Event) {
	if len(events) == 0 {
		return
	}
	if l.decoder == nil {
		l.configure(l.options)
	}
	var text strings.Builder
	out := logOutput{text: &text}
	for _, event := range events {
		if event.SessionEnd {
			if !l.sessionHasData {
				continue
			}
			l.sessionHasData = false
		} else if len(event.Data) == 0 {
			continue
		} else {
			l.sessionHasData = true
		}
		l.history.append(event)
		l.bytes += len(event.Data)
		l.render(&out, event)
	}
	if l.bytes > l.options.limit {
		l.evictDisplayPrefix(text.String())
	} else if text.Len() != 0 {
		end := l.text.Len()
		l.text.EditRemote(editor.Edit{Start: end, End: end, Text: text.String()})
		l.scrollToEnd = !l.autoScrollDisabled
	}
}

// Rebuild only the prefix whose formatting depends on the evicted bytes.
// The text decoders are stateless. HEX offsets carry across eviction so
// packet state can converge without reflowing the retained byte rows.
func (l *receiveLog) evictDisplayPrefix(tail string) {
	old := newLogRenderer(l.options, l.history.headHexOffsets)
	var removed logOutput
	before := l.bytes
	l.trim(func(event internal.Event) { old.render(&removed, event) })
	end := l.text.Len()
	if before == l.bytes {
		if tail != "" {
			l.text.EditRemote(editor.Edit{Start: end, End: end, Text: tail})
			l.scrollToEnd = !l.autoScrollDisabled
		}
		return
	}
	evicted := removed.size
	var repaired strings.Builder
	fresh := newLogRenderer(l.options, l.history.headHexOffsets)
	out := logOutput{text: &repaired}
	converged := false
	for _, block := range l.history.blocks {
		for len(block.data) != 0 {
			if old.sameState(&fresh) {
				converged = true
				break
			}
			event := block.pop()
			old.render(&removed, event)
			fresh.render(&out, event)
		}
		if converged {
			break
		}
	}
	if !converged {
		l.logRenderer = fresh
	}
	if removed.size > end {
		// Part of this batch was discarded or reformatted before it reached the view.
		repaired.WriteString(tail[removed.size-end:])
		l.text.EditRemote(editor.Edit{End: end, Text: repaired.String()})
	} else {
		stop := removed.size
		prefix := repaired.String()
		// Leave identical retained bytes outside the edit so selections and
		// scroll anchors in the first surviving event also stay attached.
		if n := min(stop-evicted, len(prefix)); n > 0 {
			unchanged := commonLogSuffix(l.text.Slice(stop-n, stop), prefix[len(prefix)-n:])
			stop -= unchanged
			prefix = prefix[:len(prefix)-unchanged]
		}
		l.text.EditRemote(editor.Edit{End: stop, Text: prefix}, editor.Edit{Start: end, End: end, Text: tail})
	}
	if removed.size != 0 || repaired.Len() != 0 || tail != "" {
		l.scrollToEnd = !l.autoScrollDisabled
	}
}

func commonLogSuffix(a, b string) int {
	i := len(a)
	for i > 0 && a[i-1] == b[i-1] {
		i--
	}
	for i < len(a) && !utf8.RuneStart(a[i]) {
		i++
	}
	return len(a) - i
}

func (l *logRenderer) sameState(other *logRenderer) bool {
	return l.started == other.started && l.newline == other.newline && l.cr == other.cr &&
		l.lastSent == other.lastSent && l.lastAt == other.lastAt && l.hexOffsets == other.hexOffsets && bytes.Equal(l.pending, other.pending)
}

func (l *logRenderer) render(out *logOutput, event internal.Event) {
	if event.SessionEnd {
		if !l.options.hex && len(l.pending) != 0 {
			l.decode(out, nil, true)
		}
		l.decoder.Reset()
		l.cr = false
		return
	}
	var offset uint64
	if l.options.hex {
		offset = l.hexOffsets.advance(event)
	}
	if event.Sent && !l.options.showSent {
		return
	}
	newPacket := !l.started || event.Sent != l.lastSent || l.options.gap > 0 && event.At.Sub(l.lastAt) >= l.options.gap
	if newPacket {
		if len(l.pending) != 0 {
			l.decode(out, nil, true)
		}
		l.decoder.Reset()
		if l.started && !l.newline {
			out.writeByte('\n')
		}
		l.cr = false
		l.newline = true
		if l.options.timestamp {
			out.writeByte('[')
			var stamp [12]byte
			out.writeString(string(event.At.AppendFormat(stamp[:0], "15:04:05.000")))
			out.writeString("] ")
			l.newline = false
		}
		if l.options.showSent {
			if event.Sent {
				out.writeString("TX > ")
			} else {
				out.writeString("RX < ")
			}
			l.newline = false
		}
	}
	l.started = true
	l.lastAt, l.lastSent = event.At, event.Sent
	if l.options.hex {
		width := l.options.hexBytesPerLine
		remaining := width - int(offset%uint64(width))
		if remaining == width && !newPacket {
			out.writeByte('\n')
		}
		for i, b := range event.Data {
			if i == remaining {
				out.writeByte('\n')
				remaining += width
			}
			out.writeByte(hexDigits[b>>4])
			out.writeByte(hexDigits[b&15])
			out.writeByte(' ')
		}
		l.newline = false
		return
	}
	l.decode(out, event.Data, false)
}

func (l *logRenderer) decode(out *logOutput, data []byte, atEOF bool) {
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
				out.writeByte('\n')
				l.cr, l.newline = true, true
			case '\n':
				if !l.cr {
					out.writeByte('\n')
				}
				l.cr, l.newline = false, true
			default:
				if r < ' ' && r != '\t' || r == 0x7f {
					r = '·'
				}
				out.writeRune(r)
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
