package internal

import (
	"bytes"
	"sync/atomic"
	"time"
)

// Each serial read is at most receiveBufferSize bytes: 128 entries retain at
// most 512 KiB, independently of the display and recording queues.
const curveQueueLimit = 128

type curveChunk struct {
	data                        []byte
	at                          time.Time
	epoch                       uint64
	droppedBytes, droppedChunks uint64
	discard                     bool
}

type curveReceiver struct {
	enabled *atomic.Bool
	epoch   *atomic.Uint64
	receive func(curveChunk)
}

type curveWorker struct {
	receiver *curveReceiver
	queue    chan curveChunk
	done     chan struct{}
	// Only the serial reader writes pending; the worker reads it after EOF.
	pending curveChunk
	// The serial reader tracks framing even for reads excluded from a curve.
	readEpoch    uint64
	readEnabled  bool
	readObserved bool
	readEndsLine bool
	discardLine  bool
}

func newCurveWorker(receiver *curveReceiver) *curveWorker {
	worker := &curveWorker{
		receiver: receiver,
		queue:    make(chan curveChunk, curveQueueLimit),
		done:     make(chan struct{}),
	}
	go worker.run()
	return worker
}

// enqueue is called only by the serial reader. Data is immutable and shared
// with display delivery; neither parsing nor its snapshot mutex runs here.
func (w *curveWorker) enqueue(data []byte, at time.Time, epoch uint64, enabled bool) {
	if len(data) == 0 {
		return
	}
	if w.readObserved && (w.readEpoch != epoch || w.readEnabled != enabled) {
		w.discardLine = !w.readEndsLine
	}
	w.readEpoch, w.readEnabled, w.readObserved = epoch, enabled, true
	w.readEndsLine = data[len(data)-1] == '\n'
	if !enabled {
		return
	}
	if w.discardLine {
		end := bytes.IndexByte(data, '\n')
		if end < 0 {
			return
		}
		w.discardLine = false
		data = data[end+1:]
		if len(data) == 0 {
			return
		}
	}
	if w.pending.epoch != epoch {
		w.pending = curveChunk{epoch: epoch}
	}
	chunk := w.pending
	chunk.data, chunk.at = data, at
	if len(data) <= receiveBufferSize {
		select {
		case w.queue <- chunk:
			w.pending = curveChunk{epoch: epoch}
			return
		default:
		}
	}
	w.pending.droppedBytes += uint64(len(data))
	w.pending.droppedChunks++
	w.pending.discard = data[len(data)-1] != '\n'
}

func (w *curveWorker) run() {
	defer close(w.done)
	for chunk := range w.queue {
		w.receiver.receive(chunk)
	}
	// Report a terminal overflow even when no later batch was accepted.
	if w.pending.droppedChunks != 0 {
		w.receiver.receive(w.pending)
	}
}
