package track

import (
	"sort"

	"github.com/iryzhkov/86-challenge/vbo"
)

// LapResult represents a single detected lap.
type LapResult struct {
	LapNumber  int
	StartTime  float64 // seconds since midnight
	EndTime    float64
	LapTimeMs  int     // milliseconds
	SplitTimes []int   // split times in ms (time from lap start to each split crossing)
	StartIdx   int     // index in points array
	EndIdx     int
	IsOutlap   bool
	IsInlap    bool
	IsValid    bool
}

// SplitLaps takes parsed session data and split gates, then computes lap times.
// The first gate in splitGates should be the start/finish line.
func SplitLaps(points []vbo.DataPoint, gates []vbo.SplitGate) []LapResult {
	if len(points) < 2 || len(gates) == 0 {
		return nil
	}

	// Find start/finish gate
	var sfGate vbo.SplitGate
	var splitGates []vbo.SplitGate
	for _, g := range gates {
		if g.Type == "Start" {
			sfGate = g
		} else {
			splitGates = append(splitGates, g)
		}
	}
	if sfGate.Type == "" {
		return nil
	}

	// Find all start/finish crossings
	type crossing struct {
		time float64
		idx  int
	}
	var crossings []crossing
	for i := 0; i < len(points)-1; i++ {
		crossed, t := CrossesGate(points[i], points[i+1],
			sfGate.Lat1, sfGate.Lon1, sfGate.Lat2, sfGate.Lon2)
		if crossed {
			// Debounce: skip if too close to last crossing (< 10 seconds)
			if len(crossings) > 0 && t-crossings[len(crossings)-1].time < 10 {
				continue
			}
			crossings = append(crossings, crossing{time: t, idx: i})
		}
	}

	if len(crossings) < 2 {
		return nil
	}

	// Build laps from consecutive crossings
	var laps []LapResult
	for i := 0; i < len(crossings)-1; i++ {
		lapStart := crossings[i]
		lapEnd := crossings[i+1]
		lapTimeMs := int((lapEnd.time - lapStart.time) * 1000)

		lap := LapResult{
			LapNumber: i + 1,
			StartTime: lapStart.time,
			EndTime:   lapEnd.time,
			LapTimeMs: lapTimeMs,
			StartIdx:  lapStart.idx,
			EndIdx:    lapEnd.idx,
			IsValid:   true,
		}

		// Compute sector times (time between consecutive split gates)
		// First: get cumulative times to each split gate
		var cumSplits []int
		for _, sg := range splitGates {
			cumSplits = append(cumSplits, findSplitCrossing(points, lap.StartIdx, lap.EndIdx, sg, lapStart.time))
		}
		// Convert to sector times: S/F→Split1, Split1→Split2, ..., LastSplit→S/F
		prev := 0
		for _, cs := range cumSplits {
			if cs > 0 {
				lap.SplitTimes = append(lap.SplitTimes, cs-prev)
				prev = cs
			} else {
				lap.SplitTimes = append(lap.SplitTimes, 0)
			}
		}
		// Final sector: last split → finish
		if prev > 0 {
			lap.SplitTimes = append(lap.SplitTimes, lapTimeMs-prev)
		}

		laps = append(laps, lap)
	}

	// Detect invalid laps by comparing to median (including first/last lap check)
	detectInvalidLaps(laps)

	return laps
}

func findSplitCrossing(points []vbo.DataPoint, startIdx, endIdx int, gate vbo.SplitGate, lapStartTime float64) int {
	for i := startIdx; i < endIdx && i < len(points)-1; i++ {
		crossed, t := CrossesGate(points[i], points[i+1],
			gate.Lat1, gate.Lon1, gate.Lat2, gate.Lon2)
		if crossed {
			return int((t - lapStartTime) * 1000)
		}
	}
	return 0
}

func detectInvalidLaps(laps []LapResult) {
	if len(laps) < 3 {
		return
	}

	// Compute median lap time from all laps
	var allTimes []int
	for _, l := range laps {
		allTimes = append(allTimes, l.LapTimeMs)
	}
	if len(allTimes) < 2 {
		return
	}

	sort.Ints(allTimes)
	median := allTimes[len(allTimes)/2]

	for i := range laps {
		// Too slow = likely outlap, inlap, or incident
		if laps[i].LapTimeMs > median*3/2 {
			laps[i].IsValid = false
			if i == 0 {
				laps[i].IsOutlap = true
			} else if i == len(laps)-1 {
				laps[i].IsInlap = true
			}
		}
	}
}
