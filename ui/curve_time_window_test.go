package ui

import (
	"math"
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
