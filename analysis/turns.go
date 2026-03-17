package analysis

import (
	"fmt"
	"math"
)

// KnownTurn is a named turn apex on a specific track.
type KnownTurn struct {
	Num  float64
	Name string
	Lat  float64
	Lon  float64
}

// TurnAnalysis holds the comparison between actual and simulated driving at a turn.
type TurnAnalysis struct {
	Name          string  `json:"name"`
	EntrySpeedMph float64 `json:"entry_speed_mph"`
	ApexSpeedMph  float64 `json:"apex_speed_mph"`
	ExitSpeedMph  float64 `json:"exit_speed_mph"`

	SimEntryMph float64 `json:"sim_entry_mph"`
	SimApexMph  float64 `json:"sim_apex_mph"`
	SimExitMph  float64 `json:"sim_exit_mph"`

	Advice string `json:"advice"`
}

// detectedTurn is an intermediate turn detection result.
type detectedTurn struct {
	entryIdx, apexIdx, exitIdx, brakeIdx int
	entryDist, apexDist, exitDist, brakeDist float64
	entrySpeed, apexSpeed, exitSpeed float64
	apexLat, apexLon float64
}

// AnalyzeTurns detects turns from the actual speed profile and compares
// actual vs simulated driving to generate actionable advice.
func AnalyzeTurns(track []TrackPoint, simSpeeds []float64, trackName, trackConfig string) []TurnAnalysis {
	if len(track) < 50 || len(simSpeeds) != len(track) {
		return nil
	}

	n := len(track)
	kmhToMph := 0.621371

	// Smooth actual speeds and brake for turn detection
	actualSpeeds := make([]float64, n)
	brakes := make([]float64, n)
	for i, tp := range track {
		actualSpeeds[i] = tp.SpeedMs * 3.6 // km/h
		brakes[i] = tp.BrakePct
	}
	smoothSpeed := smoothArray(actualSpeeds, 3)
	smoothBrake := smoothArray(brakes, 3)

	// Detect turns from brake zones in actual data
	var turns []detectedTurn
	inZone := false
	zoneStart := 0

	for i := 0; i < n; i++ {
		if !inZone && smoothBrake[i] > 1 {
			inZone = true
			zoneStart = i
		}
		if inZone && smoothBrake[i] <= 1 {
			inZone = false

			// Find apex (min speed) in and shortly after zone
			searchEnd := i + 15
			if searchEnd >= n {
				searchEnd = n - 1
			}
			minSpd := math.Inf(1)
			minIdx := zoneStart
			for j := zoneStart; j <= searchEnd; j++ {
				if smoothSpeed[j] < minSpd {
					minSpd = smoothSpeed[j]
					minIdx = j
				}
			}

			// Entry = highest speed before zone start
			entryIdx := zoneStart
			for j := zoneStart - 1; j >= max(0, zoneStart-30); j-- {
				if smoothSpeed[j] >= smoothSpeed[entryIdx] {
					entryIdx = j
				} else {
					break
				}
			}

			// Exit = speed recovery after apex
			exitIdx := minIdx
			for j := minIdx + 1; j < min(n, minIdx+80); j++ {
				if smoothBrake[j] > 1 && j > minIdx+3 {
					break
				}
				if smoothSpeed[j] > smoothSpeed[exitIdx] {
					exitIdx = j
				}
			}

			speedDrop := smoothSpeed[entryIdx] - minSpd
			if speedDrop < 3 { // skip tiny variations
				continue
			}

			// Find actual brake point
			bpIdx := zoneStart
			for j := zoneStart; j <= minIdx; j++ {
				if smoothBrake[j] > 2 {
					bpIdx = j
					break
				}
			}

			turns = append(turns, detectedTurn{
				entryIdx: entryIdx, apexIdx: minIdx, exitIdx: exitIdx, brakeIdx: bpIdx,
				entryDist: track[entryIdx].Distance, apexDist: track[minIdx].Distance,
				exitDist: track[exitIdx].Distance, brakeDist: track[bpIdx].Distance,
				entrySpeed: smoothSpeed[entryIdx], apexSpeed: minSpd, exitSpeed: smoothSpeed[exitIdx],
				apexLat: track[minIdx].Lat, apexLon: track[minIdx].Lon,
			})
		}
	}

	// Second pass: catch lift-off corners (no brake, but lateral G + speed dip)
	smoothLatG := make([]float64, n)
	for i, tp := range track {
		smoothLatG[i] = math.Abs(tp.LatG)
	}
	smoothLatG = smoothArray(smoothLatG, 3)

	for i := 5; i < n-5; i++ {
		spd := smoothSpeed[i]
		if spd < smoothSpeed[i-3]-3 && spd < smoothSpeed[i+3]-3 && smoothLatG[i] > 0.4 {
			dist := track[i].Distance
			covered := false
			for _, t := range turns {
				if dist >= t.entryDist-20 && dist <= t.exitDist+20 {
					covered = true
					break
				}
			}
			if covered {
				continue
			}

			entryIdx := i
			for j := i - 1; j >= max(0, i-30); j-- {
				if smoothSpeed[j] >= smoothSpeed[entryIdx] {
					entryIdx = j
				} else {
					break
				}
			}
			exitIdx := i
			for j := i + 1; j < min(n, i+80); j++ {
				if smoothSpeed[j] > smoothSpeed[exitIdx] {
					exitIdx = j
				}
				if j > i+5 && smoothSpeed[j] < smoothSpeed[j-1]-3 {
					break
				}
			}
			if smoothSpeed[entryIdx]-spd < 3 {
				continue
			}

			turns = append(turns, detectedTurn{
				entryIdx: entryIdx, apexIdx: i, exitIdx: exitIdx, brakeIdx: i,
				entryDist: track[entryIdx].Distance, apexDist: track[i].Distance,
				exitDist: track[exitIdx].Distance, brakeDist: track[i].Distance,
				entrySpeed: smoothSpeed[entryIdx], apexSpeed: spd, exitSpeed: smoothSpeed[exitIdx],
				apexLat: track[i].Lat, apexLon: track[i].Lon,
			})
		}
	}

	// Sort turns by distance
	for i := 1; i < len(turns); i++ {
		for j := i; j > 0 && turns[j].apexDist < turns[j-1].apexDist; j-- {
			turns[j], turns[j-1] = turns[j-1], turns[j]
		}
	}

	// Match to known turns
	knownTurns := getKnownTurns(trackName, trackConfig)
	labeledTurns := labelTurns(turns, knownTurns)

	// Build sim speed profile (smoothed same way)
	simSpeedsKmh := make([]float64, n)
	for i := range simSpeeds {
		simSpeedsKmh[i] = simSpeeds[i] * 3.6
	}
	smoothSim := smoothArray(simSpeedsKmh, 3)

	// For each turn, extract sim metrics at the same indices and generate advice
	var results []TurnAnalysis
	for _, lt := range labeledTurns {
		t := lt.turn

		// Sim speeds at the same track positions
		simEntry := smoothSim[t.entryIdx]
		simApex := smoothSim[t.apexIdx]
		simExit := smoothSim[t.exitIdx]

		ta := TurnAnalysis{
			Name:          lt.name,
			EntrySpeedMph: t.entrySpeed * kmhToMph,
			ApexSpeedMph:  t.apexSpeed * kmhToMph,
			ExitSpeedMph:  t.exitSpeed * kmhToMph,
			SimEntryMph:   simEntry * kmhToMph,
			SimApexMph:    simApex * kmhToMph,
			SimExitMph:    simExit * kmhToMph,
		}

		ta.Advice = generateAdvice(ta)
		results = append(results, ta)
	}

	return results
}

type labeledTurn struct {
	turn detectedTurn
	name string
}

func labelTurns(turns []detectedTurn, known []KnownTurn) []labeledTurn {
	var result []labeledTurn

	if len(known) == 0 {
		for i, t := range turns {
			result = append(result, labeledTurn{t, fmt.Sprintf("Turn %d", i+1)})
		}
		return result
	}

	used := make(map[int]bool)
	for _, kt := range known {
		bestIdx := -1
		bestDist := math.Inf(1)
		for i, t := range turns {
			if used[i] {
				continue
			}
			d := haversine(kt.Lat, kt.Lon, t.apexLat, t.apexLon)
			if d < bestDist {
				bestDist = d
				bestIdx = i
			}
		}
		if bestIdx >= 0 && bestDist < 150 {
			used[bestIdx] = true
			result = append(result, labeledTurn{turns[bestIdx], kt.Name})
		}
	}

	// Add any unmatched turns
	for i, t := range turns {
		if !used[i] {
			result = append(result, labeledTurn{t, fmt.Sprintf("Turn ?%d", i+1)})
		}
	}

	// Sort by distance
	for i := 1; i < len(result); i++ {
		for j := i; j > 0 && result[j].turn.apexDist < result[j-1].turn.apexDist; j-- {
			result[j], result[j-1] = result[j-1], result[j]
		}
	}

	return result
}

func generateAdvice(ta TurnAnalysis) string {
	const threshold = 2.0 // mph — below this delta, it's "good"

	entryDelta := ta.SimEntryMph - ta.EntrySpeedMph // positive = sim faster
	apexDelta := ta.SimApexMph - ta.ApexSpeedMph
	exitDelta := ta.SimExitMph - ta.ExitSpeedMph

	// Count areas with meaningful improvement potential
	issues := 0
	if entryDelta > threshold {
		issues++
	}
	if apexDelta > threshold {
		issues++
	}
	if exitDelta > threshold {
		issues++
	}

	if issues == 0 {
		if entryDelta < -threshold || apexDelta < -threshold {
			return "You're driving this turn well — at or above the theoretical limit."
		}
		return "Looks good — close to optimal."
	}

	var advice string

	// Entry speed
	if entryDelta > threshold {
		advice += fmt.Sprintf("Carry %.0f mph more into the corner. ", entryDelta)
	} else if entryDelta < -threshold {
		advice += fmt.Sprintf("Entry speed is %.0f mph above sim — may be overdriving entry. ", -entryDelta)
	}

	// Apex speed
	if apexDelta > threshold {
		if entryDelta > threshold {
			// Both entry and apex are slow — likely braking too early
			advice += fmt.Sprintf("Brake later and carry %.0f mph more through the apex. ", apexDelta)
		} else {
			// Entry is fine but apex is slow — too much scrubbing or wrong line
			advice += fmt.Sprintf("Carry %.0f mph more at the apex — tighten your line or reduce mid-corner braking. ", apexDelta)
		}
	}

	// Exit speed
	if exitDelta > threshold {
		if apexDelta <= threshold {
			// Apex is fine but exit is slow — late on throttle
			advice += fmt.Sprintf("Get on power earlier — %.0f mph more at exit. ", exitDelta)
		} else {
			advice += fmt.Sprintf("%.0f mph more at exit. ", exitDelta)
		}
	} else if exitDelta < -threshold && apexDelta > threshold {
		// Exit is faster than sim but apex was slow — driver overslows then floors it
		advice += "You're overslowing mid-corner then making up time on exit — smoother arc would be faster overall. "
	}

	if advice == "" {
		advice = "Looks good — close to optimal."
	}

	return advice
}

func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	return R * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func getKnownTurns(trackName, trackConfig string) []KnownTurn {
	key := trackName + "|" + trackConfig
	turns, ok := knownTurnsDB[key]
	if !ok {
		return nil
	}
	return turns
}

var knownTurnsDB = map[string][]KnownTurn{
	"Laguna Seca|Full": {
		{1, "T1", 36.586291, -121.756795},
		{2, "T2 Andretti", 36.582534, -121.757504},
		{3, "T3", 36.582583, -121.757065},
		{4, "T4", 36.584670, -121.756979},
		{5, "T5", 36.584323, -121.753978},
		{6, "T6", 36.580511, -121.754389},
		{7, "T7", 36.580311, -121.750156},
		{8, "T8 Corkscrew", 36.584742, -121.749022},
		{9, "T9 Rainey", 36.586887, -121.750180},
		{10, "T10", 36.586497, -121.752594},
		{11, "T11", 36.588625, -121.754504},
	},
	"Thunderhill|East Cyclone": {
		{1, "T1", 39.536582, -122.330782},
		{2, "T2", 39.536763, -122.326646},
		{3, "T3", 39.538636, -122.329485},
		{4, "T4", 39.539707, -122.328658},
		{5, "T5", 39.541066, -122.329392},
		{5.5, "T5a", 39.542178, -122.329714},
		{6, "T6", 39.543263, -122.328534},
		{7, "T7", 39.544939, -122.330668},
		{8, "T8", 39.544943, -122.333386},
		{9, "T9", 39.542655, -122.336268},
		{10, "T10", 39.538136, -122.335237},
		{11, "T11", 39.537965, -122.333554},
		{12, "T12", 39.538356, -122.333360},
		{13, "T13", 39.538786, -122.333246},
		{14, "T14", 39.544014, -122.332502},
		{15, "T15", 39.543423, -122.331293},
	},
	"Thunderhill|East Bypass": {
		{1, "T1", 39.536582, -122.330782},
		{2, "T2", 39.536763, -122.326646},
		{3, "T3", 39.538636, -122.329485},
		{4, "T4", 39.539707, -122.328658},
		{5, "T5", 39.542178, -122.329714},
		{6, "T6", 39.543263, -122.328534},
		{7, "T7", 39.544939, -122.330668},
		{8, "T8", 39.544943, -122.333386},
		{9, "T9", 39.542655, -122.336268},
		{10, "T10", 39.538136, -122.335237},
		{11, "T11", 39.537965, -122.333554},
		{12, "T12", 39.538356, -122.333360},
		{13, "T13", 39.538786, -122.333246},
		{14, "T14", 39.544014, -122.332502},
		{15, "T15", 39.543423, -122.331293},
	},
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
