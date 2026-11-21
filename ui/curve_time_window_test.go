package ui

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal"
)

func curveTimeWindowSnapshot(t *testing.T, text string) internal.CurveSnapshot {
	t.Helper()
	profile := config.NewCurveProfile("device clock")
	profile.TimePattern = `t=(\d+)`
	profile.TimeScale = 1
	profile.Channels[0].Scale = 1
	profile.Channels = append(profile.Channels, config.CurveChannel{
		Name: "current", Mode: "prefix", Pattern: "I=", Scale: 1, Enabled: true,
	})
	snapshot, err := internal.PreviewCurve(profile, text)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func assertCurveTimeWindowBounds(t *testing.T, v *curveViewState, want curveBounds) {
	t.Helper()
	got, ok := v.bounds()
	if !ok {
		t.Fatal("sampled snapshot produced no visible plot bounds")
	}
	if math.Abs(got.x0-want.x0) > 1e-9 || math.Abs(got.x1-want.x1) > 1e-9 || math.Abs(got.y0-want.y0) > 1e-9 || math.Abs(got.y1-want.y1) > 1e-9 {
		t.Fatalf("plot bounds = %+v, want %+v", got, want)
	}
}

func TestCurveLiveTimeWindowFollowsLatestSample(t *testing.T) {
	for _, test := range []struct {
		name, text string
		want       curveBounds
	}{
		{
			name: "restart_at_zero",
			text: "t=0 VBAT=10\nt=100 VBAT=1000\nt=0 VBAT=20",
			want: curveBounds{-10, 0, 9.2, 20.8},
		},
		{
			name: "restart_at_one",
			text: "t=0 VBAT=10\nt=100 VBAT=1000\nt=1 VBAT=20",
			want: curveBounds{-9, 1, 9.2, 20.8},
		},
		{
			name: "latest_in_first_series_is_not_series_maximum",
			text: "t=0 VBAT=10\nt=95 I=900\nt=100 VBAT=1000\nt=1 VBAT=20",
			want: curveBounds{-9, 1, 9.2, 20.8},
		},
		{
			name: "latest_in_second_series_is_not_series_maximum",
			text: "t=0 I=10\nt=100 VBAT=1000\nt=1 I=20",
			want: curveBounds{-9, 1, 9.2, 20.8},
		},
		{
			name: "short_retained_range_after_restart",
			text: "t=0 VBAT=10\nt=5 VBAT=1000\nt=1 VBAT=20",
			want: curveBounds{-4, 1, -5.84, 223.84},
		},
		{
			name: "monotonic_full_window",
			text: "t=0 VBAT=10\nt=95 VBAT=30\nt=100 VBAT=40",
			want: curveBounds{90, 100, 28.06315789473684, 40.88421052631579},
		},
		{
			name: "monotonic_short_window",
			text: "t=0 VBAT=10\nt=5 VBAT=20",
			want: curveBounds{0, 5, 9.2, 20.8},
		},
		{
			name: "single_sample_default_width",
			text: "t=42 VBAT=20",
			want: curveBounds{42, 43, 19, 21},
		},
		{
			name: "single_zero_sample_default_width",
			text: "t=0 VBAT=20",
			want: curveBounds{0, 1, 19, 21},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			v := newCurveViewState(&config.Settings{})
			v.span = "10 s"
			v.snapshot = curveTimeWindowSnapshot(t, test.text)
			assertCurveTimeWindowBounds(t, v, test.want)
		})
	}
}

func TestCurveHistoricalAndAllWindowsRetainFullTimeRange(t *testing.T) {
	// Autoscale includes clipped line intersections, not the full off-window neighbors.
	snapshot := curveTimeWindowSnapshot(t, "t=0 VBAT=10\nt=5 VBAT=20\nt=50 VBAT=25\nt=55 VBAT=30\nt=95 VBAT=900\nt=100 VBAT=1000\nt=1 VBAT=15")
	for _, test := range []struct {
		name, span string
		position   float64
		want       curveBounds
	}{
		{"earliest_history", "10 s", 0, curveBounds{0, 10, 9.155555555555555, 21.4}},
		{"middle_history", "10 s", 50, curveBounds{45, 55, 24, 30.444444444444443}},
		{"all_at_live_position", "All", 100, curveBounds{0, 100, -69.2, 1079.2}},
		{"all_at_history_position", "All", 0, curveBounds{0, 100, -69.2, 1079.2}},
	} {
		t.Run(test.name, func(t *testing.T) {
			v := newCurveViewState(&config.Settings{})
			v.snapshot, v.span, v.position = snapshot, test.span, test.position
			assertCurveTimeWindowBounds(t, v, test.want)
		})
	}
}

func TestCurvePausedTimeWindowUsesFrozenSnapshot(t *testing.T) {
	v := newCurveViewState(&config.Settings{})
	v.span = "10 s"
	v.snapshot = curveTimeWindowSnapshot(t, "t=0 VBAT=10\nt=100 VBAT=1000")
	v.running, v.paused = true, true
	assertCurveTimeWindowBounds(t, v, curveBounds{90, 100, 893.08, 1007.92})

	live := newCurveViewState(&config.Settings{})
	live.span = v.span
	live.snapshot = curveTimeWindowSnapshot(t, "t=0 VBAT=10\nt=100 VBAT=1000\nt=1 VBAT=20")
	assertCurveTimeWindowBounds(t, live, curveBounds{-9, 1, 9.2, 20.8})
	assertCurveTimeWindowBounds(t, v, curveBounds{90, 100, 893.08, 1007.92})

	v.paused = false
	v.snapshot = live.snapshot
	assertCurveTimeWindowBounds(t, v, curveBounds{-9, 1, 9.2, 20.8})
}

func curveSummaryWindowSeries(t *testing.T) internal.CurveSeries {
	t.Helper()
	var records strings.Builder
	for i := range 1024 {
		fmt.Fprintf(&records, "t=%d VBAT=%d\n", i, (i*29)%127)
	}
	return curveTimeWindowSnapshot(t, records.String()).Series[0]
}

func TestCurveBlockSummarySelectionUsesLargestVisibleColumn(t *testing.T) {
	series := curveSummaryWindowSeries(t)
	for _, tc := range []struct {
		name        string
		x0, x1      float64
		columnWidth float64
		first, end  int
	}{
		{"root", 0, 1023, 1024, 0, 1024},
		{"fallback_to_child", 0, 1023, 512, 0, 512},
		{"fallback_to_base", 0, 1023, 256, 0, 256},
		{"fallback_to_raw", 0, 1023, 128, 0, 0},
		{"clipped_right", 0, 511.5, 0, 0, 512},
		{"clipped_left", 511.5, 1023, 0, 512, 1024},
		{"clipped_on_both_sides", 255.5, 511.5, 0, 256, 512},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var column func(float64) int
			if tc.columnWidth != 0 {
				column = func(x float64) int { return int(x / tc.columnWidth) }
			}
			segments := newCurveVisibleSegments(series, tc.x0, tc.x1)
			a, b, visible := segments.next()
			if !visible || a.X != tc.x0 || b.X != float64(tc.first) {
				t.Fatalf("incoming segment = (%+v, %+v, %t), want [%g, %d]", a, b, visible, tc.x0, tc.first)
			}
			block, ok := segments.nextBlock(column)
			if ok != (tc.end != 0) || (ok && (block.Start != tc.first || block.End != tc.end)) {
				t.Fatalf("summary = (%+v, %t), want [%d, %d)", block, ok, tc.first, tc.end)
			}
			next := max(tc.first+1, tc.end)
			if next < len(series.Points) {
				a, b, visible := segments.next()
				if !visible || a != series.Points[next-1] || b.X != min(tc.x1, float64(next)) {
					t.Fatalf("outgoing segment = (%+v, %+v, %t), want [%d, %g]", a, b, visible, next-1, min(tc.x1, float64(next)))
				}
			}
		})
	}
	if raw := closestCurvePoint(series, 0, 1023, 370.25); raw != 370 || series.Points[raw].Y != 62 {
		t.Fatalf("hover did not retrieve the original interior sample: index %d", raw)
	}
}

func TestCurveBlockSummaryBoundsMatchFullScan(t *testing.T) {
	series := curveSummaryWindowSeries(t)
	blocks := series.Blocks
	v := newCurveViewState(&config.Settings{})
	v.snapshot = internal.CurveSnapshot{LatestX: 1023, Series: []internal.CurveSeries{series}}
	for _, span := range []string{"10 s", "60 s", "300 s", "All"} {
		for _, position := range []float64{0, 35, 100} {
			v.span, v.position = span, position
			got, ok := v.bounds()
			v.snapshot.Series[0].Blocks = nil
			want, wantOK := v.bounds()
			v.snapshot.Series[0].Blocks = blocks
			if ok != wantOK || got != want {
				t.Fatalf("%s position %g: summarized %+v, full scan %+v", span, position, got, want)
			}
		}
	}
}

func TestCurveIndexedWindowsMatchFullScan(t *testing.T) {
	text := "t=0 VBAT=2\nt=1 I=6\nt=10 VBAT=30 I=8\nt=10 VBAT=7 I=9\n" +
		"t=20 VBAT=bad\nt=30 VBAT=4\nt=4 I=20\nt=5 VBAT=50\n" +
		"t=6 VBAT=3\nt=40 VBAT=5 I=6\nt=3 VBAT=7\nt=8 I=12\n"
	snapshot := curveTimeWindowSnapshot(t, text)
	for _, indexed := range snapshot.Series {
		unindexed := indexed
		unindexed.TimeRuns = nil
		for x0 := -1.0; x0 <= 42; x0++ {
			for _, width := range []float64{0, .5, 4, 25, 60} {
				x1 := x0 + width
				fast, full := newCurveVisibleSegments(indexed, x0, x1), newCurveVisibleSegments(unindexed, x0, x1)
				for {
					a, b, ok := fast.next()
					wantA, wantB, wantOK := full.next()
					if ok != wantOK || ok && (a != wantA || b != wantB || fast.nextIndex != full.nextIndex) {
						t.Fatalf("%s [%g, %g]: got %v→%v (%v), want %v→%v (%v)", indexed.Name, x0, x1, a, b, ok, wantA, wantB, wantOK)
					}
					if !ok {
						break
					}
				}
				for _, x := range []float64{x0, (x0 + x1) / 2, x1} {
					got, want := closestCurvePoint(indexed, x0, x1, x), closestCurvePoint(unindexed, x0, x1, x)
					if got != want {
						t.Fatalf("%s [%g, %g] hover %g: index %d, want %d", indexed.Name, x0, x1, x, got, want)
					}
				}
			}
		}
	}
}
