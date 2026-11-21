package internal

import (
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fcying/SerialGo/config"
)

func TestCurveDeleteSameSchemaResetsAcquisition(t *testing.T) {
	for _, test := range []struct {
		name    string
		index   int
		active  int
		count   int
		running bool
	}{
		{"running replacement at same index", 0, 0, 2, true},
		{"stopped replacement at same index", 0, 0, 2, false},
		{"inactive deletion at same active index", 1, 0, 2, true},
		{"last deletion selects previous index", 2, 1, 3, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := &App{}
			profiles := []config.CurveProfile{config.NewCurveProfile("A"), config.NewCurveProfile("B"), config.NewCurveProfile("C")}
			profiles = profiles[:test.count]
			if err := app.ApplyCurveProfiles(profiles, 0); err != nil {
				t.Fatal(err)
			}
			if err := app.StartCurve(0); err != nil {
				t.Fatal(err)
			}
			receive := testCurveReceiver(app.curveReceiver())
			at := time.Unix(1, 0)
			receive([]byte("VBAT=1000\nVBAT=bad\n"+strings.Repeat("x", curveLineLimit+1)+"\nVBAT="), at)
			if !test.running {
				app.StopCurve()
			}
			var snapshot CurveSnapshot
			app.CurveSnapshot(&snapshot)
			if snapshot.Lines != 3 || snapshot.Matched != 2 || snapshot.Invalid != 1 || snapshot.Oversize != 1 || len(snapshot.Series[0].Points) != 1 {
				t.Fatalf("删除前的采样状态错误: %+v", snapshot)
			}
			revision, settingsRevision, generation := snapshot.Revision, app.SettingsRevision, app.curve.generation
			previous := app.Settings.CurveProfiles
			if err := app.DeleteCurveProfile(test.index); err != nil {
				t.Fatal(err)
			}
			app.CurveSnapshot(&snapshot)
			if app.curve.enabled.Load() != test.running || app.curve.generation != generation || app.SettingsRevision != settingsRevision+1 || len(app.Settings.CurveProfiles) != test.count-1 || app.Settings.CurveProfile != test.active {
				t.Fatalf("删除改变了运行状态或未保存正确活动索引: %+v", app.Settings)
			}
			if snapshot.Revision != revision+1 || snapshot.LatestX != 0 || snapshot.Lines != 0 || snapshot.Matched != 0 || snapshot.Invalid != 0 || snapshot.Oversize != 0 || len(snapshot.Series) != 1 || len(snapshot.Series[0].Points) != 0 || app.curve.parser.lineSize != 0 || app.curve.parser.discard {
				t.Fatalf("同结构删除未清空点集、计数或半行: %+v", snapshot)
			}
			for i, profile := range app.Settings.CurveProfiles {
				oldIndex := i
				if i >= test.index {
					oldIndex++
				}
				if profile.Name != previous[oldIndex].Name {
					t.Fatalf("删除了错误的方案: %+v", app.Settings.CurveProfiles)
				}
				previous[oldIndex].Channels[0].Pattern = "OLD="
				if profile.Channels[0].Pattern != "VBAT=" {
					t.Fatal("删除后的通道仍与旧设置共享存储")
				}
			}
			receive([]byte("9000\nVBAT=2000\n"), at.Add(time.Second))
			app.CurveSnapshot(&snapshot)
			if test.running {
				if snapshot.Lines != 2 || snapshot.Matched != 1 || snapshot.LatestX != 2 || len(snapshot.Series[0].Points) != 1 || snapshot.Series[0].Points[0] != (CurvePoint{X: 2, Y: 2}) {
					t.Fatalf("删除后拼接了旧半行或未继续采样: %+v", snapshot)
				}
			} else if snapshot.Lines != 0 || len(snapshot.Series[0].Points) != 0 || snapshot.LatestX != 0 {
				t.Fatalf("停止状态的删除重新启动了采样: %+v", snapshot)
			}
		})
	}
}

func TestCurvePointLimitResizesLiveHistory(t *testing.T) {
	profile := config.NewCurveProfile("resize")
	profile.PointLimit = 3
	app := &App{Settings: config.Settings{CurveProfiles: []config.CurveProfile{profile}}}
	err := app.StartCurve(0)
	if err != nil {
		t.Fatal(err)
	}
	receive := testCurveReceiver(app.curveReceiver())
	at := time.Unix(1, 0)
	var snapshot CurveSnapshot
	check := func(first, last int, lines uint64) {
		t.Helper()
		app.CurveSnapshot(&snapshot)
		points := snapshot.Series[0].Points
		if !app.CurveRunning() || len(points) != last-first+1 || snapshot.Lines != lines {
			t.Fatalf("want points %d through %d after %d lines, got %+v", first, last, lines, snapshot)
		}
		for i, point := range points {
			want := CurvePoint{X: 1, Y: float64(first + i)}
			if point != want {
				t.Fatalf("point %d: got %+v, want %+v", i, point, want)
			}
		}
	}
	resize := func(limit int) {
		t.Helper()
		profile.PointLimit = limit
		err := app.ApplyCurveProfiles([]config.CurveProfile{profile}, 0)
		if err != nil {
			t.Fatal(err)
		}
	}
	receive([]byte("VBAT=1000\nVBAT=2000\nVBAT=3000\nVBAT=4000\nVBAT="), at)
	check(2, 4, 4)
	resize(5)
	receive([]byte("5000\nVBAT=6000\n"), at)
	check(2, 6, 6)
	resize(2)
	check(5, 6, 6)
	receive([]byte("VBAT=7000\n"), at)
	check(6, 7, 7)
	resize(0) // Unwrap the existing ring before appending without eviction.
	receive([]byte("VBAT=8000\nVBAT=9000\nVBAT=10000\n"), at)
	check(6, 10, 10)
	resize(1)
	check(10, 10, 10)
	receive([]byte("VBAT=11000\n"), at)
	check(11, 11, 11)
}

func TestCurveTimeRunsFollowEvictionAndResize(t *testing.T) {
	profile := config.NewCurveProfile("clock resets")
	profile.TimePattern = `t=(\d+)`
	profile.PointLimit = 4
	parser := testCurveParser(t, profile)
	var snapshot CurveSnapshot
	check := func(want []int) {
		t.Helper()
		parser.snapshot(&snapshot)
		got := snapshot.Series[0].TimeRuns
		if !slices.Equal(got, want) {
			t.Fatalf("indexed starts %v, want %v for points %v", got, want, snapshot.Series[0].Points)
		}
	}
	parser.feed([]byte("t=10 VBAT=1\nt=20 VBAT=1\nt=0 VBAT=1\nt=1 VBAT=1\n"), time.Time{})
	check([]int{0, 2})
	parser.feed([]byte("t=0 VBAT=1\n"), time.Time{})
	check([]int{0, 1, 3})
	parser.feed([]byte("t=1 VBAT=1\n"), time.Time{})
	check([]int{0, 2})
	parser.setPointLimit(3)
	check([]int{0, 1})
	parser.setPointLimit(0)
	parser.feed([]byte("t=0 VBAT=1\n"), time.Time{})
	check([]int{0, 1, 3})
	parser.setPointLimit(1)
	parser.feed([]byte("t=0 VBAT=1\n"), time.Time{})
	check([]int{0})
}

func TestCurveBlockSummariesTrackRingChanges(t *testing.T) {
	const limit = 4*curveSummaryBlockSize + 77
	var ring curveRing
	var retained []CurvePoint
	add := func(i, limit int) {
		point := CurvePoint{X: float64(i), Y: float64((i * 71) % 137), Break: i == 512 || i == 2100}
		if i >= 1408 {
			point.X -= 1000
		}
		ring.segment = point.Break
		ring.add(point, limit)
		retained = append(retained, point)
		if limit > 0 && len(retained) > limit {
			retained = retained[1:]
		}
	}
	check := func() {
		t.Helper()
		points := make([]CurvePoint, len(retained))
		ring.copyLatest(points)
		if !slices.Equal(points, retained) {
			t.Fatalf("raw history diverged at head %d, length %d", ring.head, len(retained))
		}
		// Independently scan each aligned range in the retained raw sequence.
		// This also checks completeness, precedence and earliest-point extrema ties.
		var want []CurveBlock
		for size := curveSummaryBlockSize; size <= len(points); size *= 2 {
			for physical := 0; physical+size <= len(points); physical += size {
				start := physical - ring.head
				if start < 0 {
					start += len(points)
				}
				if start+size > len(points) {
					continue
				}
				block := CurveBlock{Start: start, End: start + size, Min: start, Max: start}
				valid := true
				for i := start; i < block.End; i++ {
					if i > start && (points[i].Break || points[i].X < points[i-1].X) {
						valid = false
						break
					}
					if points[i].Y < points[block.Min].Y {
						block.Min = i
					}
					if points[i].Y > points[block.Max].Y {
						block.Max = i
					}
				}
				if valid {
					want = append(want, block)
				}
			}
		}
		slices.SortFunc(want, func(a, b CurveBlock) int {
			if a.Start != b.Start {
				return a.Start - b.Start
			}
			return b.End - a.End
		})
		if got := ring.copyBlocks(nil); !slices.Equal(got, want) {
			t.Fatalf("head %d, length %d: summaries %v, want %v", ring.head, len(points), got, want)
		}
	}
	for i := range 2900 {
		add(i, limit)
		if i%31 == 0 || (i+1)%curveSummaryBlockSize == 0 || (i+1)%limit == 0 {
			check()
		}
	}
	check()
	ring.resize(1400)
	check()
	for i := 2900; i < 3220; i++ {
		add(i, 1400)
	}
	check()
	ring.resize(777)
	retained = retained[len(retained)-777:]
	check()
	ring.resize(0)
	for i := 3220; i < 3800; i++ {
		add(i, 0)
	}
	check()
}

func TestCurveBlockParentsRequireContinuousOrderedJoins(t *testing.T) {
	const base = curveSummaryBlockSize
	for _, tc := range []struct {
		name             string
		change           func(int, *CurvePoint)
		wantParent       bool
		wantRightSubtree bool
	}{
		{"equal_time_and_leading_break", func(i int, p *CurvePoint) { p.Break = i == 0 }, true, true},
		{"break_at_join", func(i int, p *CurvePoint) { p.Break = i == 2*base }, false, true},
		{"backwards_at_join", func(i int, p *CurvePoint) {
			if i >= 2*base {
				p.X -= 100
			}
		}, false, true},
		{"nonfinite_value", func(i int, p *CurvePoint) {
			if i == 2*base+1 {
				p.Y = math.NaN()
			}
		}, false, false},
		{"nonfinite_time", func(i int, p *CurvePoint) {
			if i == 2*base+1 {
				p.X = math.Inf(1)
			}
		}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ring curveRing
			for i := range 4 * base {
				p := CurvePoint{X: float64(i / 8)}
				if i == base+19 {
					p.Y = 11
				}
				if i == 3*base+21 {
					p.Y = -13
				}
				tc.change(i, &p)
				ring.segment = p.Break
				ring.add(p, 0)
			}
			blocks := ring.copyBlocks(nil)
			for _, check := range []struct {
				block CurveBlock
				want  bool
			}{
				{CurveBlock{Start: 0, End: 4 * base, Min: 3*base + 21, Max: base + 19}, tc.wantParent},
				{CurveBlock{Start: 0, End: 2 * base, Min: 0, Max: base + 19}, true},
				{CurveBlock{Start: 2 * base, End: 4 * base, Min: 3*base + 21, Max: 2 * base}, tc.wantRightSubtree},
				{CurveBlock{Start: 3 * base, End: 4 * base, Min: 3*base + 21, Max: 3 * base}, true},
			} {
				index := slices.IndexFunc(blocks, func(b CurveBlock) bool {
					return b.Start == check.block.Start && b.End == check.block.End
				})
				if (index != -1) != check.want || (index != -1 && blocks[index] != check.block) {
					t.Fatalf("summary %+v present with exact extrema wanted=%t, got %v", check.block, check.want, blocks)
				}
			}
		})
	}
}

func TestCurveDeleteInvalidCandidatesAreAtomic(t *testing.T) {
	for _, test := range []struct {
		name  string
		index int
	}{
		{"negative index", -1},
		{"past end index", 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := &App{}
			if err := app.ApplyCurveProfiles([]config.CurveProfile{config.NewCurveProfile("A"), config.NewCurveProfile("B")}, 0); err != nil {
				t.Fatal(err)
			}
			if err := app.StartCurve(0); err != nil {
				t.Fatal(err)
			}
			receive := testCurveReceiver(app.curveReceiver())
			receive([]byte("VBAT=1000\nVBAT="), time.Unix(1, 0))
			beforeProfiles := config.CloneCurveProfiles(app.Settings.CurveProfiles)
			beforeValid := app.curve.activeValid
			settingsRevision, generation := app.SettingsRevision, app.curve.generation
			app.SettingsDirty = false
			var before, after CurveSnapshot
			app.CurveSnapshot(&before)
			if err := app.DeleteCurveProfile(test.index); err == nil {
				t.Fatal("接受了无效的删除候选")
			}
			app.CurveSnapshot(&after)
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(beforeProfiles, app.Settings.CurveProfiles) || beforeValid != app.curve.activeValid || app.Settings.CurveProfile != 0 || app.SettingsRevision != settingsRevision || app.SettingsDirty || !app.CurveRunning() || app.curve.generation != generation || app.curve.parser.lineSize != len("VBAT=") {
				t.Fatal("invalid deletion changed settings or parser state")
			}
			receive([]byte("2000\n"), time.Unix(2, 0))
			app.CurveSnapshot(&after)
			if after.Lines != 2 || after.LatestX != 2 || len(after.Series[0].Points) != 2 || after.Series[0].Points[1].Y != 2 {
				t.Fatalf("无效删除丢失了旧半行或阻止了采样: %+v", after)
			}
		})
	}
}

func TestCurveSnapshotLatestSuccessfulX(t *testing.T) {
	profile := config.NewCurveProfile("device")
	profile.TimePattern, profile.TimeScale = `t=([^ ]+)`, 1
	profile.Channels = append(profile.Channels, config.CurveChannel{Name: "current", Mode: "prefix", Pattern: "I=", Scale: 1, Enabled: true})
	app := &App{}
	if err := app.ApplyCurveProfiles([]config.CurveProfile{profile}, 0); err != nil {
		t.Fatal(err)
	}
	if err := app.StartCurve(0); err != nil {
		t.Fatal(err)
	}
	receive := testCurveReceiver(app.curveReceiver())
	var snapshot CurveSnapshot
	for _, test := range []struct {
		line   string
		x      float64
		counts [2]int
	}{
		{"t=10 VBAT=1000 I=1\n", 10, [2]int{1, 1}},
		{"t=20 I=2\n", 20, [2]int{1, 2}},
		{"t=5 VBAT=2000\n", 5, [2]int{2, 2}},
		{"t=8 I=3\n", 8, [2]int{2, 3}},
		{"t=99 VBAT=bad\n", 8, [2]int{2, 3}},
		{"t=100 unrelated\n", 8, [2]int{2, 3}},
		{"t=NaN VBAT=3000\n", 8, [2]int{2, 3}},
		{"t=0 VBAT=4000\n", 0, [2]int{3, 3}},
		{"t=-2 I=4\n", -2, [2]int{3, 4}},
	} {
		receive([]byte(test.line), time.Unix(123, 0))
		app.CurveSnapshot(&snapshot)
		if !snapshot.DeviceTime || snapshot.LatestX != test.x || len(snapshot.Series[0].Points) != test.counts[0] || len(snapshot.Series[1].Points) != test.counts[1] {
			t.Fatalf("最新X未反映最近成功采样的全局顺序, line=%q: %+v", test.line, snapshot)
		}
	}
	app.StopCurve()
	app.CurveSnapshot(&snapshot)
	if snapshot.LatestX != -2 {
		t.Fatalf("停止采样丢失了最新X: %+v", snapshot)
	}
	app.ClearCurve()
	app.CurveSnapshot(&snapshot)
	if snapshot.LatestX != 0 || !snapshot.DeviceTime || snapshot.Lines != 0 || len(snapshot.Series[0].Points) != 0 || len(snapshot.Series[1].Points) != 0 || app.curve.enabled.Load() {
		t.Fatalf("清空后快照未同步重置最新X或改变了停止状态: %+v", snapshot)
	}
}

func TestCurveLegacyProfilesRecoverByDeletion(t *testing.T) {
	profiles := make([]config.CurveProfile, 18)
	for i := range profiles {
		profiles[i] = config.NewCurveProfile("legacy")
	}
	profiles[0].TimePattern, profiles[1].TimePattern = "[", "["
	app := &App{Settings: config.Settings{CurveProfiles: profiles, CurveProfile: 2}}
	err := app.StartCurve(2)
	if err != nil {
		t.Fatalf("untouched invalid profiles or legacy count blocked a valid start: %v", err)
	}
	receive := testCurveReceiver(app.curveReceiver())
	receive([]byte("VBAT=1000\nVBAT="), time.Unix(1, 0))
	err = app.DeleteCurveProfile(0)
	if err != nil {
		t.Fatalf("18-to-17 recovery was rejected: %v", err)
	}
	if len(app.Settings.CurveProfiles) != 17 || app.Settings.CurveProfiles[0].TimePattern != "[" || app.CurveRunning() || app.curve.activeValid {
		t.Fatal("deletion did not retain the remaining invalid profile and stop acquisition")
	}
	var snapshot CurveSnapshot
	app.CurveSnapshot(&snapshot)
	if snapshot.Lines != 0 || len(snapshot.Series) != 0 || app.curve.parser.lineSize != 0 {
		t.Fatal("invalid replacement retained old acquisition data or a partial line")
	}
	err = app.StartCurve(0)
	if err == nil || app.CurveRunning() {
		t.Fatal("an invalid retained profile was reported as successful acquisition")
	}
	err = app.DeleteCurveProfile(0)
	if err != nil {
		t.Fatalf("17-to-16 recovery was rejected: %v", err)
	}
	if len(app.Settings.CurveProfiles) != 16 || app.CurveRunning() {
		t.Fatal("recovery truncated profiles or restarted stopped acquisition")
	}
	err = app.StartCurve(0)
	if err != nil {
		t.Fatal(err)
	}
	receive([]byte("2000\nVBAT=3000\n"), time.Unix(3, 0))
	app.CurveSnapshot(&snapshot)
	if !app.CurveRunning() || snapshot.Lines != 2 || len(snapshot.Series) != 1 || len(snapshot.Series[0].Points) != 1 || snapshot.Series[0].Points[0].Y != 3 {
		t.Fatal("recovered valid profile did not collect independently of the deleted partial record")
	}
	err = app.ApplyCurveProfiles(append(config.CloneCurveProfiles(app.Settings.CurveProfiles), config.NewCurveProfile("extra")), 0)
	if err == nil {
		t.Fatal("recovery removed the 16-profile addition limit")
	}
}

func TestCurveNewInvalidProfilesRemainAtomicBesideLegacy(t *testing.T) {
	valid, legacy := config.NewCurveProfile("active"), config.NewCurveProfile("legacy")
	legacy.TimePattern = "["
	app := &App{Settings: config.Settings{CurveProfiles: []config.CurveProfile{valid, legacy}}}
	err := app.StartCurve(0)
	if err != nil {
		t.Fatal(err)
	}
	receive := testCurveReceiver(app.curveReceiver())
	receive([]byte("VBAT=1000\nVBAT="), time.Unix(1, 0))
	var before, after CurveSnapshot
	app.CurveSnapshot(&before)
	profiles := config.CloneCurveProfiles(app.Settings.CurveProfiles)
	revision := app.SettingsRevision
	for _, duplicate := range []bool{false, true} {
		candidate := config.CloneCurveProfiles(profiles)
		if duplicate {
			candidate = append(candidate, legacy)
		} else {
			candidate[0].TimePattern = "["
		}
		err = app.ApplyCurveProfiles(candidate, 0)
		if err == nil {
			t.Fatal("new invalid edit or duplicated invalid legacy profile was accepted")
		}
		app.CurveSnapshot(&after)
		if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(profiles, app.Settings.CurveProfiles) || app.SettingsRevision != revision || !app.CurveRunning() || app.curve.parser.lineSize != len("VBAT=") {
			t.Fatal("rejected edit changed active data, settings, or partial input")
		}
	}
	receive([]byte("2000\n"), time.Unix(2, 0))
	app.CurveSnapshot(&after)
	if len(after.Series[0].Points) != 2 || after.Series[0].Points[1].Y != 2 {
		t.Fatal("rejected edits interrupted acquisition")
	}
}

func TestCurveDeleteInvalidLegacyRetainsValidRunningProfile(t *testing.T) {
	valid, legacy := config.NewCurveProfile("valid"), config.NewCurveProfile("legacy")
	legacy.TimePattern = "["
	app := &App{Settings: config.Settings{CurveProfiles: []config.CurveProfile{valid, legacy}}}
	err := app.StartCurve(0)
	if err != nil {
		t.Fatal(err)
	}
	err = app.DeleteCurveProfile(1)
	if err != nil {
		t.Fatal(err)
	}
	testCurveReceiver(app.curveReceiver())([]byte("VBAT=4000\n"), time.Unix(4, 0))
	var snapshot CurveSnapshot
	app.CurveSnapshot(&snapshot)
	if !app.CurveRunning() || len(snapshot.Series) != 1 || len(snapshot.Series[0].Points) != 1 || snapshot.Series[0].Points[0].Y != 4 {
		t.Fatal("deleting the invalid legacy profile prevented valid acquisition")
	}
}

func TestCurveRetainedNonfiniteLegacyValuesDoNotBlockRecovery(t *testing.T) {
	legacy, valid := config.NewCurveProfile("legacy"), config.NewCurveProfile("valid")
	legacy.TimeScale = math.NaN()
	legacy.Channels[0].Offset = math.NaN()
	app := &App{Settings: config.Settings{CurveProfiles: []config.CurveProfile{valid, legacy}}}
	err := app.StartCurve(0)
	if err != nil {
		t.Fatal(err)
	}
	profiles := config.CloneCurveProfiles(app.Settings.CurveProfiles)
	profiles[0].Name = "renamed"
	profiles[0].Channels[0].Color = "#D020A0"
	err = app.ApplyCurveProfiles(profiles, 0)
	if err != nil || !app.CurveRunning() || !math.IsNaN(app.Settings.CurveProfiles[1].TimeScale) || !math.IsNaN(app.Settings.CurveProfiles[1].Channels[0].Offset) {
		t.Fatalf("retained NaN values blocked an independent metadata edit: %v", err)
	}
	err = app.DeleteCurveProfile(0)
	if err != nil || app.CurveRunning() || app.CurveError() == nil || !math.IsNaN(app.Settings.CurveProfiles[0].TimeScale) || !math.IsNaN(app.Settings.CurveProfiles[0].Channels[0].Offset) {
		t.Fatalf("index-shifted NaN profile blocked recovery or retained false running state: %v", err)
	}
	profiles = config.CloneCurveProfiles(app.Settings.CurveProfiles)
	profiles[0].TimeScale, profiles[0].Channels[0].Offset = .001, 0
	err = app.ApplyCurveProfiles(profiles, 0)
	if err != nil || app.CurveError() != nil {
		t.Fatalf("nonfinite legacy profile could not be repaired: %v", err)
	}
	err = app.StartCurve(0)
	if err != nil || !app.CurveRunning() {
		t.Fatalf("repaired nonfinite legacy profile did not start: %v", err)
	}
}
