package internal

// CurveLiveCache holds frontend-owned buffers for incremental snapshots. The
// associated snapshot's point slices must be treated as read-only. Use
// CurveSnapshot for a caller-modifiable snapshot instead.
type CurveLiveCache struct {
	epoch, lines, revision uint64
	limit                  int
	series                 []curveLiveSeries
	valid                  bool
}

type curveLiveSeries struct {
	points      []CurvePoint
	start, head int
	count       int
}

// CurveLiveSnapshot refreshes an exclusively owned frontend snapshot. Its point
// slices are independent of the parser but must not be modified by the caller.
// The same cache must not be shared by multiple snapshots or goroutines.
func (s *App) CurveLiveSnapshot(dst *CurveSnapshot, cache *CurveLiveCache) {
	if dst == nil || cache == nil {
		return
	}
	s.curve.mu.Lock()
	defer s.curve.mu.Unlock()
	p := &s.curve.parser
	epoch := s.curve.epoch.Load()
	valid := cache.valid && cache.epoch == epoch && cache.limit == p.compiled.pointLimit &&
		cache.lines <= p.lines && cache.revision == dst.Revision && dst.Revision != 0 &&
		len(cache.series) == len(p.rings) && len(dst.Series) == len(p.rings)
	if valid {
		for i := range cache.series {
			entry := &cache.series[i]
			points := dst.Series[i].Points
			if len(points) != entry.count || (len(points) != 0 && &points[0] != &entry.points[entry.start]) {
				valid = false
				break
			}
		}
	}
	if !valid {
		cache.series = make([]curveLiveSeries, len(p.rings))
		dst.Revision = 0
	}
	cache.valid = valid
	p.snapshotWithCache(dst, cache)
	cache.epoch, cache.lines, cache.revision = epoch, p.lines, dst.Revision
	cache.limit, cache.valid = p.compiled.pointLimit, true
}

func (c *CurveLiveCache) copyPoints(index int, series *CurveSeries, ring *curveRing, lines uint64) {
	entry := &c.series[index]
	count := len(ring.points)
	added := count
	incremental := c.valid && lines >= c.lines && count >= entry.count
	if incremental && count > 0 {
		// At most one point per input line enters a given ring. If a full
		// revolution is possible, head positions cannot identify the delta.
		if lines-c.lines >= uint64(count) {
			incremental = false
		} else {
			evicted := ring.head - entry.head
			if evicted < 0 {
				evicted += count
			}
			added = count - entry.count + evicted
			if added < 0 || added > count || uint64(added) > lines-c.lines {
				incremental = false
			}
		}
	}
	if !incremental {
		added = count
		entry.start = 0
	}
	keep := count - added
	if incremental {
		entry.start += entry.count - keep
	}
	// Reserve a small tail so evictions advance a contiguous view without
	// moving the retained history on every frame. Compact only when it fills.
	if len(entry.points) < count {
		entry.points = make([]CurvePoint, count+max(16, count/8))
		entry.start = 0
		keep = 0
		added = count
	} else if entry.start+count > len(entry.points) {
		copy(entry.points, entry.points[entry.start:entry.start+keep])
		entry.start = 0
	}
	points := entry.points[entry.start : entry.start+count]
	ring.copyLatest(points[keep : keep+added])
	series.Points = points
	entry.head, entry.count = ring.head, count
}
