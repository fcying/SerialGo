package ui

import (
	"encoding/binary"
	"time"

	"github.com/fcying/SerialGo/internal"
)

const receiveHistoryBlockSize = 8 * 1024
const receiveHistoryPackLimit = 1024

// Keep RX and TX offsets independent of visibility and the selected row width.
type hexOffsets [2]uint64

func (o *hexOffsets) advance(event internal.Event) uint64 {
	direction := 0
	if event.Sent {
		direction = 1
	}
	before := o[direction]
	o[direction] += uint64(len(event.Data))
	return before
}

type receiveHistoryBlock struct {
	data   []byte
	at     time.Time
	packed bool
	sent   bool
}

type receiveHistory struct {
	blocks         []receiveHistoryBlock
	lastAt         time.Time
	headHexOffsets hexOffsets
}

func (h *receiveHistory) append(event internal.Event) {
	if len(event.Data) > receiveHistoryPackLimit {
		// Large reads already own their immutable payload; don't copy it again.
		h.blocks = append(h.blocks, receiveHistoryBlock{data: event.Data, at: event.At, sent: event.Sent})
		h.lastAt = event.At
		return
	}

	delta := event.At.Sub(h.lastAt)
	n := len(h.blocks)
	// Rebase when a time delta cannot reconstruct both clocks and the zone,
	// for example after a host clock adjustment or a change in location.
	if n == 0 || !h.blocks[n-1].packed || h.lastAt.Add(delta) != event.At ||
		len(h.blocks[n-1].data)+len(event.Data)+2*binary.MaxVarintLen64 > receiveHistoryBlockSize {
		h.blocks = append(h.blocks, receiveHistoryBlock{at: event.At, packed: true})
		delta = 0
	}
	block := &h.blocks[len(h.blocks)-1]
	header := uint64(len(event.Data)) << 1
	if event.Sent || event.SessionEnd {
		header |= 1
	}
	block.data = binary.AppendUvarint(block.data, header)
	block.data = binary.AppendVarint(block.data, int64(delta))
	block.data = append(block.data, event.Data...)
	h.lastAt = event.At
}

// pop walks a block copy during replay, or advances the head during eviction.
func (b *receiveHistoryBlock) pop() internal.Event {
	if !b.packed {
		event := internal.Event{At: b.at, Data: b.data, Sent: b.sent}
		b.data = nil
		return event
	}
	header, n := binary.Uvarint(b.data)
	delta, m := binary.Varint(b.data[n:])
	start := n + m
	end := start + int(header>>1)
	b.at = b.at.Add(time.Duration(delta))
	event := internal.Event{At: b.at, Data: b.data[start:end], Sent: header&1 != 0 && header>>1 != 0, SessionEnd: header == 1}
	b.data = b.data[end:]
	return event
}

func (h *receiveHistory) trim(bytes, target int, evict func(internal.Event)) int {
	for bytes > target && len(h.blocks) != 0 {
		head := h.blocks[0]
		event := head.pop()
		if len(head.data) == 0 {
			if len(h.blocks) == 1 {
				break // Always keep the newest event, even if oversized.
			}
			h.blocks[0] = receiveHistoryBlock{}
			h.blocks = h.blocks[1:]
		} else {
			h.blocks[0] = head
		}
		bytes -= len(event.Data)
		h.headHexOffsets.advance(event)
		if evict != nil {
			evict(event)
		}
	}
	return bytes
}
