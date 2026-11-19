package internal

import "time"

// Keep parser/command unit tests synchronous; session tests exercise the queue.
func testCurveReceiver(receiver *curveReceiver) func([]byte, time.Time) {
	return func(data []byte, at time.Time) {
		receiver.receive(curveChunk{data: data, at: at, epoch: receiver.epoch.Load()})
	}
}
