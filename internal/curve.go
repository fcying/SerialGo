package internal

import (
	"fmt"
	"math"
	"math/bits"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fcying/SerialGo/config"
	"github.com/fcying/SerialGo/internal/i18n"
)

const curveLineLimit = 4096
const curvePreviewLimit = 64 * 1024

// CurvePoint.X is UNIX receive time in millisecond precision, or configured device seconds.
type CurvePoint struct {
	X, Y  float64
	Break bool
}

// CurveBlock summarizes a continuous, time-ordered range of snapshot points.
// End is exclusive; Min and Max are point indices, not interpolated values.
type CurveBlock struct {
	Start, End, Min, Max int
}

const curveSummaryBlockSize = 256

type curveBlockCache struct {
	min, max     int // Physical indices.
	valid, clean bool
}

type CurveSeries struct {
	Name, Unit string
	Color      uint32
	Points     []CurvePoint
	// TimeRuns lists the starts of nondecreasing spans in Points. Empty means no index.
	TimeRuns []int
	// Blocks contains valid summaries of aligned groups of 256, 512, 1024, …
	// points, sorted by Start and then by decreasing End for the same Start.
	Blocks []CurveBlock
}

// CurveSnapshot owns its copied series. Revision changes only with plot data
// or metadata; StatsRevision lets callers refresh counters without recopying
// point storage. Each destination must have a single owner.
type CurveSnapshot struct {
	Series                            []CurveSeries
	Lines, Matched, Invalid, Oversize uint64
	DroppedBytes, DroppedChunks       uint64
	StatsRevision                     uint64
	Revision                          uint64
	LatestX                           float64
	DeviceTime                        bool
}

type curveRule struct {
	channel config.CurveChannel
	color   uint32
	regex   *regexp.Regexp
}

type compiledCurve struct {
	rules      []curveRule
	timeRegex  *regexp.Regexp
	timeScale  float64
	pointLimit int
}

// ValidateCurveProfile checks a complete edit without allocating preview state.
func ValidateCurveProfile(profile config.CurveProfile) error {
	_, err := compileCurve(profile)
	return err
}

func compileCurve(profile config.CurveProfile) (compiledCurve, error) {
	err := config.ValidateCurveProfiles([]config.CurveProfile{profile}, 0)
	if err != nil {
		return compiledCurve{}, err
	}
	compiled := compiledCurve{timeScale: profile.TimeScale, pointLimit: profile.PointLimit}
	compile := func(pattern, zh, en string) (*regexp.Regexp, error) {
		rule, err := regexp.Compile(pattern)
		if err != nil {
			return nil, i18n.WrapError(zh, en, err)
		}
		if rule.NumSubexp() < 1 {
			return nil, i18n.NewError(zh+": 需要第一个捕获组", en+": first capture group is required")
		}
		return rule, nil
	}
	if profile.TimePattern != "" {
		var err error
		compiled.timeRegex, err = compile(profile.TimePattern, "时间正则表达式无效", "Invalid time regex")
		if err != nil {
			return compiledCurve{}, err
		}
	}
	for i, channel := range profile.Channels {
		color := channel.Color
		if color == "" {
			color = config.DefaultCurveColor(i)
		}
		rgba, _ := config.ParseCurveColor(color)
		rule := curveRule{channel: channel, color: rgba}
		if channel.Mode == "regex" {
			var err error
			rule.regex, err = compile(channel.Pattern, "通道正则表达式无效: "+channel.Name, "Invalid channel regex: "+channel.Name)
			if err != nil {
				return compiledCurve{}, err
			}
		}
		if channel.Enabled {
			compiled.rules = append(compiled.rules, rule)
		}
	}
	return compiled, nil
}

type curveRing struct {
	points     []CurvePoint
	head       int
	segment    bool
	timeBreaks []int               // Physical indices of backwards jumps, oldest first.
	blocks     [][]curveBlockCache // Level 0 holds 256-point physical blocks; each higher level doubles the size.
	// Stage each record's value until its shared timestamp is validated.
	pendingValue float64
	pendingValid bool
}

func (r *curveRing) add(point CurvePoint, limit int) {
	point.Break = r.segment
	r.segment = false
	physical := len(r.points)
	if limit > 0 && physical == limit {
		physical = r.head
	}
	r.dirtyBlock(physical / curveSummaryBlockSize)
	n := len(r.points)
	if n != 0 {
		last := n - 1
		if r.head != 0 {
			last = r.head - 1
		}
		if point.X < r.points[last].X {
			start := n
			if n == limit {
				start = r.head
			}
			r.timeBreaks = append(r.timeBreaks, start)
		}
	}
	if limit == 0 {
		r.points = append(r.points, point)
		return
	}
	if len(r.points) < limit {
		if len(r.points) == cap(r.points) {
			points := make([]CurvePoint, len(r.points), min(limit, max(16, 2*len(r.points))))
			copy(points, r.points)
			r.points = points
		}
		r.points = append(r.points, point)
		return
	}
	r.points[r.head] = point
	r.head = (r.head + 1) % limit
	if len(r.timeBreaks) != 0 && r.timeBreaks[0] == r.head {
		if len(r.timeBreaks) == 1 {
			r.timeBreaks = r.timeBreaks[:0]
		} else {
			r.timeBreaks = r.timeBreaks[1:]
		}
	}
}

// copyLatest copies the newest len(dst) points in acquisition order.
func (r *curveRing) copyLatest(dst []CurvePoint) {
	if len(dst) == 0 {
		return
	}
	start := (r.head + len(r.points) - len(dst)) % len(r.points)
	first := copy(dst, r.points[start:])
	copy(dst[first:], r.points[:start])
}

// Dirtying stops at the first dirty node: a clean parent can only have clean
// children. Snapshot publication refreshes both children even if one is invalid.
func (r *curveRing) dirtyBlock(block int) {
	if len(r.blocks) == 0 || block == len(r.blocks[0]) {
		for len(r.blocks) < bits.Len(uint(block+1)) {
			r.blocks = append(r.blocks, nil)
		}
		for level := range r.blocks {
			index := block >> level
			if index == len(r.blocks[level]) {
				r.blocks[level] = append(r.blocks[level], curveBlockCache{})
			} else {
				r.blocks[level][index].clean = false
			}
		}
		return
	}
	for level := range r.blocks {
		cache := &r.blocks[level][block>>level]
		if !cache.clean {
			break
		}
		cache.clean = false
	}
}

func (r *curveRing) refreshBlock(first, level int) curveBlockCache {
	cache := &r.blocks[level][first>>level]
	if cache.clean {
		return *cache
	}
	cache.clean = true
	start := first * curveSummaryBlockSize
	if level == 0 {
		cache.min, cache.max, cache.valid = start, start, true
		for i := start; i < start+curveSummaryBlockSize; i++ {
			p := r.points[i]
			// A break on the first point is outside this block; the caller
			// always processes its incoming segment (or dot) separately.
			if !finiteCurve(p.X) || !finiteCurve(p.Y) || (i > start && (p.Break || p.X < r.points[i-1].X)) {
				cache.valid = false
				break
			}
			if p.Y < r.points[cache.min].Y {
				cache.min = i
			}
			if p.Y > r.points[cache.max].Y {
				cache.max = i
			}
		}
		return *cache
	}
	mid := first + (1 << (level - 1))
	left, right := r.refreshBlock(first, level-1), r.refreshBlock(mid, level-1)
	join := mid * curveSummaryBlockSize
	cache.valid = left.valid && right.valid && !r.points[join].Break && r.points[join].X >= r.points[join-1].X
	if cache.valid {
		cache.min, cache.max = left.min, left.max
		if r.points[right.min].Y < r.points[cache.min].Y {
			cache.min = right.min
		}
		if r.points[right.max].Y > r.points[cache.max].Y {
			cache.max = right.max
		}
	}
	return *cache
}

func (r *curveRing) appendBlockTree(dst []CurveBlock, first, level, offset int) []CurveBlock {
	cache := r.blocks[level][first>>level]
	if cache.valid {
		start := first*curveSummaryBlockSize + offset
		dst = append(dst, CurveBlock{
			Start: start, End: start + (curveSummaryBlockSize << level),
			Min: cache.min + offset, Max: cache.max + offset,
		})
	}
	if level > 0 {
		dst = r.appendBlockTree(dst, first, level-1, offset)
		dst = r.appendBlockTree(dst, first+(1<<(level-1)), level-1, offset)
	}
	return dst
}

// Copy complete, aligned trees in logical order. No summary spans the ring
// head or the physical end of the backing slice, including partial end blocks.
func (r *curveRing) copyBlocks(dst []CurveBlock) []CurveBlock {
	dst = dst[:0]
	addRange := func(lo, hi, offset int) {
		first, end := (lo+curveSummaryBlockSize-1)/curveSummaryBlockSize, hi/curveSummaryBlockSize
		for first < end {
			// Largest aligned tree that fits in this physical range.
			level := min(bits.Len(uint(end-first))-1, bits.TrailingZeros(uint(first)))
			r.refreshBlock(first, level)
			dst = r.appendBlockTree(dst, first, level, offset)
			first += 1 << level
		}
	}
	addRange(r.head, len(r.points), -r.head)
	addRange(0, r.head, len(r.points)-r.head)
	return dst
}

func (r *curveRing) logicalIndex(physical int) int {
	if physical < r.head {
		physical += len(r.points)
	}
	return physical - r.head
}

func (r *curveRing) resize(limit int) {
	if r.head == 0 && (limit == 0 || cap(r.points) <= limit) {
		return
	}
	count := len(r.points)
	if limit > 0 {
		count = min(count, limit)
	}
	points := make([]CurvePoint, count)
	r.copyLatest(points)
	breaks := r.timeBreaks[:0]
	removed := len(r.points) - count
	for _, physical := range r.timeBreaks {
		index := r.logicalIndex(physical) - removed
		if index > 0 {
			breaks = append(breaks, index)
		}
	}
	r.timeBreaks = breaks
	r.points, r.head = points, 0
	blockCount := (count + curveSummaryBlockSize - 1) / curveSummaryBlockSize
	r.blocks = make([][]curveBlockCache, bits.Len(uint(blockCount)))
	for level := range r.blocks {
		r.blocks[level] = make([]curveBlockCache, ((blockCount-1)>>level)+1)
	}
}

type curveParser struct {
	compiled                          compiledCurve
	rings                             []curveRing
	line                              [curveLineLimit + 1]byte
	lineSize                          int
	discard                           bool
	lastX                             float64
	haveX                             bool
	lines, matched, invalid, oversize uint64
	droppedBytes, droppedChunks       uint64
	statsRevision                     uint64
	revision                          uint64
}

func (p *curveParser) reset(compiled compiledCurve) {
	revision := p.revision + 1
	statsRevision := p.statsRevision + 1
	*p = curveParser{compiled: compiled, rings: make([]curveRing, len(compiled.rules)), revision: revision, statsRevision: statsRevision}
}

func (p *curveParser) setPointLimit(limit int) {
	for i := range p.rings {
		p.rings[i].resize(limit)
	}
	p.compiled.pointLimit = limit
	p.revision++
}

func (p *curveParser) boundary() {
	p.lineSize, p.discard = 0, false
	p.haveX = false
	for i := range p.rings {
		p.rings[i].segment = len(p.rings[i].points) > 0
	}
}

func (p *curveParser) feed(data []byte, at time.Time) {
	for _, b := range data {
		if b == '\n' {
			p.lines++
			p.statsRevision++
			if !p.discard {
				line := p.line[:p.lineSize]
				if len(line) > 0 && line[len(line)-1] == '\r' {
					line = line[:len(line)-1]
				}
				p.parseLine(string(line), at)
			}
			p.lineSize, p.discard = 0, false
			continue
		}
		if p.discard {
			continue
		}
		if p.lineSize == curveLineLimit+1 || p.lineSize == curveLineLimit && b != '\r' {
			p.discard, p.lineSize = true, 0
			p.oversize++
			p.statsRevision++
			for i := range p.rings {
				p.rings[i].segment = true
			}
			continue
		}
		p.line[p.lineSize] = b
		p.lineSize++
	}
}

func finiteCurve(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

// decimalEnd recognizes complete signed decimal/exponent tokens, never a partial exponent.
func decimalEnd(text string) int {
	i := 0
	if i < len(text) && (text[i] == '+' || text[i] == '-') {
		i++
	}
	digits := 0
	for i < len(text) && text[i] >= '0' && text[i] <= '9' {
		i++
		digits++
	}
	if i < len(text) && text[i] == '.' {
		i++
		for i < len(text) && text[i] >= '0' && text[i] <= '9' {
			i++
			digits++
		}
	}
	if digits == 0 {
		return 0
	}
	if i < len(text) && (text[i] == 'e' || text[i] == 'E') {
		i++
		if i < len(text) && (text[i] == '+' || text[i] == '-') {
			i++
		}
		start := i
		for i < len(text) && text[i] >= '0' && text[i] <= '9' {
			i++
		}
		if start == i {
			return 0
		}
	}
	// Reject adjacent word/number syntax rather than silently accepting a truncated value.
	if i < len(text) {
		b := text[i]
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '_' || b == '.' {
			return 0
		}
	}
	return i
}

func regexCapture(regex *regexp.Regexp, line string) (string, bool) {
	match := regex.FindStringSubmatchIndex(line)
	if match == nil {
		return "", false
	}
	if match[2] < 0 {
		return "", true
	}
	return line[match[2]:match[3]], true
}

func curveNumber(text string) (float64, bool) {
	if text == "" || decimalEnd(text) != len(text) {
		return 0, false
	}
	value, err := strconv.ParseFloat(text, 64)
	return value, err == nil && finiteCurve(value)
}

func (p *curveParser) parseLine(line string, at time.Time) {
	matched, anyValid := false, false
	for i := range p.compiled.rules {
		rule := &p.compiled.rules[i]
		ring := &p.rings[i]
		ring.pendingValid = false
		var text string
		var found bool
		if rule.regex != nil {
			text, found = regexCapture(rule.regex, line)
		} else if start := strings.Index(line, rule.channel.Pattern); start >= 0 {
			found = true
			text = strings.TrimLeft(line[start+len(rule.channel.Pattern):], " \t\v\f")
			text = text[:decimalEnd(text)]
		}
		if !found {
			continue
		}
		matched = true
		value, ok := curveNumber(text)
		value = value*rule.channel.Scale + rule.channel.Offset
		if !ok || !finiteCurve(value) {
			p.invalid++
			p.rings[i].segment = true
			continue
		}
		ring.pendingValue, ring.pendingValid = value, true
		anyValid = true
	}
	if !matched {
		return
	}
	p.matched++
	var x float64
	if p.compiled.timeRegex != nil {
		text, found := regexCapture(p.compiled.timeRegex, line)
		value, ok := curveNumber(text)
		x = value * p.compiled.timeScale
		if !found || !ok || !finiteCurve(x) {
			p.invalid++
			for i := range p.rings {
				p.rings[i].segment = true
			}
			return
		}
	} else {
		x = float64(at.UnixMilli()) / 1000
	}
	if !anyValid {
		return
	}
	if p.haveX && x < p.lastX {
		for i := range p.rings {
			p.rings[i].segment = true
		}
	}
	p.lastX, p.haveX = x, true
	p.revision++
	for i := range p.rings {
		if p.rings[i].pendingValid {
			p.rings[i].add(CurvePoint{X: x, Y: p.rings[i].pendingValue}, p.compiled.pointLimit)
		}
	}
}

func (p *curveParser) snapshot(dst *CurveSnapshot) {
	p.snapshotWithCache(dst, nil)
}

func (p *curveParser) snapshotWithCache(dst *CurveSnapshot, cache *CurveLiveCache) {
	if p.revision == 0 {
		p.revision = 1
	}
	dst.Lines, dst.Matched, dst.Invalid, dst.Oversize = p.lines, p.matched, p.invalid, p.oversize
	dst.DroppedBytes, dst.DroppedChunks = p.droppedBytes, p.droppedChunks
	dst.StatsRevision = p.statsRevision
	if dst.Revision == p.revision {
		return
	}
	if cap(dst.Series) < len(p.rings) {
		dst.Series = make([]CurveSeries, len(p.rings))
	} else {
		dst.Series = dst.Series[:len(p.rings)]
	}
	for i := range p.rings {
		ring := &p.rings[i]
		series := &dst.Series[i]
		series.Name, series.Unit = p.compiled.rules[i].channel.Name, p.compiled.rules[i].channel.Unit
		series.Color = p.compiled.rules[i].color
		count := len(ring.points)
		if cache != nil {
			cache.copyPoints(i, series, ring, p.lines)
		} else {
			if cap(series.Points) < count || (p.compiled.pointLimit > 0 && cap(series.Points) > p.compiled.pointLimit) {
				series.Points = make([]CurvePoint, count, cap(ring.points))
			} else {
				series.Points = series.Points[:count]
			}
			ring.copyLatest(series.Points)
		}
		series.Blocks = ring.copyBlocks(series.Blocks)
		series.TimeRuns = series.TimeRuns[:0]
		if count != 0 {
			series.TimeRuns = append(slices.Grow(series.TimeRuns, 1+len(ring.timeBreaks)), 0)
			for _, physical := range ring.timeBreaks {
				series.TimeRuns = append(series.TimeRuns, ring.logicalIndex(physical))
			}
		}
	}
	dst.LatestX = p.lastX
	dst.DeviceTime = p.compiled.timeRegex != nil
	dst.Revision = p.revision
}

type curveEngine struct {
	mu            sync.Mutex
	enabled       atomic.Bool
	generation    uint64
	epoch         atomic.Uint64
	parser        curveParser
	activeValid   bool                // Owned by the UI producer; true only after successful compilation.
	activeError   error               // Compilation failure of a retained active legacy profile.
	activeProfile config.CurveProfile // Independent of mutable settings, used only to compare parser schemas.
}

// CurveSnapshot may run concurrently with receive; dst belongs exclusively to its caller.
func (s *App) CurveSnapshot(dst *CurveSnapshot) {
	if dst == nil {
		return
	}
	s.curve.mu.Lock()
	defer s.curve.mu.Unlock()
	s.curve.parser.snapshot(dst)
}

func (s *App) ApplyCurveProfiles(profiles []config.CurveProfile, active int) error {
	return s.applyCurveProfiles(profiles, active, false)
}

func (s *App) DeleteCurveProfile(index int) error {
	if index < 0 || index >= len(s.Settings.CurveProfiles) {
		return i18n.NewError("曲线方案索引无效", "Invalid curve profile index")
	}
	profiles := config.CloneCurveProfiles(s.Settings.CurveProfiles)
	profiles = slices.Delete(profiles, index, index+1)
	active := max(0, min(index, len(profiles)-1))
	return s.applyCurveProfiles(profiles, active, true)
}

// sameStoredCurve compares full stored values, including non-finite legacy values.
func sameStoredCurve(a, b config.CurveProfile) bool {
	if a.Name != b.Name || a.TimePattern != b.TimePattern || math.Float64bits(a.TimeScale) != math.Float64bits(b.TimeScale) || a.PointLimit != b.PointLimit || len(a.Channels) != len(b.Channels) {
		return false
	}
	for i, channel := range a.Channels {
		other := b.Channels[i]
		if math.Float64bits(channel.Scale) != math.Float64bits(other.Scale) || math.Float64bits(channel.Offset) != math.Float64bits(other.Offset) {
			return false
		}
		channel.Scale, other.Scale, channel.Offset, other.Offset = 0, 0, 0, 0
		if channel != other {
			return false
		}
	}
	return true
}

func (s *App) applyCurveProfiles(profiles []config.CurveProfile, active int, forceReset bool) error {
	previous := s.Settings.CurveProfiles
	if len(profiles) > max(config.CurveProfileLimit, len(previous)) {
		return i18n.NewError("曲线方案最多支持 16 个", "At most 16 curve profiles are supported")
	}
	if (len(profiles) == 0 && active != 0 && active != -1) || (len(profiles) > 0 && (active < 0 || active >= len(profiles))) {
		return i18n.NewError("曲线方案索引无效", "Invalid curve profile index")
	}
	unchanged := slices.EqualFunc(previous, profiles, sameStoredCurve) && s.Settings.CurveProfile == active
	if unchanged && !forceReset && len(profiles) == 0 {
		return nil
	}
	// A metadata edit before the first parser exists must not create plot series.
	uninitializedMetadata := !forceReset && !s.curve.activeValid && s.curve.activeError == nil && len(profiles) > 0 && s.Settings.CurveProfile == active && active < len(previous) && config.SameCurveSchema(previous[active], profiles[active])
	reset := forceReset || !s.curve.activeValid || s.Settings.CurveProfile != active || s.Settings.CurveProfile < 0 || s.Settings.CurveProfile >= len(previous) || len(profiles) == 0
	if !reset {
		reset = !config.SameCurveSchema(s.curve.activeProfile, profiles[active])
	}
	if unchanged && !forceReset && !reset {
		return nil
	}
	// Match retained values rather than indices: deletion shifts legacy profiles.
	// Consume each match so duplicating an invalid legacy profile is still rejected.
	retained := make([]bool, len(previous))
	var compiled compiledCurve
	activeCompiled := false
	var activeError error
	for i, profile := range profiles {
		oldIndex := -1
		for j, old := range previous {
			if !retained[j] && sameStoredCurve(old, profile) {
				oldIndex = j
				break
			}
		}
		if oldIndex >= 0 {
			retained[oldIndex] = true
			continue
		}
		if i == active && !reset {
			err := config.ValidateCurveProfiles(profiles[i:i+1], 0)
			if err != nil {
				return i18n.WrapError(fmt.Sprintf("曲线方案 %d 无效", i+1), fmt.Sprintf("Invalid curve profile %d", i+1), err)
			}
			continue
		}
		candidate, err := compileCurve(profile)
		if err != nil {
			return i18n.WrapError(fmt.Sprintf("曲线方案 %d 无效", i+1), fmt.Sprintf("Invalid curve profile %d", i+1), err)
		}
		if i == active {
			compiled, activeCompiled = candidate, true
		}
	}
	if reset && len(profiles) > 0 && !activeCompiled {
		compiled, activeError = compileCurve(profiles[active])
		activeCompiled = activeError == nil
	}
	if uninitializedMetadata && activeCompiled {
		reset = false
		if unchanged {
			return nil
		}
	}
	cloned := config.CloneCurveProfiles(profiles)
	s.curve.mu.Lock()
	if reset {
		s.curve.parser.reset(compiled)
		s.curve.epoch.Add(1)
		s.curve.activeValid = activeCompiled
		s.curve.activeError = activeError
		s.curve.activeProfile = config.CurveProfile{}
		if activeCompiled {
			s.curve.activeProfile = profiles[active]
			s.curve.activeProfile.Channels = slices.Clone(profiles[active].Channels)
		}
	} else if active >= 0 && active < len(profiles) && !slices.Equal(previous[active].Channels, profiles[active].Channels) {
		ruleIndex := 0
		for i, channel := range profiles[active].Channels {
			if !channel.Enabled {
				continue
			}
			if ruleIndex >= len(s.curve.parser.compiled.rules) {
				break
			}
			color := channel.Color
			if color == "" {
				color = config.DefaultCurveColor(i)
			}
			rule := &s.curve.parser.compiled.rules[ruleIndex]
			rule.channel.Color = channel.Color
			rule.color, _ = config.ParseCurveColor(color)
			ruleIndex++
		}
		s.curve.parser.revision++
	}
	if !reset && s.curve.activeValid && s.curve.parser.compiled.pointLimit != profiles[active].PointLimit {
		s.curve.parser.setPointLimit(profiles[active].PointLimit)
	}
	if len(profiles) == 0 || !s.curve.activeValid {
		s.curve.enabled.Store(false)
	}
	s.Settings.CurveProfiles = cloned
	s.Settings.CurveProfile = active
	s.curve.mu.Unlock()
	s.SettingsChanged()
	s.SaveUsedSettings()
	return nil
}

func (s *App) StartCurve(index int) error {
	if index < 0 || index >= len(s.Settings.CurveProfiles) {
		return i18n.NewError("请选择有效的曲线方案", "Select a valid curve profile")
	}
	compiled, err := compileCurve(s.Settings.CurveProfiles[index])
	if err != nil {
		return err
	}
	s.curve.mu.Lock()
	s.curve.parser.reset(compiled)
	s.curve.epoch.Add(1)
	s.curve.activeValid = true
	s.curve.activeError = nil
	s.curve.activeProfile = s.Settings.CurveProfiles[index]
	s.curve.activeProfile.Channels = slices.Clone(s.curve.activeProfile.Channels)
	s.curve.enabled.Store(true)
	s.curve.mu.Unlock()
	if s.Settings.CurveProfile != index {
		s.Settings.CurveProfile = index
		s.SettingsChanged()
		s.SaveUsedSettings()
	}
	return nil
}

// CurveRunning reports actual acquisition state independently of UI selection.
func (s *App) CurveRunning() bool {
	return s.curve.enabled.Load()
}

// CurveError reports the retained active profile's error after applying selection.
func (s *App) CurveError() error {
	s.curve.mu.Lock()
	defer s.curve.mu.Unlock()
	return s.curve.activeError
}

func (s *App) StopCurve() {
	s.curve.mu.Lock()
	s.curve.enabled.Store(false)
	s.curve.epoch.Add(1)
	s.curve.parser.boundary()
	s.curve.mu.Unlock()
}

// CloseCurve stops acquisition and releases the parser without changing saved rules.
func (s *App) CloseCurve() {
	s.curve.mu.Lock()
	s.curve.enabled.Store(false)
	s.curve.epoch.Add(1)
	s.curve.parser.reset(compiledCurve{})
	s.curve.activeValid = false
	s.curve.activeError = nil
	s.curve.activeProfile = config.CurveProfile{}
	s.curve.mu.Unlock()
}

func (s *App) ClearCurve() {
	s.curve.mu.Lock()
	s.curve.parser.reset(s.curve.parser.compiled)
	s.curve.epoch.Add(1)
	s.curve.mu.Unlock()
}

// Binding rejects obsolete sessions; the epoch rejects queued input from an
// earlier start, clear or schema. Only the session's curve worker calls receive.
func (s *App) curveReceiver() *curveReceiver {
	s.curve.mu.Lock()
	s.curve.generation++
	generation := s.curve.generation
	s.curve.epoch.Add(1)
	s.curve.parser.boundary()
	s.curve.mu.Unlock()
	return &curveReceiver{
		enabled: &s.curve.enabled,
		epoch:   &s.curve.epoch,
		receive: func(chunk curveChunk) {
			if !s.curve.enabled.Load() {
				return
			}
			s.curve.mu.Lock()
			defer s.curve.mu.Unlock()
			if generation != s.curve.generation || chunk.epoch != s.curve.epoch.Load() || !s.curve.enabled.Load() {
				return
			}
			if chunk.droppedChunks != 0 {
				s.curve.parser.boundary()
				s.curve.parser.discard = chunk.discard
				s.curve.parser.droppedBytes += chunk.droppedBytes
				s.curve.parser.droppedChunks += chunk.droppedChunks
				s.curve.parser.statsRevision++
			}
			s.curve.parser.feed(chunk.data, chunk.at)
		},
	}
}

func (s *App) curveDisconnected() {
	s.curve.mu.Lock()
	s.curve.generation++
	s.curve.epoch.Add(1)
	s.curve.parser.boundary()
	s.curve.mu.Unlock()
}

// PreviewCurve uses device seconds when configured, otherwise synthetic receive
// times one second apart per record, ending at the current host wall-clock time.
// The final unterminated record is completed without relaxing the live line limit.
func PreviewCurve(profile config.CurveProfile, text string) (CurveSnapshot, error) {
	if len(text) > curvePreviewLimit {
		return CurveSnapshot{}, i18n.NewError("预览文本最多支持 64 KiB", "Preview text is limited to 64 KiB")
	}
	compiled, err := compileCurve(profile)
	if err != nil {
		return CurveSnapshot{}, err
	}
	var parser curveParser
	parser.reset(compiled)
	var at time.Time
	if compiled.timeRegex == nil {
		records := strings.Count(text, "\n")
		if len(text) > 0 && text[len(text)-1] != '\n' {
			records++
		}
		at = time.Now()
		if records > 1 {
			at = at.Add(-time.Duration(records-1) * time.Second)
		}
	}
	for start := 0; start < len(text); {
		next := strings.IndexByte(text[start:], '\n')
		if next < 0 {
			parser.feed([]byte(text[start:]), at)
			parser.feed([]byte{'\n'}, at)
			break
		}
		end := start + next + 1
		parser.feed([]byte(text[start:end]), at)
		start = end
		if compiled.timeRegex == nil {
			at = at.Add(time.Second)
		}
	}
	var snapshot CurveSnapshot
	parser.snapshot(&snapshot)
	return snapshot, nil
}
