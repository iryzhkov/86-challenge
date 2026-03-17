package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/iryzhkov/86-challenge/analysis"
	"github.com/iryzhkov/86-challenge/models"
	"github.com/iryzhkov/86-challenge/track"
	"github.com/iryzhkov/86-challenge/vbo"
)

// trackAnimPoint is a subsampled GPS point for the loading animation.
type trackAnimPoint struct {
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Speed float64 `json:"speed"` // normalized 0-1 (0=slowest, 1=fastest)
}

// SimulatePage renders the simulation page with loading animation.
// It pre-loads the VBO to extract the track outline for the animation
// (fast, <100ms), while the actual simulation runs async via the API.
func SimulatePage(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/session/simulate/")
	if id == "" {
		http.NotFound(w, r)
		return
	}

	session, err := models.GetSession(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Pre-load VBO to extract track outline for animation
	var trackPoints []trackAnimPoint
	vboPath := filepath.Join("uploads", id, session.Filename)
	if data, err := os.ReadFile(vboPath); err == nil {
		if parsed, err := vbo.Parse(bytes.NewReader(data)); err == nil {
			laps := track.SplitLaps(parsed.Points, parsed.SplitGates)
			// Find best valid lap
			var bestLap *track.LapResult
			for i := range laps {
				if laps[i].IsValid && (bestLap == nil || laps[i].LapTimeMs < bestLap.LapTimeMs) {
					bestLap = &laps[i]
				}
			}
			if bestLap != nil {
				trackPoints = buildTrackAnim(parsed.Points, bestLap.StartIdx, bestLap.EndIdx)
			}
		}
	}

	trackJSON, _ := json.Marshal(trackPoints)

	Templates["simulate.html"].ExecuteTemplate(w, "base", map[string]any{
		"Session":    session,
		"TrackJSON":  string(trackJSON),
	})
}

// buildTrackAnim creates a subsampled, normalized track outline for SVG animation.
// Coordinates are projected to a local 2D plane and scaled to fit a viewbox.
// Speed is normalized 0-1 for variable animation speed.
func buildTrackAnim(points []vbo.DataPoint, startIdx, endIdx int) []trackAnimPoint {
	lapPoints := points[startIdx:endIdx]
	if len(lapPoints) < 20 {
		return nil
	}

	// Subsample to ~100 points
	step := len(lapPoints) / 100
	if step < 1 {
		step = 1
	}

	// Find bounds and speed range
	refLat := lapPoints[0].Lat
	refLon := lapPoints[0].Lon
	const earthR = 6371000.0
	const deg2rad = 3.14159265 / 180.0

	type rawPt struct {
		x, y, speed float64
	}
	var raw []rawPt
	var minX, maxX, minY, maxY float64
	var minSpd, maxSpd float64
	first := true

	for i := 0; i < len(lapPoints); i += step {
		p := lapPoints[i]
		x := (p.Lon - refLon) * deg2rad * earthR * cosApprox(refLat*deg2rad)
		y := (p.Lat - refLat) * deg2rad * earthR
		spd := p.SpeedKmh

		if first {
			minX, maxX = x, x
			minY, maxY = y, y
			minSpd, maxSpd = spd, spd
			first = false
		} else {
			if x < minX { minX = x }
			if x > maxX { maxX = x }
			if y < minY { minY = y }
			if y > maxY { maxY = y }
			if spd < minSpd { minSpd = spd }
			if spd > maxSpd { maxSpd = spd }
		}
		raw = append(raw, rawPt{x, y, spd})
	}

	// Scale to fit in 320x180 viewbox, centered
	const svgW, svgH = 320.0, 180.0
	const pad = 15.0
	availW := svgW - 2*pad
	availH := svgH - 2*pad
	rangeX := maxX - minX
	rangeY := maxY - minY
	if rangeX < 1 { rangeX = 1 }
	if rangeY < 1 { rangeY = 1 }
	scale := availW / rangeX
	if availH/rangeY < scale {
		scale = availH / rangeY
	}
	// Center offset: actual drawn size vs available space
	drawnW := rangeX * scale
	drawnH := rangeY * scale
	offsetX := pad + (availW-drawnW)/2
	offsetY := pad + (availH-drawnH)/2

	spdRange := maxSpd - minSpd
	if spdRange < 1 { spdRange = 1 }

	result := make([]trackAnimPoint, len(raw))
	for i, r := range raw {
		result[i] = trackAnimPoint{
			X:     offsetX + (r.x-minX)*scale,
			Y:     offsetY + drawnH - (r.y-minY)*scale, // flip Y for SVG
			Speed: (r.speed - minSpd) / spdRange,
		}
	}
	return result
}

func cosApprox(rad float64) float64 {
	// Simple cos approximation, good enough for projection
	x := rad
	return 1 - x*x/2 + x*x*x*x/24
}

// SimulateAPI runs the lap simulation and returns results as JSON.
func SimulateAPI(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/simulate/")
	if id == "" {
		http.Error(w, "Missing session ID", http.StatusBadRequest)
		return
	}

	session, err := models.GetSession(id)
	if err != nil {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Load VBO file
	vboPath := filepath.Join("uploads", id, session.Filename)
	data, err := os.ReadFile(vboPath)
	if err != nil {
		http.Error(w, "VBO file not found", http.StatusNotFound)
		return
	}

	parsed, err := vbo.Parse(bytes.NewReader(data))
	if err != nil {
		http.Error(w, fmt.Sprintf("VBO parse error: %v", err), http.StatusInternalServerError)
		return
	}

	// Check if VBO has necessary fields
	if !hasSimFields(parsed) {
		http.Error(w, "VBO file missing required telemetry fields (lateral G, heading, throttle)", http.StatusBadRequest)
		return
	}

	// Split laps
	laps := track.SplitLaps(parsed.Points, parsed.SplitGates)
	var validLaps []track.LapResult
	for _, l := range laps {
		if l.IsValid {
			validLaps = append(validLaps, l)
		}
	}
	if len(validLaps) == 0 {
		http.Error(w, "No valid laps found", http.StatusBadRequest)
		return
	}

	// Build grip envelope from all valid laps
	var gripPoints []analysis.TelemetryPoint
	for _, l := range validLaps {
		trackModel := analysis.BuildTrackModel(parsed.Points, l.StartIdx, l.EndIdx)
		for _, tp := range trackModel {
			gripPoints = append(gripPoints, analysis.TelemetryPoint{
				SpeedMs:     tp.SpeedMs,
				LatG:        tp.LatG,
				LongG:       tp.LongG,
				VerticalG:   tp.VerticalG,
				Gear:        tp.Gear,
				ThrottlePct: tp.ThrottlePct,
				BrakePct:    tp.BrakePct,
				SteeringDeg: tp.SteeringDeg,
			})
		}
	}

	grip := analysis.BuildGripEnvelope(gripPoints, session.CarClass, session.TireBrand, session.TireModel)

	// Find best valid lap
	bestLap := validLaps[0]
	for _, l := range validLaps[1:] {
		if l.LapTimeMs < bestLap.LapTimeMs {
			bestLap = l
		}
	}

	// Build track model
	trackModel := analysis.BuildTrackModel(parsed.Points, bestLap.StartIdx, bestLap.EndIdx)

	// Pre-compute simulations at multiple utilization levels
	// 1% steps from 80% to 100%
	var utilizationLevels []float64
	for u := 80; u <= 100; u++ {
		utilizationLevels = append(utilizationLevels, float64(u)/100.0)
	}
	defaultUtil := 0.95

	// Subsample indices for speed profiles
	maxPoints := 500
	step := 1
	if len(trackModel) > maxPoints {
		step = len(trackModel) / maxPoints
	}

	// Build actual speed + distance arrays (same for all utilizations)
	type speedPoint struct {
		DistM     float64 `json:"dist_m"`
		ActualKmh float64 `json:"actual_kmh"`
	}
	var distances []speedPoint
	for i := 0; i < len(trackModel); i += step {
		distances = append(distances, speedPoint{
			DistM:     trackModel[i].Distance,
			ActualKmh: trackModel[i].SpeedMs * 3.6,
		})
	}

	type simPreset struct {
		Utilization  float64   `json:"utilization"`
		LapTimeMs    int       `json:"lap_time_ms"`
		LapTime      string    `json:"lap_time"`
		DeltaMs      int       `json:"delta_ms"`
		DeltaPct     float64   `json:"delta_pct"`
		SimSpeedsKmh []float64 `json:"sim_speeds_kmh"`
	}

	var presets []simPreset
	for _, util := range utilizationLevels {
		result := analysis.Simulate(trackModel, &grip, util)

		var simSpeeds []float64
		for i := 0; i < len(trackModel); i += step {
			simSpeeds = append(simSpeeds, result.SimSpeedsMs[i]*3.6)
		}

		presets = append(presets, simPreset{
			Utilization:  util,
			LapTimeMs:    result.TheoreticalLapTimeMs,
			LapTime:      FormatLapTime(result.TheoreticalLapTimeMs),
			DeltaMs:      result.DeltaMs,
			DeltaPct:     result.DeltaPct,
			SimSpeedsKmh: simSpeeds,
		})
	}

	// Find crossover utilization: where theoretical time ≈ actual time.
	// DeltaMs = actual - theoretical. Negative means sim is slower.
	// As utilization increases, DeltaMs goes from negative to positive.
	// The crossover (DeltaMs = 0) is the driver's estimated grip usage.
	estimatedUtil := 0.0
	for i := 1; i < len(presets); i++ {
		if presets[i-1].DeltaMs < 0 && presets[i].DeltaMs >= 0 {
			// Linear interpolation
			d1 := float64(-presets[i-1].DeltaMs) // make positive
			d2 := float64(presets[i].DeltaMs)
			t := d1 / (d1 + d2)
			estimatedUtil = presets[i-1].Utilization*(1-t) + presets[i].Utilization*t
			break
		}
	}
	if estimatedUtil == 0 && len(presets) > 0 {
		if presets[0].DeltaMs >= 0 {
			estimatedUtil = presets[0].Utilization // already faster at lowest setting
		} else {
			estimatedUtil = 1.0 // driver is faster than even 100% theoretical
		}
	}

	// Estimate vehicle characteristics from telemetry
	assumedWeight := analysis.ClassWeight(session.CarClass, session.CarModel)
	vehicle := analysis.EstimateVehicle(&grip, trackModel, assumedWeight)

	type vehicleJSON struct {
		EstPowerHP      float64 `json:"est_power_hp"`
		AssumedWeightLb float64 `json:"assumed_weight_lb"`
		CdA             float64 `json:"cda"`
		TopSpeedMph     float64 `json:"top_speed_mph"`
	}

	// Turn-by-turn advice at 95% utilization
	adviceResult := analysis.Simulate(trackModel, &grip, 0.95)
	turnAdvice := analysis.AnalyzeTurns(trackModel, adviceResult.SimSpeedsMs, session.TrackName, session.TrackConfig)

	resp := map[string]any{
		"actual_lap_ms":   bestLap.LapTimeMs,
		"actual_lap_time": FormatLapTime(bestLap.LapTimeMs),
		"track_length_m":  trackModel[len(trackModel)-1].Distance,
		"max_lat_g":       grip.MaxLatG,
		"max_brake_g":     grip.MaxBrakeG,
		"lat_g_rate":      grip.LatGRate,
		"top_speed_kmh":   grip.MaxSpeedMs * 3.6,
		"default_util":    defaultUtil,
		"estimated_util":  estimatedUtil,
		"distances":       distances,
		"presets":         presets,
		"lap_number":      bestLap.LapNumber,
		"turn_advice":     turnAdvice,
		"vehicle": vehicleJSON{
			EstPowerHP:      vehicle.EstPowerHP,
			AssumedWeightLb: assumedWeight * 2.20462,
			CdA:             vehicle.CdA,
			TopSpeedMph:     vehicle.TopSpeedKmh / 1.60934,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// CheckSimCapable checks if a session's VBO file exists on disk and contains
// the required telemetry fields with non-zero data (lateral G, longitudinal G,
// throttle, brake, heading). Samples several points across the session to avoid
// false negatives from a single zero reading.
func CheckSimCapable(sessionID, filename string) bool {
	path := filepath.Join("uploads", sessionID, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	parsed, err := vbo.Parse(bytes.NewReader(data))
	if err != nil || len(parsed.Points) < 100 {
		return false
	}

	return hasSimFields(parsed)
}

func hasSimFields(parsed *vbo.ParsedSession) bool {
	if len(parsed.Points) < 100 {
		return false
	}

	// Scan a chunk of points in the middle of the session to check that
	// required channels have non-zero data. We check 100 consecutive points
	// around the midpoint — this avoids false negatives from sampling a
	// single moment where brake=0 (coasting) or throttle=0 (braking).
	start := len(parsed.Points)/2 - 50
	if start < 0 {
		start = 0
	}
	end := start + 100
	if end > len(parsed.Points) {
		end = len(parsed.Points)
	}

	var hasLatG, hasLongG, hasThrottle, hasBrake, hasHeading bool
	for i := start; i < end; i++ {
		p := parsed.Points[i]
		if p.LatG != 0 {
			hasLatG = true
		}
		if p.LongG != 0 {
			hasLongG = true
		}
		if p.ThrottlePct != 0 {
			hasThrottle = true
		}
		if p.BrakePct != 0 {
			hasBrake = true
		}
		if p.Heading != 0 {
			hasHeading = true
		}
		if hasLatG && hasLongG && hasThrottle && hasBrake && hasHeading {
			return true
		}
	}

	return hasLatG && hasLongG && hasThrottle && hasBrake && hasHeading
}
