package internal

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fcying/SerialGo/config"
)

func curveRevisionProfile() config.CurveProfile {
	profile := config.NewCurveProfile("revision")
	profile.TimePattern, profile.TimeScale = `t=([^ ]+)`, 1
	profile.Channels = []config.CurveChannel{
		{Name: "voltage", Unit: "V", Mode: "prefix", Pattern: "V=", Scale: 1, Enabled: true, Color: "#112233ff"},
		{Name: "current", Unit: "A", Mode: "prefix", Pattern: "I=", Scale: 1, Enabled: true, Color: "#445566ff"},
	}
	return profile
}

func cloneCurveRevisionSeries(series []CurveSeries) []CurveSeries {
	cloned := slices.Clone(series)
	for i := range cloned {
		cloned[i].Points = slices.Clone(cloned[i].Points)
		cloned[i].TimeRuns = slices.Clone(cloned[i].TimeRuns)
		cloned[i].Blocks = slices.Clone(cloned[i].Blocks)
	}
	return cloned
}

func TestCurveRevisionUnmatchedLinesReuseCallerPoints(t *testing.T) {
	parser := testCurveParser(t, curveRevisionProfile())
	at := time.Unix(100, 0)
	parser.feed([]byte("t=1 V=10 I=100\nt=2 V=20 I=200\n"), at)
	var snapshot CurveSnapshot
	parser.snapshot(&snapshot)
	original := cloneCurveRevisionSeries(snapshot.Series)
	if len(original) != 2 || !slices.Equal(original[0].Points, []CurvePoint{{X: 1, Y: 10}, {X: 2, Y: 20}}) || !slices.Equal(original[1].Points, []CurvePoint{{X: 1, Y: 100}, {X: 2, Y: 200}}) {
		t.Fatalf("initial samples are incorrect: %+v", original)
	}
	revision, statsRevision := snapshot.Revision, snapshot.StatsRevision
	parser.feed([]byte("unrelated complete record\n"), at)
	parser.snapshot(&snapshot)
	if snapshot.Lines != 3 || snapshot.Matched != 2 || snapshot.Invalid != 0 || snapshot.Oversize != 0 || snapshot.StatsRevision == statsRevision {
		t.Fatalf("unmatched record did not update only its statistics: %+v", snapshot)
	}
	if snapshot.Revision != revision || snapshot.LatestX != 2 || !snapshot.DeviceTime || !reflect.DeepEqual(snapshot.Series, original) {
		t.Fatalf("unmatched record changed the plot snapshot: %+v", snapshot)
	}

	// The caller owns its snapshot. A statistics-only refresh must not copy the ring over these sentinels.
	snapshot.Series[0].Points[0] = CurvePoint{X: -101, Y: -102, Break: true}
	snapshot.Series[1].Points[1] = CurvePoint{X: -201, Y: -202, Break: true}
	cached := cloneCurveRevisionSeries(snapshot.Series)
	statsRevision = snapshot.StatsRevision
	parser.feed([]byte("another unrelated record\n"), at)
	parser.snapshot(&snapshot)
	if snapshot.Lines != 4 || snapshot.Matched != 2 || snapshot.StatsRevision == statsRevision || snapshot.Revision != revision {
		t.Fatalf("second unmatched record invalidated data or missed statistics: %+v", snapshot)
	}
	if !reflect.DeepEqual(snapshot.Series, cached) || snapshot.LatestX != 2 || !snapshot.DeviceTime {
		t.Fatalf("statistics-only refresh recopied existing points: %+v", snapshot)
	}

	// A real data change must restore every original point, proving the sentinels never reached parser storage.
	statsRevision = snapshot.StatsRevision
	parser.feed([]byte("t=3 V=30 I=300\n"), at)
	parser.snapshot(&snapshot)
	original[0].Points = append(original[0].Points, CurvePoint{X: 3, Y: 30})
	original[1].Points = append(original[1].Points, CurvePoint{X: 3, Y: 300})
	if snapshot.Revision == revision || snapshot.StatsRevision == statsRevision || snapshot.Lines != 5 || snapshot.Matched != 3 {
		t.Fatalf("new samples did not invalidate data and statistics: %+v", snapshot)
	}
	if !reflect.DeepEqual(snapshot.Series, original) || snapshot.LatestX != 3 || !snapshot.DeviceTime {
		t.Fatalf("data refresh did not restore the complete authoritative point set: %+v", snapshot)
	}
}

func TestCurveRevisionRejectedRecordsOnlyUpdateStatistics(t *testing.T) {
	for _, test := range []struct {
		name, record     string
		matched, invalid uint64
		oversize         uint64
	}{
		{name: "invalid values", record: "t=3 V=1e999 I=NaN\n", matched: 3, invalid: 2},
		{name: "invalid timestamp", record: "t=NaN V=30 I=300\n", matched: 3, invalid: 1},
		{name: "missing timestamp", record: "V=30 I=300\n", matched: 3, invalid: 1},
		{name: "oversize", record: strings.Repeat("x", curveLineLimit+1) + "\n", matched: 2, oversize: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			parser := testCurveParser(t, curveRevisionProfile())
			at := time.Unix(100, 0)
			parser.feed([]byte("t=1 V=10 I=100\nt=2 V=20 I=200\n"), at)
			var snapshot CurveSnapshot
			parser.snapshot(&snapshot)
			original := cloneCurveRevisionSeries(snapshot.Series)
			revision, statsRevision := snapshot.Revision, snapshot.StatsRevision

			parser.feed([]byte(test.record), at)
			parser.snapshot(&snapshot)
			if snapshot.Lines != 3 || snapshot.Matched != test.matched || snapshot.Invalid != test.invalid || snapshot.Oversize != test.oversize || snapshot.StatsRevision == statsRevision {
				t.Fatalf("rejected record statistics are incorrect: %+v", snapshot)
			}
			if snapshot.Revision != revision || snapshot.LatestX != 2 || !snapshot.DeviceTime || !reflect.DeepEqual(snapshot.Series, original) {
				t.Fatalf("rejected record changed the plot snapshot: %+v", snapshot)
			}

			statsRevision = snapshot.StatsRevision
			parser.feed([]byte("t=4 V=40 I=400\n"), at)
			parser.snapshot(&snapshot)
			original[0].Points = append(original[0].Points, CurvePoint{X: 4, Y: 40, Break: true})
			original[1].Points = append(original[1].Points, CurvePoint{X: 4, Y: 400, Break: true})
			if snapshot.Revision == revision || snapshot.StatsRevision == statsRevision || snapshot.Lines != 4 || snapshot.Matched != test.matched+1 || snapshot.Invalid != test.invalid || snapshot.Oversize != test.oversize {
				t.Fatalf("recovery record did not update data and retain rejection statistics: %+v", snapshot)
			}
			if snapshot.LatestX != 4 || !reflect.DeepEqual(snapshot.Series, original) {
				t.Fatalf("recovery record did not preserve samples and break both series: %+v", snapshot)
			}
		})
	}
}

func TestCurveRevisionColorRefreshesPlotWithoutStatistics(t *testing.T) {
	app := &App{}
	if err := app.ApplyCurveProfiles([]config.CurveProfile{curveRevisionProfile()}, 0); err != nil {
		t.Fatal(err)
	}
	if err := app.StartCurve(0); err != nil {
		t.Fatal(err)
	}
	app.curve.parser.feed([]byte("t=1 V=10 I=100\nt=2 V=20 I=200\n"), time.Unix(100, 0))
	var snapshot CurveSnapshot
	app.CurveSnapshot(&snapshot)
	want := snapshot
	want.Series = cloneCurveRevisionSeries(snapshot.Series)
	revision := snapshot.Revision

	// Color changes must invalidate even a populated, caller-cached plot.
	snapshot.Series[0].Points[0] = CurvePoint{X: -1, Y: -1}
	profiles := config.CloneCurveProfiles(app.Settings.CurveProfiles)
	profiles[0].Channels[0].Color = "#abcdef80"
	if err := app.ApplyCurveProfiles(profiles, 0); err != nil {
		t.Fatal(err)
	}
	app.CurveSnapshot(&snapshot)
	if snapshot.Revision == revision {
		t.Fatalf("color edit did not invalidate the plot: %+v", snapshot)
	}
	want.Revision = snapshot.Revision
	want.Series[0].Color = 0xabcdef80
	if !reflect.DeepEqual(snapshot, want) {
		t.Fatalf("color edit changed statistics or failed to refresh the complete plot:\ngot  %+v\nwant %+v", snapshot, want)
	}
}

func TestCurveRevisionClearAndResetRefreshPlotAndStatistics(t *testing.T) {
	for _, operation := range []string{"clear", "restart", "schema reset"} {
		t.Run(operation, func(t *testing.T) {
			app := &App{}
			if err := app.ApplyCurveProfiles([]config.CurveProfile{curveRevisionProfile()}, 0); err != nil {
				t.Fatal(err)
			}
			if err := app.StartCurve(0); err != nil {
				t.Fatal(err)
			}
			at := time.Unix(100, 0)
			app.curve.parser.feed([]byte("t=1 V=10 I=100\nt=2 V=20 I=200\nt=NaN V=30 I=300\n"), at)
			var snapshot CurveSnapshot
			app.CurveSnapshot(&snapshot)
			wantSeries := cloneCurveRevisionSeries(snapshot.Series)
			for i := range wantSeries {
				wantSeries[i].Points = wantSeries[i].Points[:0]
				wantSeries[i].TimeRuns = wantSeries[i].TimeRuns[:0]
				wantSeries[i].Blocks = wantSeries[i].Blocks[:0]
			}
			revision, statsRevision := snapshot.Revision, snapshot.StatsRevision
			deviceTime := true
			switch operation {
			case "clear":
				app.ClearCurve()
			case "restart":
				if err := app.StartCurve(0); err != nil {
					t.Fatal(err)
				}
			case "schema reset":
				profiles := config.CloneCurveProfiles(app.Settings.CurveProfiles)
				profiles[0].TimePattern = ""
				profiles[0].Channels[0].Unit = "mV"
				if err := app.ApplyCurveProfiles(profiles, 0); err != nil {
					t.Fatal(err)
				}
				wantSeries[0].Unit = "mV"
				deviceTime = false
			}
			app.CurveSnapshot(&snapshot)
			if snapshot.Revision == revision || snapshot.StatsRevision == statsRevision {
				t.Fatalf("reset did not invalidate both plot and statistics: %+v", snapshot)
			}
			want := CurveSnapshot{Series: wantSeries, Revision: snapshot.Revision, StatsRevision: snapshot.StatsRevision, DeviceTime: deviceTime}
			if !reflect.DeepEqual(snapshot, want) {
				t.Fatalf("reset left stale data, metadata, or counters:\ngot  %+v\nwant %+v", snapshot, want)
			}

			revision, statsRevision = snapshot.Revision, snapshot.StatsRevision
			app.curve.parser.feed([]byte("t=4 V=40 I=400\n"), at)
			app.CurveSnapshot(&snapshot)
			x := float64(4)
			if !deviceTime {
				x = 100
			}
			wantSeries[0].Points = []CurvePoint{{X: x, Y: 40}}
			wantSeries[1].Points = []CurvePoint{{X: x, Y: 400}}
			wantSeries[0].TimeRuns = []int{0}
			wantSeries[1].TimeRuns = []int{0}
			if snapshot.Revision == revision || snapshot.StatsRevision == statsRevision || snapshot.Lines != 1 || snapshot.Matched != 1 || snapshot.Invalid != 0 || snapshot.Oversize != 0 {
				t.Fatalf("first record after reset did not update data and fresh statistics: %+v", snapshot)
			}
			if snapshot.LatestX != x || snapshot.DeviceTime != deviceTime || !reflect.DeepEqual(snapshot.Series, wantSeries) {
				t.Fatalf("first record after reset retained stale samples or segment state: %+v", snapshot)
			}
		})
	}
}
