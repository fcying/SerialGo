package internal

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestCurveLiveSnapshotMatchesIndependentSnapshot(t *testing.T) {
	profile := curveRevisionProfile()
	profile.PointLimit = 17
	app := &App{}
	app.curve.parser = *testCurveParser(t, profile)
	var live CurveSnapshot
	var cache CurveLiveCache
	check := func(label string) {
		t.Helper()
		app.CurveLiveSnapshot(&live, &cache)
		var want CurveSnapshot
		app.CurveSnapshot(&want)
		if !reflect.DeepEqual(live, want) {
			t.Fatalf("%s: live snapshot differs from independent snapshot\ngot: %+v\nwant: %+v", label, live, want)
		}
	}
	feed := func(i int) {
		line := fmt.Sprintf("t=%d V=%d I=%d\n", i, i, i*10)
		if i%3 == 0 {
			line = fmt.Sprintf("t=%d V=%d\n", i, i)
		}
		app.curve.parser.feed([]byte(line), time.Time{})
	}
	check("empty")
	for i := 1; i <= 300; i++ {
		feed(i)
		if i%4 == 0 {
			check(fmt.Sprintf("append and wrap %d", i))
		}
	}
	// Exceed a whole ring between updates: the head alone is ambiguous.
	for i := 301; i <= 350; i++ {
		feed(i)
	}
	check("multiple wraps")
	app.curve.parser.setPointLimit(9)
	check("shrink")
	app.curve.parser.setPointLimit(0)
	for i := 351; i <= 370; i++ {
		feed(i)
	}
	check("unbounded growth")
	app.curve.parser.reset(app.curve.parser.compiled)
	app.curve.epoch.Add(1)
	feed(1)
	check("parser reset")
	live = CurveSnapshot{}
	check("replaced destination")
}

func TestCurveLiveSnapshotDoesNotShareParserStorage(t *testing.T) {
	profile := curveRevisionProfile()
	profile.PointLimit = 8
	app := &App{}
	app.curve.parser = *testCurveParser(t, profile)
	app.curve.parser.feed([]byte("t=1 V=10 I=100\n"), time.Time{})
	var snapshot CurveSnapshot
	var cache CurveLiveCache
	app.CurveLiveSnapshot(&snapshot, &cache)
	before := snapshot.Series[0].Points[0]
	app.curve.parser.feed([]byte("t=2 V=20 I=200\n"), time.Time{})
	if snapshot.Series[0].Points[0] != before || len(snapshot.Series[0].Points) != 1 {
		t.Fatal("parser mutated the frontend snapshot before publication")
	}
	app.CurveLiveSnapshot(&snapshot, &cache)
	if len(snapshot.Series[0].Points) != 2 || snapshot.Series[0].Points[1].Y != 20 {
		t.Fatalf("live snapshot missed appended sample: %+v", snapshot.Series[0].Points)
	}
}

func TestCurveLiveSnapshotConcurrentReceive(t *testing.T) {
	profile := curveRevisionProfile()
	profile.PointLimit = 128
	app := &App{}
	app.curve.parser = *testCurveParser(t, profile)
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		for i := 0; i < 1000; i++ {
			line := fmt.Sprintf("t=%d V=%d I=%d\n", i, i, i)
			app.curve.mu.Lock()
			app.curve.parser.feed([]byte(line), time.Time{})
			app.curve.mu.Unlock()
		}
	}()
	var snapshot CurveSnapshot
	var cache CurveLiveCache
	for i := 0; i < 200; i++ {
		app.CurveLiveSnapshot(&snapshot, &cache)
		for _, series := range snapshot.Series {
			for j := 1; j < len(series.Points); j++ {
				if series.Points[j].X <= series.Points[j-1].X {
					t.Fatalf("non-chronological snapshot: %+v", series.Points)
				}
			}
		}
	}
	workers.Wait()
}
