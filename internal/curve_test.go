package internal

import (
	"errors"
	"fmt"
	"math"
	"regexp/syntax"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal/i18n"
)

func testCurveParser(t *testing.T, profile config.CurveProfile) *curveParser {
	t.Helper()
	compiled, err := compileCurve(profile)
	if err != nil {
		t.Fatal(err)
	}
	parser := &curveParser{}
	parser.reset(compiled)
	return parser
}

func TestCurveArbitraryChunksAndConversions(t *testing.T) {
	profile := config.NewCurveProfile("firmware")
	profile.Channels = append(profile.Channels, config.CurveChannel{Name: "current", Mode: "regex", Pattern: `I=([^ ,]+)`, Scale: 2, Offset: 1, Unit: "A", Enabled: true})
	text := "boot\r\nVBAT= +3.3e3 I=-.5\r\nVBAT= 4000 I=2\nnot a voltage\n"
	for step := 1; step <= len(text); step++ {
		parser := testCurveParser(t, profile)
		for i := 0; i < len(text); i += step {
			parser.feed([]byte(text[i:min(i+step, len(text))]), time.Unix(1, 0))
		}
		var snapshot CurveSnapshot
		parser.snapshot(&snapshot)
		if snapshot.Lines != 4 || snapshot.Matched != 2 || snapshot.Invalid != 0 || len(snapshot.Series) != 2 {
			t.Fatalf("step %d: %+v", step, snapshot)
		}
		if points := snapshot.Series[0].Points; len(points) != 2 || math.Abs(points[0].Y-3.3) > 1e-12 || points[1].Y != 4 {
			t.Fatalf("step %d prefix: %+v", step, points)
		}
		if points := snapshot.Series[1].Points; len(points) != 2 || points[0].Y != 0 || points[1].Y != 5 {
			t.Fatalf("step %d regex: %+v", step, points)
		}
	}
}

func TestCurveInvalidSamples(t *testing.T) {
	profile := config.NewCurveProfile("numeric")
	parser := testCurveParser(t, profile)
	parser.feed([]byte("VBAT=3300\nVBAT=3400\nVBAT=1e999\nVBAT=1e\nVBAT=NaN\nVBAT=12garbage\nVBAT=.5\n"), time.Unix(1, 0))
	var snapshot CurveSnapshot
	parser.snapshot(&snapshot)
	points := snapshot.Series[0].Points
	if len(points) != 3 || points[0].X != 1 || points[1].X != 1 || points[0].Break || points[1].Break || points[2].Y != .0005 || !points[2].Break {
		t.Fatalf("invalid samples did not break the line: %+v", points)
	}
	if snapshot.Invalid != 4 || snapshot.Matched != 7 {
		t.Fatalf("invalid counters: %+v", snapshot)
	}
	profile.Channels[0].Scale = math.MaxFloat64
	preview, err := PreviewCurve(profile, "VBAT=2")
	if err != nil || preview.Invalid != 1 || len(preview.Series[0].Points) != 0 {
		t.Fatalf("overflow generated a sample: %+v %v", preview, err)
	}
}

func TestCurveReceiveWallClockTimestamps(t *testing.T) {
	app := &App{Settings: config.Settings{CurveProfiles: []config.CurveProfile{config.NewCurveProfile("host clock")}}}
	if err := app.StartCurve(0); err != nil {
		t.Fatal(err)
	}
	session := &Session{curve: newCurveWorker(app.curveReceiver())}
	at := time.Unix(1700000000, 123987654)
	session.receiveChunk([]byte("t=900000 VBAT=1000\nt=1 VBAT=2000\nVBAT="), at)
	session.receiveChunk([]byte("3000\nVBAT=4000\n"), at.Add(1250*time.Millisecond))
	close(session.curve.queue)
	<-session.curve.done
	var snapshot CurveSnapshot
	app.CurveSnapshot(&snapshot)
	points := snapshot.Series[0].Points
	if snapshot.DeviceTime || snapshot.Lines != 4 || snapshot.Matched != 4 || snapshot.Invalid != 0 || len(points) != 4 {
		t.Fatalf("receive framing: %+v", snapshot)
	}
	for i, wantX := range []float64{1700000000.123, 1700000000.123, 1700000001.373, 1700000001.373} {
		if points[i].X != wantX || points[i].Y != float64(i+1) || points[i].Break {
			t.Fatalf("record %d must use its completing receive batch's millisecond wall-clock time: %+v", i, points)
		}
	}
}

func TestCurveHostClockBackwardsBreaksAllSeries(t *testing.T) {
	profile := config.NewCurveProfile("host clock")
	profile.Channels = append(profile.Channels, config.CurveChannel{Name: "current", Mode: "prefix", Pattern: "I=", Scale: 1, Enabled: true})
	parser := testCurveParser(t, profile)
	at := time.Unix(1700000000, 123000000)
	parser.feed([]byte("VBAT=1000 I=1\nVBAT=2000 I=2\n"), at)
	parser.feed([]byte("VBAT=3000\n"), at.Add(-time.Second))
	parser.feed([]byte("VBAT=4000 I=3\n"), at.Add(time.Second))
	var snapshot CurveSnapshot
	parser.snapshot(&snapshot)
	voltage, current := snapshot.Series[0].Points, snapshot.Series[1].Points
	if len(voltage) != 4 || len(current) != 3 {
		t.Fatalf("missing clock-regression samples: %+v", snapshot)
	}
	for i, want := range []CurvePoint{
		{X: 1700000000.123, Y: 1},
		{X: 1700000000.123, Y: 2},
		{X: 1699999999.123, Y: 3, Break: true},
		{X: 1700000001.123, Y: 4},
	} {
		if voltage[i] != want {
			t.Fatalf("clock regression voltage sample %d: got %+v, want %+v", i, voltage[i], want)
		}
	}
	if current[0].Break || current[1].Break || current[2] != (CurvePoint{X: 1700000001.123, Y: 3, Break: true}) {
		t.Fatalf("clock regression must break even a channel absent in the regressing batch: %+v", current)
	}
}

func TestCurveOversizeCRLFAndWrapping(t *testing.T) {
	parser := testCurveParser(t, config.NewCurveProfile("bounded"))
	at := time.Unix(1, 0)
	parser.feed([]byte(strings.Repeat("x", curveLineLimit)+"\r"), at)
	parser.feed([]byte("\n"+strings.Repeat("x", curveLineLimit+1)+"VBAT=9000\nVBAT=1000\n"), at)
	var snapshot CurveSnapshot
	parser.snapshot(&snapshot)
	if snapshot.Lines != 3 || snapshot.Oversize != 1 || len(snapshot.Series[0].Points) != 1 {
		t.Fatalf("framing: %+v", snapshot)
	}
	for i := range config.DefaultCurvePointLimit + 1 {
		parser.feed([]byte(fmt.Sprintf("VBAT=%d\n", i)), at)
	}
	parser.snapshot(&snapshot)
	points := snapshot.Series[0].Points
	if len(points) != config.DefaultCurvePointLimit || points[0].Y != .001 || points[len(points)-1].Y != 10 {
		t.Fatalf("ring order/count: %d %+v %+v", len(points), points[0], points[len(points)-1])
	}
	backing := &snapshot.Series[0].Points[0]
	parser.snapshot(&snapshot)
	if backing != &snapshot.Series[0].Points[0] {
		t.Fatal("unchanged snapshot replaced backing memory")
	}
}

func TestCurveSessionBoundariesAndLogIndependence(t *testing.T) {
	app := &App{Settings: config.Settings{CurveProfiles: []config.CurveProfile{config.NewCurveProfile("device")}}}
	if err := app.StartCurve(0); err != nil {
		t.Fatal(err)
	}
	oldReceiver := app.curveReceiver()
	oldReceive := testCurveReceiver(oldReceiver)
	at := time.Unix(1700000000, 123987654)
	app.session = &Session{curve: newCurveWorker(oldReceiver)}
	app.session.receiveChunk([]byte("VBAT=1000\nVBAT="), at)
	app.session.enqueueDisplay(Event{Data: []byte(strings.Repeat("x", displayBufferSize))})
	app.session.enqueueDisplay(Event{Data: []byte("evicts queued display")})
	app.ClearReceiveLog()
	app.session.receiveChunk([]byte("2000\n"), at.Add(time.Second))
	app.session.emit(Event{Sent: true, Data: []byte("VBAT=9000\n")})
	close(app.session.curve.queue)
	<-app.session.curve.done
	app.curveDisconnected()
	oldReceive([]byte("VBAT=9999\n"), at)
	newReceive := testCurveReceiver(app.curveReceiver())
	newReceive([]byte("VBAT=3000\n"), at.Add(2*time.Second))
	var snapshot CurveSnapshot
	app.CurveSnapshot(&snapshot)
	points := snapshot.Series[0].Points
	if len(points) != 3 || points[0].X != 1700000000.123 || points[1].Y != 2 || points[1].X != 1700000001.123 || points[2].Y != 3 || points[2].X != 1700000002.123 || !points[2].Break {
		t.Fatalf("log clear or stale connection affected curve: %+v", points)
	}
	app.StopCurve()
	newReceive([]byte("VBAT=4000\n"), at)
	app.CurveSnapshot(&snapshot)
	if len(snapshot.Series[0].Points) != 3 {
		t.Fatal("stop did not preserve history")
	}
	app.ClearCurve()
	newReceive([]byte("VBAT=4000\n"), at)
	app.CurveSnapshot(&snapshot)
	if len(snapshot.Series[0].Points) != 0 || snapshot.Lines != 0 {
		t.Fatal("clear changed disabled state or retained counters")
	}
	if err := app.StartCurve(0); err != nil {
		t.Fatal(err)
	}
	app.ClearCurve()
	newReceive([]byte("VBAT=5000\n"), at)
	app.CurveSnapshot(&snapshot)
	if len(snapshot.Series[0].Points) != 1 {
		t.Fatal("clear changed enabled state")
	}
	app.CloseCurve()
	newReceive([]byte("VBAT=6000\n"), at)
	app.CurveSnapshot(&snapshot)
	if app.CurveRunning() || len(snapshot.Series) != 0 || snapshot.Lines != 0 || cap(app.curve.parser.rings) != 0 {
		t.Fatalf("close left acquisition or sample storage active: %+v", snapshot)
	}
	if err := app.StartCurve(0); err != nil {
		t.Fatal(err)
	}
	newReceive([]byte("VBAT=7000\n"), at)
	app.CurveSnapshot(&snapshot)
	points = snapshot.Series[0].Points
	if len(points) != 1 || points[0].Y != 7 || snapshot.Lines != 1 {
		t.Fatalf("restart on the same serial connection did not use the stored rule: %+v", snapshot)
	}
}

func TestCurveApplyAtomicPreviewAndRegexCauses(t *testing.T) {
	profiles := []config.CurveProfile{config.NewCurveProfile("device")}
	app := &App{}
	if err := app.ApplyCurveProfiles(profiles, 0); err != nil {
		t.Fatal(err)
	}
	profiles[0].Channels[0].Pattern = "OTHER="
	if app.Settings.CurveProfiles[0].Channels[0].Pattern != "VBAT=" || !app.SettingsDirty {
		t.Fatal("apply did not isolate settings")
	}
	if err := app.StartCurve(0); err != nil {
		t.Fatal(err)
	}
	receive := testCurveReceiver(app.curveReceiver())
	receive([]byte("VBAT=1000\nVBAT="), time.Unix(1, 0))
	revision := app.SettingsRevision
	parserRevision, partial := app.curve.parser.revision, app.curve.parser.lineSize
	bad := config.CloneCurveProfiles(app.Settings.CurveProfiles)
	bad[0].Channels[0].Mode, bad[0].Channels[0].Pattern = "regex", "["
	err := app.ApplyCurveProfiles(bad, 0)
	var cause *syntax.Error
	if !errors.As(err, &cause) || !strings.Contains(i18n.ErrorText(err, true), "Invalid") {
		t.Fatalf("regex error lost bilingual context or cause: %v", err)
	}
	if app.SettingsRevision != revision || app.Settings.CurveProfiles[0].Channels[0].Pattern != "VBAT=" || !app.curve.enabled.Load() || app.curve.parser.revision != parserRevision || app.curve.parser.lineSize != partial {
		t.Fatal("无效编辑修改了设置、运行状态或解析状态")
	}
	bad = config.CloneCurveProfiles(app.Settings.CurveProfiles)
	bad[0].Channels[0].Pattern = "OTHER="
	inactive := config.NewCurveProfile("invalid inactive")
	inactive.Channels[0].Mode, inactive.Channels[0].Pattern = "regex", "["
	bad = append(bad, inactive)
	err = app.ApplyCurveProfiles(bad, 0)
	if !errors.As(err, &cause) || app.SettingsRevision != revision || len(app.Settings.CurveProfiles) != 1 || app.Settings.CurveProfiles[0].Channels[0].Pattern != "VBAT=" || app.curve.parser.revision != parserRevision || app.curve.parser.lineSize != partial || !app.curve.enabled.Load() {
		t.Fatalf("非活动方案的无效通道正则未原子拒绝整次编辑: %v", err)
	}
	receive([]byte("2000\n"), time.Unix(2, 0))
	var snapshot CurveSnapshot
	app.CurveSnapshot(&snapshot)
	if len(snapshot.Series[0].Points) != 2 {
		t.Fatal("failed apply changed acquisition")
	}
	preview, err := PreviewCurve(app.Settings.CurveProfiles[0], "VBAT=3000\r\nVBAT=4000")
	if err != nil || preview.Lines != 2 || len(preview.Series[0].Points) != 2 || preview.Series[0].Points[1].X-preview.Series[0].Points[0].X != 1 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	if _, err := PreviewCurve(app.Settings.CurveProfiles[0], strings.Repeat("x", curvePreviewLimit+1)); err == nil {
		t.Fatal("unbounded preview accepted")
	}
	app.CurveSnapshot(&snapshot)
	if len(snapshot.Series[0].Points) != 2 || snapshot.Series[0].Points[0].Y != 1 {
		t.Fatal("preview modified app")
	}
}

func TestCurveApplyLiveSchema(t *testing.T) {
	for _, change := range []struct {
		name string
		edit func(*config.CurveProfile)
		line string
		x, y float64
	}{
		{"channel", func(p *config.CurveProfile) {
			p.Channels[0].Mode, p.Channels[0].Pattern = "regex", `I=([0-9]+)`
			p.Channels[0].Scale, p.Channels[0].Offset = 2, 1
		}, "I=3\n", 2, 7},
		{"time pattern", func(p *config.CurveProfile) {
			p.TimePattern = `t=([0-9]+)`
		}, "t=2500 VBAT=3000\n", 2.5, 3},
		{"time scale in system mode", func(p *config.CurveProfile) {
			p.TimeScale = 2
		}, "VBAT=3000\n", 2, 3},
	} {
		t.Run(change.name, func(t *testing.T) {
			app := &App{}
			if err := app.ApplyCurveProfiles([]config.CurveProfile{config.NewCurveProfile("device")}, 0); err != nil {
				t.Fatal(err)
			}
			if err := app.StartCurve(0); err != nil {
				t.Fatal(err)
			}
			receive := testCurveReceiver(app.curveReceiver())
			at := time.Unix(1, 0)
			receive([]byte("VBAT=1000\nVBAT=bad\nVBAT="), at)
			revision, generation := app.curve.parser.revision, app.curve.generation
			profiles := config.CloneCurveProfiles(app.Settings.CurveProfiles)
			change.edit(&profiles[0])
			if err := app.ApplyCurveProfiles(profiles, 0); err != nil {
				t.Fatal(err)
			}
			var snapshot CurveSnapshot
			app.CurveSnapshot(&snapshot)
			if snapshot.DeviceTime != (profiles[0].TimePattern != "") {
				t.Fatalf("快照时间模式未反映已编译规则: %+v", snapshot)
			}
			if !app.curve.enabled.Load() || app.curve.generation != generation || snapshot.Revision != revision+1 || snapshot.Lines != 0 || snapshot.Matched != 0 || snapshot.Invalid != 0 || snapshot.Oversize != 0 || len(snapshot.Series) != 1 || len(snapshot.Series[0].Points) != 0 || app.curve.parser.lineSize != 0 {
				t.Fatalf("规则更新未原子清空旧数据或改变了运行状态: %+v", snapshot)
			}
			// The already captured callback must use the replacement parser.
			receive([]byte("9000\n"+change.line), at.Add(time.Second))
			app.CurveSnapshot(&snapshot)
			points := snapshot.Series[0].Points
			if snapshot.Lines != 2 || snapshot.Matched != 1 || len(points) != 1 || points[0].X != change.x || points[0].Y != change.y || points[0].Break {
				t.Fatalf("旧回调未使用新规则或混入旧数据: %+v", snapshot)
			}
		})
	}
}

func TestCurveApplyMetadataInactiveAndNoOp(t *testing.T) {
	profiles := []config.CurveProfile{config.NewCurveProfile("active"), config.NewCurveProfile("inactive")}
	profiles[0].Channels[0].Mode, profiles[0].Channels[0].Pattern = "regex", `VBAT=([0-9]+)`
	app := &App{}
	if err := app.ApplyCurveProfiles(profiles, 0); err != nil {
		t.Fatal(err)
	}
	if err := app.StartCurve(0); err != nil {
		t.Fatal(err)
	}
	receive := testCurveReceiver(app.curveReceiver())
	at := time.Unix(1, 0)
	receive([]byte("VBAT=1000\nVBAT="), at)
	var snapshot CurveSnapshot
	app.CurveSnapshot(&snapshot)
	revision, settingsRevision := snapshot.Revision, app.SettingsRevision
	regex := app.curve.parser.compiled.rules[0].regex
	for _, edit := range []func([]config.CurveProfile){
		func(p []config.CurveProfile) { p[0].Name = "renamed" },
		func(p []config.CurveProfile) { p[1].Channels[0].Pattern = "OTHER=" },
	} {
		profiles = config.CloneCurveProfiles(app.Settings.CurveProfiles)
		edit(profiles)
		if err := app.ApplyCurveProfiles(profiles, 0); err != nil {
			t.Fatal(err)
		}
		settingsRevision++
		app.CurveSnapshot(&snapshot)
		if app.SettingsRevision != settingsRevision || !app.SettingsDirty || !app.curve.enabled.Load() || snapshot.Revision != revision || len(snapshot.Series[0].Points) != 1 || app.curve.parser.lineSize != len("VBAT=") || app.curve.parser.compiled.rules[0].regex != regex {
			t.Fatalf("方案重命名或非活动方案编辑影响了活动历史: %+v", snapshot)
		}
	}
	app.SettingsDirty = false
	if err := app.ApplyCurveProfiles(config.CloneCurveProfiles(app.Settings.CurveProfiles), 0); err != nil {
		t.Fatal(err)
	}
	if app.SettingsDirty || app.SettingsRevision != settingsRevision || app.curve.parser.revision != revision || app.curve.parser.compiled.rules[0].regex != regex {
		t.Fatal("未变更的设置触发了写入或解析更新")
	}
	receive([]byte("2000\n"), at.Add(time.Second))
	app.CurveSnapshot(&snapshot)
	if points := snapshot.Series[0].Points; len(points) != 2 || points[0].X != 1 || points[1].Y != 2 || points[1].X != 2 || points[1].Break {
		t.Fatalf("元数据编辑丢失了半行或接收时间: %+v", points)
	}
}

func TestCurveApplyActiveSelectionAndRemoval(t *testing.T) {
	app := &App{}
	profiles := []config.CurveProfile{config.NewCurveProfile("first"), config.NewCurveProfile("second")}
	if err := app.ApplyCurveProfiles(profiles, 0); err != nil {
		t.Fatal(err)
	}
	if err := app.StartCurve(0); err != nil {
		t.Fatal(err)
	}
	receive := testCurveReceiver(app.curveReceiver())
	at := time.Unix(1, 0)
	receive([]byte("VBAT=1000\nVBAT="), at)
	revision := app.curve.parser.revision
	if err := app.ApplyCurveProfiles(profiles, 1); err != nil {
		t.Fatal(err)
	}
	if !app.curve.enabled.Load() || app.curve.parser.revision != revision+1 || app.curve.parser.lineSize != 0 || app.curve.parser.lines != 0 {
		t.Fatal("活动索引切换未建立全新解析状态")
	}
	receive([]byte("VBAT=2000\nVBAT="), at)
	revision = app.curve.parser.revision
	if err := app.DeleteCurveProfile(0); err != nil {
		t.Fatal(err)
	}
	var snapshot CurveSnapshot
	app.CurveSnapshot(&snapshot)
	if !app.curve.enabled.Load() || snapshot.Revision != revision+1 || len(snapshot.Series[0].Points) != 0 || snapshot.Lines != 0 || app.curve.parser.lineSize != 0 {
		t.Fatalf("删除后的活动索引重排未清空旧状态: %+v", snapshot)
	}
	receive([]byte("VBAT=3000\n"), at)
	app.StopCurve()
	profiles = config.CloneCurveProfiles(app.Settings.CurveProfiles)
	profiles[0].Channels[0].Pattern = "CURRENT="
	if err := app.ApplyCurveProfiles(profiles, 0); err != nil {
		t.Fatal(err)
	}
	receive([]byte("CURRENT=4000\n"), at)
	app.CurveSnapshot(&snapshot)
	if app.curve.enabled.Load() || len(snapshot.Series[0].Points) != 0 || snapshot.Lines != 0 || app.curve.parser.compiled.rules[0].channel.Pattern != "CURRENT=" {
		t.Fatalf("停止时的规则编辑改变了停止状态或保留了旧历史: %+v", snapshot)
	}
	if err := app.StartCurve(0); err != nil {
		t.Fatal(err)
	}
	receive([]byte("CURRENT=5000\nCURRENT="), at)
	if err := app.DeleteCurveProfile(0); err != nil {
		t.Fatal(err)
	}
	receive([]byte("6000\nCURRENT=7000\n"), at)
	app.CurveSnapshot(&snapshot)
	if snapshot.DeviceTime || snapshot.LatestX != 0 || app.curve.enabled.Load() || len(app.Settings.CurveProfiles) != 0 || app.Settings.CurveProfile != 0 || len(snapshot.Series) != 0 || snapshot.Lines != 0 || snapshot.Matched != 0 || snapshot.Invalid != 0 || snapshot.Oversize != 0 || app.curve.parser.lineSize != 0 {
		t.Fatalf("删除最后一个方案未禁用并清空曲线: %+v", snapshot)
	}
	revision, settingsRevision := snapshot.Revision, app.SettingsRevision
	app.SettingsDirty = false
	if err := app.ApplyCurveProfiles([]config.CurveProfile{}, 0); err != nil {
		t.Fatal(err)
	}
	if app.SettingsDirty || app.SettingsRevision != settingsRevision || app.curve.parser.revision != revision {
		t.Fatal("重复提交空方案触发了变更")
	}
}

func TestCurvePreviewSystemReceiveSpanAndLineLimit(t *testing.T) {
	profile := config.NewCurveProfile("preview")
	line := "VBAT=1000" + strings.Repeat(" ", curveLineLimit-len("VBAT=1000"))
	before := float64(time.Now().UnixMilli()) / 1000
	snapshot, err := PreviewCurve(profile, line+"\r\n"+line+"x\nVBAT=2000")
	after := float64(time.Now().UnixMilli()) / 1000
	if err != nil {
		t.Fatal(err)
	}
	points := snapshot.Series[0].Points
	if snapshot.DeviceTime || snapshot.Lines != 3 || snapshot.Oversize != 1 || len(points) != 2 || points[1].X-points[0].X != 2 || points[1].X < before || points[1].X > after || !points[1].Break {
		t.Fatalf("preview framing and wall-clock span: %+v", snapshot)
	}
	for _, text := range []string{
		"t=4000 VBAT=1000\nt=6500 VBAT=2000",
		"t=4000 VBAT=1000\nt=6500 VBAT=2000\n",
	} {
		before = float64(time.Now().UnixMilli()) / 1000
		snapshot, err = PreviewCurve(profile, text)
		after = float64(time.Now().UnixMilli()) / 1000
		if err != nil {
			t.Fatal(err)
		}
		points = snapshot.Series[0].Points
		if snapshot.Lines != 2 || snapshot.Matched != 2 || snapshot.Invalid != 0 || len(points) != 2 || points[0].Y != 1 || points[1].Y != 2 || points[1].X-points[0].X != 1 || points[1].X < before || points[1].X > after || points[0].Break || points[1].Break {
			t.Fatalf("preview must use host time, not pasted timestamps, with identical final-line framing: %+v", snapshot)
		}
	}
}

func TestCurveConcurrentSnapshots(t *testing.T) {
	app := &App{Settings: config.Settings{CurveProfiles: []config.CurveProfile{config.NewCurveProfile("device")}}}
	if err := app.StartCurve(0); err != nil {
		t.Fatal(err)
	}
	receive := testCurveReceiver(app.curveReceiver())
	var done sync.WaitGroup
	done.Add(1)
	go func() {
		defer done.Done()
		for range 1000 {
			receive([]byte("VBAT=3300\n"), time.Unix(1, 0))
		}
	}()
	var snapshot CurveSnapshot
	for range 1000 {
		app.CurveSnapshot(&snapshot)
	}
	done.Wait()
	app.CurveSnapshot(&snapshot)
	if snapshot.Matched != 1000 || len(snapshot.Series[0].Points) != 1000 {
		t.Fatal("lost concurrent samples")
	}
}

func TestCurveSettingsSnapshotDeepCopies(t *testing.T) {
	app, _ := settingsSaveFixture(t, "unused", func(string, config.Settings) error { return nil })
	app.Settings.CurveProfiles = []config.CurveProfile{config.NewCurveProfile("device")}
	app.SettingsChanged()
	snapshot := app.usedSettingsSnapshot()
	app.Settings.CurveProfiles[0].Channels[0].Pattern = "CHANGED="
	app.Settings.CurveProfiles[0].Name = "changed"
	if snapshot == nil || snapshot.settings.CurveProfiles[0].Channels[0].Pattern != "VBAT=" || snapshot.settings.CurveProfiles[0].Name != "device" {
		t.Fatal("settings snapshot aliases mutable curve settings")
	}
}

func TestCurveDeviceTimeConversionAndBoundaries(t *testing.T) {
	profile := config.NewCurveProfile("设备时间")
	profile.TimePattern = `t=([^ ,]+)`
	profile.Channels = append(profile.Channels, config.CurveChannel{Name: "current", Mode: "prefix", Pattern: "I=", Scale: 1, Enabled: true})
	parser := testCurveParser(t, profile)
	at := time.Unix(1700000000, 123000000)
	parser.feed([]byte("t=4000 VBAT=1000 I=1\nt=6500 VBAT=2000 I=2\nt=500 VBAT=3000\nt=1000 VBAT=4000 I=3\n"), at)
	var snapshot CurveSnapshot
	parser.snapshot(&snapshot)
	if !snapshot.DeviceTime || snapshot.Invalid != 0 || snapshot.Matched != 4 {
		t.Fatalf("设备时间模式或计数错误: %+v", snapshot)
	}
	for i, want := range []CurvePoint{{X: 4, Y: 1}, {X: 6.5, Y: 2}, {X: .5, Y: 3, Break: true}, {X: 1, Y: 4}} {
		if points := snapshot.Series[0].Points; len(points) != 4 || points[i] != want {
			t.Fatalf("设备时间转换或回退分段错误: %+v", points)
		}
	}
	if points := snapshot.Series[1].Points; len(points) != 3 || points[2] != (CurvePoint{X: 1, Y: 3, Break: true}) {
		t.Fatalf("设备时间回退必须分隔所有通道: %+v", points)
	}
	parser.feed([]byte("t=9999 VBAT="), at)
	parser.boundary()
	parser.feed([]byte("t=-2000 VBAT=5000 I=4\n"), at.Add(-time.Hour))
	parser.snapshot(&snapshot)
	if points := snapshot.Series[0].Points; len(points) != 5 || points[4] != (CurvePoint{X: -2, Y: 5, Break: true}) {
		t.Fatalf("会话边界必须丢弃半行并保留绝对设备秒: %+v", points)
	}
	parser.reset(parser.compiled)
	parser.feed([]byte("t=9000 VBAT=6000\n"), at)
	parser.snapshot(&snapshot)
	if points := snapshot.Series[0].Points; !snapshot.DeviceTime || snapshot.Lines != 1 || len(points) != 1 || points[0] != (CurvePoint{X: 9, Y: 6}) {
		t.Fatalf("重置必须保留时间规则但清除历史: %+v", snapshot)
	}
	for _, text := range []string{"t=4000 VBAT=1000\nt=6500 VBAT=2000", "t=4000 VBAT=1000\nt=6500 VBAT=2000\n"} {
		preview, err := PreviewCurve(profile, text)
		if err != nil {
			t.Fatal(err)
		}
		if points := preview.Series[0].Points; !preview.DeviceTime || preview.LatestX != 6.5 || preview.Lines != 2 || len(points) != 2 || points[0].X != 4 || points[1].X != 6.5 || points[0].Break || points[1].Break {
			t.Fatalf("预览必须使用未归零的设备时间: %+v", preview)
		}
	}
}

func TestCurveInvalidDeviceTimestamps(t *testing.T) {
	for _, test := range []struct {
		name, pattern, line string
		scale               float64
	}{
		{"missing", `t=([^ ,]+)`, "VBAT=2000 I=2", .001},
		{"empty", `t=([^ ,]*)`, "t= VBAT=2000 I=2", .001},
		{"unmatched capture", `(?:t=([0-9]+))?`, "VBAT=2000 I=2", .001},
		{"NaN", `t=([^ ,]+)`, "t=NaN VBAT=2000 I=2", .001},
		{"infinity", `t=([^ ,]+)`, "t=Inf VBAT=2000 I=2", .001},
		{"overflow", `t=([^ ,]+)`, "t=1e999 VBAT=2000 I=2", .001},
		{"partial number", `t=([^ ,]+)`, "t=12garbage VBAT=2000 I=2", .001},
		{"incomplete exponent", `t=([^ ,]+)`, "t=1e VBAT=2000 I=2", .001},
		{"scaled overflow", `t=([^ ,]+)`, "t=2 VBAT=2000 I=2", math.MaxFloat64},
	} {
		t.Run(test.name, func(t *testing.T) {
			profile := config.NewCurveProfile("设备时间")
			profile.TimePattern, profile.TimeScale = test.pattern, test.scale
			profile.Channels = append(profile.Channels, config.CurveChannel{Name: "current", Mode: "prefix", Pattern: "I=", Scale: 1, Enabled: true})
			parser := testCurveParser(t, profile)
			at := time.Unix(1700000000, 0)
			parser.feed([]byte("t=0 VBAT=1000 I=1\n"+test.line+"\nt=1 VBAT=3000 I=3\n"), at)
			var snapshot CurveSnapshot
			parser.snapshot(&snapshot)
			if !snapshot.DeviceTime || snapshot.Invalid != 1 || snapshot.Matched != 3 {
				t.Fatalf("无效设备时间计数错误: %+v", snapshot)
			}
			for _, series := range snapshot.Series {
				if len(series.Points) != 2 || series.Points[0].X != 0 || series.Points[1].X != test.scale || series.Points[0].Break || !series.Points[1].Break {
					t.Fatalf("无效设备时间必须丢弃记录并分隔所有通道: %+v", series)
				}
			}
		})
	}
}

func TestCurveTimeRegexValidation(t *testing.T) {
	for _, pattern := range []string{"[", `t=[0-9]+`} {
		profile := config.NewCurveProfile("设备时间")
		profile.TimePattern = pattern
		if _, err := compileCurve(profile); err == nil {
			t.Fatalf("无效时间正则通过编译: %q", pattern)
		} else if pattern == "[" {
			var cause *syntax.Error
			if !errors.As(err, &cause) {
				t.Fatalf("时间正则错误丢失原因: %v", err)
			}
		}
		if _, err := PreviewCurve(profile, "t=1000 VBAT=1000"); err == nil {
			t.Fatalf("预览接受无效时间正则: %q", pattern)
		}
	}
}

func TestCurveDeviceTimeLiveChangesAndSnapshotMode(t *testing.T) {
	app := &App{}
	snapshot := CurveSnapshot{DeviceTime: true}
	app.CurveSnapshot(&snapshot)
	if snapshot.DeviceTime || len(snapshot.Series) != 0 {
		t.Fatalf("空解析规则必须使用系统时间模式: %+v", snapshot)
	}
	if err := app.ApplyCurveProfiles([]config.CurveProfile{config.NewCurveProfile("时间切换")}, 0); err != nil {
		t.Fatal(err)
	}
	if err := app.StartCurve(0); err != nil {
		t.Fatal(err)
	}
	receive := testCurveReceiver(app.curveReceiver())
	at := time.Unix(1700000000, 123000000)
	receive([]byte("VBAT=1000\nVBAT="), at)
	profiles := config.CloneCurveProfiles(app.Settings.CurveProfiles)
	profiles[0].TimePattern = "["
	if err := app.ApplyCurveProfiles(profiles, 0); err == nil {
		t.Fatal("无效设备时间规则未拒绝")
	}
	app.CurveSnapshot(&snapshot)
	if snapshot.DeviceTime || len(snapshot.Series[0].Points) != 1 || app.curve.parser.lineSize != len("VBAT=") {
		t.Fatalf("失败应用改变了系统时间解析模式: %+v", snapshot)
	}
	profiles[0].TimePattern = `t=([0-9]+)`
	if err := app.ApplyCurveProfiles(profiles, 0); err != nil {
		t.Fatal(err)
	}
	app.CurveSnapshot(&snapshot)
	if !snapshot.DeviceTime || snapshot.Lines != 0 || len(snapshot.Series[0].Points) != 0 || app.curve.parser.lineSize != 0 || !app.curve.enabled.Load() {
		t.Fatalf("启用设备时间未清空旧模式数据: %+v", snapshot)
	}
	receive([]byte("t=4000 VBAT=2000\n"), at)
	app.CurveSnapshot(&snapshot)
	if points := snapshot.Series[0].Points; len(points) != 1 || points[0].X != 4 {
		t.Fatalf("已有接收回调未使用设备时间: %+v", snapshot)
	}
	regex, revision := app.curve.parser.compiled.timeRegex, snapshot.Revision
	profiles[0].Name = "重命名"
	if err := app.ApplyCurveProfiles(profiles, 0); err != nil {
		t.Fatal(err)
	}
	if err := app.ApplyCurveProfiles(config.CloneCurveProfiles(profiles), 0); err != nil {
		t.Fatal(err)
	}
	if app.curve.parser.compiled.timeRegex != regex || app.curve.parser.revision != revision {
		t.Fatal("重命名或无变化应用重新编译了时间规则")
	}
	bad := config.CloneCurveProfiles(profiles)
	bad[0].TimePattern = ""
	bad[0].Channels[0].Mode, bad[0].Channels[0].Pattern = "regex", "["
	if err := app.ApplyCurveProfiles(bad, 0); err == nil {
		t.Fatal("无效通道规则未拒绝")
	}
	app.CurveSnapshot(&snapshot)
	if !snapshot.DeviceTime || snapshot.Revision != revision || app.curve.parser.compiled.timeRegex != regex {
		t.Fatalf("失败应用改变了设备时间解析模式: %+v", snapshot)
	}
	for _, change := range []struct {
		pattern string
		scale   float64
		line    string
		x       float64
	}{
		{`t=([0-9]+)`, .01, "t=4000 VBAT=3000\n", 40},
		{`tick=([0-9]+)`, .01, "tick=5000 VBAT=3000\n", 50},
		{"", .01, "tick=5000 VBAT=3000\n", 1700000000.123},
	} {
		profiles = config.CloneCurveProfiles(app.Settings.CurveProfiles)
		profiles[0].TimePattern, profiles[0].TimeScale = change.pattern, change.scale
		if err := app.ApplyCurveProfiles(profiles, 0); err != nil {
			t.Fatal(err)
		}
		app.CurveSnapshot(&snapshot)
		if snapshot.DeviceTime != (change.pattern != "") || snapshot.Lines != 0 || len(snapshot.Series[0].Points) != 0 || !app.curve.enabled.Load() {
			t.Fatalf("时间规则变化未重置解析状态: %+v", snapshot)
		}
		receive([]byte(change.line), at)
		app.CurveSnapshot(&snapshot)
		if points := snapshot.Series[0].Points; len(points) != 1 || points[0].X != change.x || points[0].Y != 3 || points[0].Break {
			t.Fatalf("已有回调未使用新时间规则: %+v", snapshot)
		}
	}
	profiles[0].TimePattern = `t=([0-9]+)`
	if err := app.ApplyCurveProfiles(profiles, 0); err != nil {
		t.Fatal(err)
	}
	app.CurveSnapshot(&snapshot)
	if !snapshot.DeviceTime {
		t.Fatal("清空前未恢复设备时间模式")
	}
	if err := app.ApplyCurveProfiles(nil, -1); err != nil {
		t.Fatal(err)
	}
	app.CurveSnapshot(&snapshot)
	if snapshot.DeviceTime || len(snapshot.Series) != 0 || app.curve.enabled.Load() {
		t.Fatalf("删除设备时间方案后空规则仍带设备时间标记: %+v", snapshot)
	}
}

func TestCurveSnapshotModeUsesCompiledSchema(t *testing.T) {
	for _, deviceTime := range []bool{false, true} {
		name := "system"
		if deviceTime {
			name = "device"
		}
		t.Run(name, func(t *testing.T) {
			profile := config.NewCurveProfile(name)
			if deviceTime {
				profile.TimePattern = `t=([0-9]+)`
			}
			app := &App{Settings: config.Settings{CurveProfiles: []config.CurveProfile{profile}}}
			if err := app.StartCurve(0); err != nil {
				t.Fatal(err)
			}
			receive := testCurveReceiver(app.curveReceiver())
			// Mutable settings may contain an invalid draft while acquisition keeps
			// using the last successfully compiled schema.
			app.Settings.CurveProfiles[0].TimePattern = "["
			if deviceTime {
				app.Settings.CurveProfiles[0].TimePattern = ""
			}
			app.Settings.CurveProfiles[0].TimeScale = math.NaN()
			receive([]byte("t=2500 VBAT=1000\n"), time.Unix(1700000000, 123000000))
			snapshot := CurveSnapshot{DeviceTime: !deviceTime}
			app.CurveSnapshot(&snapshot)
			wantX := 1700000000.123
			if deviceTime {
				wantX = 2.5
			}
			if points := snapshot.Series[0].Points; snapshot.DeviceTime != deviceTime || snapshot.Invalid != 0 || len(points) != 1 || points[0].X != wantX {
				t.Fatalf("snapshot used draft settings instead of the compiled schema: %+v", snapshot)
			}
			app.ClearCurve()
			app.CurveSnapshot(&snapshot)
			if snapshot.DeviceTime != deviceTime || snapshot.Lines != 0 || len(snapshot.Series[0].Points) != 0 || !app.curve.enabled.Load() {
				t.Fatalf("clear replaced the compiled time mode with draft settings: %+v", snapshot)
			}
		})
	}
}
