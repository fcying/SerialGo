package internal

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fcying/SerialGo/config"
)

func TestCurveManyChannelsKeepIndependentSamples(t *testing.T) {
	for _, count := range []int{9, 32, 128} {
		t.Run(fmt.Sprintf("channels_%d", count), func(t *testing.T) {
			profile := config.NewCurveProfile("many channels")
			profile.Channels = make([]config.CurveChannel, count)
			var record strings.Builder
			for i := range profile.Channels {
				profile.Channels[i] = config.CurveChannel{
					Name: fmt.Sprintf("Channel %d", i), Mode: "prefix", Pattern: fmt.Sprintf("C%d=", i),
					Scale: 1, Color: config.DefaultCurveColor(i), Enabled: true,
				}
				fmt.Fprintf(&record, "C%d=%d ", i, i+1)
			}
			record.WriteByte('\n')
			compiled, err := compileCurve(profile)
			if err != nil {
				t.Fatal(err)
			}
			var parser curveParser
			parser.reset(compiled)
			parser.feed([]byte(record.String()), time.Unix(10, 0))
			parser.feed([]byte(fmt.Sprintf("C%d=999\n", count-1)), time.Unix(11, 0))
			parser.feed([]byte("C0=invalid\nnoise\n"), time.Unix(12, 0))
			var snapshot CurveSnapshot
			parser.snapshot(&snapshot)
			if len(snapshot.Series) != count || snapshot.Matched != 3 || snapshot.Invalid != 1 || snapshot.LatestX != 11 {
				t.Fatalf("multi-channel acquisition state is incorrect: %+v", snapshot)
			}
			for i := range snapshot.Series {
				series := &snapshot.Series[i]
				wantCount := 1
				if i == count-1 {
					wantCount = 2
				}
				if series.Name != profile.Channels[i].Name || len(series.Points) != wantCount || series.Points[0].X != 10 || series.Points[0].Y != float64(i+1) {
					t.Fatalf("channel %d lost or duplicated samples: %+v", i, series)
				}
			}
			last := snapshot.Series[count-1].Points[1]
			if last.X != 11 || last.Y != 999 {
				t.Fatalf("sparse update did not reach the last channel: %+v", last)
			}
		})
	}
}
