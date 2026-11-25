package ui

import (
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"
)

func TestReceiveLogPacksFullCapacityOfSingleByteReads(t *testing.T) {
	const limit = 5_000_000
	var l receiveLog
	l.configure(logOptions{encoding: "UTF-8", limit: limit})
	batch := make([]internal.Event, 512)
	at := time.Now()
	for remaining := limit; remaining > 0; {
		n := min(remaining, len(batch))
		for i := range batch[:n] {
			batch[i] = internal.Event{At: at, Data: []byte("x")}
			at = at.Add(time.Millisecond)
		}
		l.append(batch[:n])
		remaining -= n
	}
	if l.bytes != limit || l.text.String() != strings.Repeat("x", limit) {
		t.Fatalf("single-byte reads did not fill the selected capacity: payload=%d display=%d", l.bytes, l.text.Len())
	}
	var allocated int
	for _, block := range l.history.blocks {
		allocated += cap(block.data)
	}
	if allocated > 6*limit || len(l.history.blocks) > limit/1000 {
		t.Fatalf("tiny reads retained excessive storage: block bytes=%d blocks=%d", allocated, len(l.history.blocks))
	}
	l.append([]internal.Event{{At: at, Data: []byte("z")}})
	const retained = limit - 16_384
	if l.bytes != retained || l.text.String() != strings.Repeat("x", retained-1)+"z" {
		t.Fatalf("packed eviction lost or misordered bytes: payload=%d display=%d", l.bytes, l.text.Len())
	}
}

func TestReceiveLogReplaysPacketBoundariesAcrossPackedBlocks(t *testing.T) {
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var l receiveLog
	l.configure(logOptions{encoding: "UTF-8"})
	// More than one packed block, with an exact threshold at the final event.
	batch := make([]internal.Event, receiveHistoryBlockSize)
	for i := range batch {
		batch[i] = internal.Event{At: start, Data: []byte("a")}
	}
	batch = append(batch,
		internal.Event{At: start.Add(20*time.Millisecond - 1), Data: []byte{0xe4}},
		internal.Event{At: start.Add(20 * time.Millisecond), Data: []byte{0xb8, 0xad}},
		internal.Event{At: start.Add(40 * time.Millisecond), Data: []byte("b")},
		internal.Event{At: start.Add(41 * time.Millisecond), Sent: true, Data: []byte("c")},
		internal.Event{At: start.In(time.FixedZone("alternate", 2*60*60)), Data: []byte("d")},
	)
	l.append(batch)
	l.configure(logOptions{encoding: "UTF-8", timestamp: true, showSent: true, gap: 20 * time.Millisecond})
	want := "[12:00:00.000] RX < " + strings.Repeat("a", receiveHistoryBlockSize) +
		"中\n[12:00:00.040] RX < b\n[12:00:00.041] TX > c\n[14:00:00.000] RX < d"
	if got := l.text.String(); got != want {
		i := 0
		for i < min(len(got), len(want)) && got[i] == want[i] {
			i++
		}
		t.Fatalf("replay differs at byte %d: got %q, want %q", i, got[i:], want[i:])
	}
}

func TestReceiveLogEvictionPreservesPacketFormatting(t *testing.T) {
	const limit = 200_000
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, encoding, cut, rest string
		hex, showSent, sent       bool
	}{
		{name: "split CRLF", cut: "\r", rest: "\nretained"},
		{name: "split UTF-8", cut: "\xe4", rest: "\xb8\xad retained"},
		{name: "split GB18030", encoding: "GB18030", cut: "\xd6", rest: "\xd0 retained"},
		{name: "packet direction", cut: "end", rest: "retained", showSent: true, sent: true},
		{name: "hidden send", cut: "end", rest: "hidden", sent: true},
		{name: "hex", cut: "end", rest: "retained", hex: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := logOptions{encoding: tc.encoding, limit: limit, timestamp: true, showSent: tc.showSent, hex: tc.hex, gap: 20 * time.Millisecond}
			var l, replay receiveLog
			l.configure(options)
			replay.configure(options)
			old := internal.Event{At: at, Data: []byte(strings.Repeat("x", 17_000-len(tc.cut)) + tc.cut)}
			first := internal.Event{At: at.Add(time.Millisecond), Data: []byte(tc.rest), Sent: tc.sent}
			fill := internal.Event{At: first.At, Data: []byte(strings.Repeat("y", limit-16_384-len(tc.rest)-1000))}
			tail := internal.Event{At: first.At, Data: []byte(strings.Repeat("z", 1000))}
			l.append([]internal.Event{old, first, fill})
			l.append([]internal.Event{tail})
			// The retained bytes keep their original logical columns.
			replay.history.headHexOffsets[0] = uint64(len(old.Data))
			replay.rebuild()
			replay.append([]internal.Event{first, fill, tail})
			later := internal.Event{At: at.Add(25 * time.Millisecond), Data: []byte("next packet\r\n")}
			l.append([]internal.Event{later})
			replay.append([]internal.Event{later})
			got, want := l.text.String(), replay.text.String()
			if got != want {
				t.Fatalf("display after eviction: %q… (%d bytes), want %q… (%d bytes)", got[:min(len(got), 80)], len(got), want[:min(len(want), 80)], len(want))
			}
		})
	}
}

func TestReceiveLogRandomChunksMatchRebuildAfterRepeatedEviction(t *testing.T) {
	const limit = 200_000
	const trimTarget = limit - 16_384
	const hexPayload = " \x00\x01\x7f\x80\xff\r\n"
	type testCase struct {
		name, encoding, payload  string
		hex, timestamp, showSent bool
		hexBytesPerLine          int
	}
	cases := []testCase{
		{name: "utf8", encoding: "UTF-8", payload: " 电压⚡\x00\xff\r\n温度\r下一行\n"},
		{name: "utf8 timestamp and direction", encoding: "UTF-8", payload: " 电压⚡\x00\xff\r\n温度\r下一行\n", timestamp: true, showSent: true},
		{name: "gb18030", encoding: "GB18030", payload: " \xd6\xd0\x81\x30\x81\x30\r\n\xb5\xe7\xff\x7f\r\n", timestamp: true},
		{name: "hex", payload: hexPayload, hex: true, showSent: true},
	}
	for _, width := range config.ReceiveHexLineWidths() {
		cases = append(cases, testCase{
			name: "hex timestamp and direction width " + strconv.Itoa(width), payload: hexPayload,
			hex: true, timestamp: true, showSent: true, hexBytesPerLine: width,
		})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rng := rand.New(rand.NewPCG(42, 12345))
			at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
			data := make([]byte, 0, 640*1024)
			for i := 0; len(data) < cap(data)-64; i++ {
				data = strconv.AppendInt(data, int64(i), 10)
				data = append(data, tc.payload...)
			}
			options := logOptions{
				encoding: tc.encoding, limit: limit, hex: tc.hex, hexBytesPerLine: tc.hexBytesPerLine,
				timestamp: tc.timestamp, showSent: tc.showSent, gap: 20 * time.Millisecond,
			}
			var log, reference receiveLog
			log.configure(options)
			reference.configure(options)
			var retained []internal.Event
			var head hexOffsets
			var retainedBytes, trims int
			gaps := [...]time.Duration{0, time.Millisecond, 19 * time.Millisecond, 20 * time.Millisecond, 21 * time.Millisecond}
			batch := make([]internal.Event, 0, 400)
			for batchIndex := 0; len(data) > 0; batchIndex++ {
				count := 1 + rng.IntN(32)
				if batchIndex == 9 {
					// Evict into an incoming batch, not just the previously displayed history.
					count = 400
				}
				batch = batch[:0]
				for range count {
					if len(data) == 0 {
						break
					}
					chunkLimit := 2048
					if rng.IntN(4) == 0 {
						chunkLimit = 5
					}
					n := 1 + rng.IntN(min(chunkLimit, len(data)))
					at = at.Add(gaps[rng.IntN(len(gaps))])
					batch = append(batch, internal.Event{At: at, Data: data[:n], Sent: rng.IntN(7) == 0})
					data = data[n:]
					retainedBytes += n
				}
				log.append(batch)
				// Keep a separate uncompressed event list. The oracle does not use
				// incremental display edits or the production eviction routine.
				retained = append(retained, batch...)
				if retainedBytes > limit {
					trims++
					for retainedBytes > trimTarget && len(retained) > 1 {
						if retained[0].Sent {
							head[1] += uint64(len(retained[0].Data))
						} else {
							head[0] += uint64(len(retained[0].Data))
						}
						retainedBytes -= len(retained[0].Data)
						retained = retained[1:]
					}
				}
				reference.history = receiveHistory{headHexOffsets: head}
				for _, event := range retained {
					reference.history.append(event)
				}
				reference.rebuild()
				got, want := log.text.String(), reference.text.String()
				if got != want {
					i := 0
					for i < min(len(got), len(want)) && got[i] == want[i] {
						i++
					}
					t.Fatalf("batch %d after %d trims: byte %d: got %q, want %q (total lengths %d, %d)", batchIndex, trims, i,
						got[i:min(i+32, len(got))], want[i:min(i+32, len(want))], len(got), len(want))
				}
			}
			if trims < 4 {
				t.Fatalf("fixture exercised only %d evictions", trims)
			}
		})
	}
}

func TestReceiveLogHexRowsSurviveReadsEvictionAndDisplayChanges(t *testing.T) {
	const limit = 200_000
	var l receiveLog
	options := logOptions{encoding: "UTF-8", limit: limit}
	l.configure(options)
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	old := internal.Event{At: at, Data: []byte(strings.Repeat("x", 17_001))}
	retained := []byte(strings.Repeat("y", limit-16_384))
	l.append([]internal.Event{old, {At: at, Data: retained[:5]}, {At: at, Data: retained[5 : len(retained)-1000]}})
	l.append([]internal.Event{{At: at, Data: retained[len(retained)-1000:]}})
	if l.bytes != len(retained) {
		t.Fatalf("expected the first complete event to be evicted, got %d bytes", l.bytes)
	}
	for _, width := range []int{16, 32, 64} {
		options.hex = true
		options.hexBytesPerLine = width
		options.timestamp = true
		l.configure(options)
		first := width - len(old.Data)%width
		want := "[12:00:00.000] " + strings.Repeat("79 ", first) + "\n" + strings.Repeat("79 ", width) + "\n"
		if got := l.text.Slice(0, len(want)); got != want {
			t.Fatalf("width %d with timestamps: got %q, want %q", width, got, want)
		}
		options.timestamp = false
		l.configure(options)
		want = strings.Repeat("79 ", first) + "\n" + strings.Repeat("79 ", width) + "\n"
		if got := l.text.Slice(0, len(want)); got != want {
			t.Fatalf("width %d without timestamps: got %q, want %q", width, got, want)
		}
	}
}
