package track

import (
	"math"

	"github.com/iryzhkov/86-challenge/vbo"
)

const detectionRadiusKm = 3.0

// DetectTrack identifies which track and configuration a session was recorded at.
// Uses GPS centroid for track identification, then discriminator waypoints and
// winding direction to determine the specific configuration.
func DetectTrack(points []vbo.DataPoint) (name string, config string, found bool) {
	if len(points) == 0 {
		return "", "", false
	}

	// Filter to fast points for analysis (skip pit/idle)
	var fast []vbo.DataPoint
	for _, p := range points {
		if p.SpeedKmh > 30 {
			fast = append(fast, p)
		}
	}
	if len(fast) == 0 {
		fast = points
	}

	// Compute centroid
	var sumLat, sumLon float64
	for _, p := range fast {
		sumLat += p.Lat
		sumLon += p.Lon
	}
	centroidLat := sumLat / float64(len(fast))
	centroidLon := sumLon / float64(len(fast))

	// Find closest known track (by name, deduplicating)
	bestDist := math.MaxFloat64
	bestName := ""
	seen := map[string]bool{}
	for _, t := range KnownTracks {
		if seen[t.Name] {
			continue
		}
		dist := vbo.Haversine(centroidLat, centroidLon, t.CenterLat, t.CenterLon)
		if dist < bestDist {
			bestDist = dist
			bestName = t.Name
		}
		seen[t.Name] = true
	}

	if bestDist > detectionRadiusKm*1000 {
		return "", "", false
	}

	// Determine config
	configs := ConfigsForTrack(bestName)
	if len(configs) == 1 {
		return bestName, configs[0], true
	}

	// Multiple configs — try discriminator waypoints and winding direction
	winding := detectWinding(fast)
	bestConfig := disambiguateConfig(bestName, fast, winding)

	return bestName, bestConfig, true
}

// disambiguateConfig scores each config for a track and returns the best match.
func disambiguateConfig(trackName string, fast []vbo.DataPoint, winding string) string {
	type candidate struct {
		config string
		score  int
	}
	var candidates []candidate

	for _, t := range KnownTracks {
		if t.Name != trackName {
			continue
		}

		score := 0

		// Check discriminator waypoint
		if t.DiscRadius > 0 {
			if passesNear(fast, t.DiscLat, t.DiscLon, t.DiscRadius) {
				score += 10 // strong signal
			} else {
				score -= 10 // disqualify
			}
		}

		// Check winding direction
		if t.Direction != "" && winding != "" {
			if t.Direction == winding {
				score += 5
			} else {
				score -= 5
			}
		}

		candidates = append(candidates, candidate{t.Config, score})
	}

	// Pick highest scoring config
	bestScore := math.MinInt
	bestConfig := ""
	for _, c := range candidates {
		if c.score > bestScore {
			bestScore = c.score
			bestConfig = c.config
		}
	}
	return bestConfig
}

// passesNear returns true if any point in the trace is within radius meters of (lat, lon).
func passesNear(points []vbo.DataPoint, lat, lon, radius float64) bool {
	for _, p := range points {
		if vbo.Haversine(p.Lat, p.Lon, lat, lon) <= radius {
			return true
		}
	}
	return false
}

// detectWinding determines the direction of travel around a closed track.
// Returns "CW" for clockwise, "CCW" for counter-clockwise, or "" if unclear.
// Uses the shoelace formula on the GPS trace: negative signed area = CW when
// viewed from above with standard lat(y)/lon(x) orientation.
func detectWinding(points []vbo.DataPoint) string {
	if len(points) < 100 {
		return ""
	}

	// Subsample to avoid noise and speed up
	step := len(points) / 500
	if step < 1 {
		step = 1
	}

	// Scale longitude by cos(lat) for equal-area approximation
	midLat := points[len(points)/2].Lat
	cosLat := math.Cos(midLat * math.Pi / 180)

	var signedArea float64
	var sampled []vbo.DataPoint
	for i := 0; i < len(points); i += step {
		sampled = append(sampled, points[i])
	}

	n := len(sampled)
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		xi := sampled[i].Lon * cosLat
		yi := sampled[i].Lat
		xj := sampled[j].Lon * cosLat
		yj := sampled[j].Lat
		signedArea += xi*yj - xj*yi
	}

	if math.Abs(signedArea) < 1e-10 {
		return ""
	}

	// In lat/lon space: positive signed area = CCW, negative = CW
	if signedArea > 0 {
		return "CCW"
	}
	return "CW"
}

// ConfigsForTrack returns all known configurations for a track name.
func ConfigsForTrack(name string) []string {
	var configs []string
	for _, t := range KnownTracks {
		if t.Name == name {
			configs = append(configs, t.Config)
		}
	}
	return configs
}

// CrossesGate detects if the path from p1 to p2 crosses a line perpendicular
// to the gate direction, passing through the gate midpoint.
//
// VBO gate endpoints define the direction of travel (parallel to the straight).
// We create a crossing line perpendicular to that direction at the midpoint,
// which reliably detects lap crossings even on long straights.
func CrossesGate(p1, p2 vbo.DataPoint, gLat1, gLon1, gLat2, gLon2 float64) (bool, float64) {
	midLat := (gLat1 + gLat2) / 2
	midLon := (gLon1 + gLon2) / 2

	// Gate direction vector (approximately the car's direction on the straight)
	cosLat := math.Cos(midLat * math.Pi / 180)
	gdx := (gLon2 - gLon1) * cosLat
	gdy := gLat2 - gLat1

	// Perpendicular direction (rotated 90°)
	perpDx := -gdy
	perpDy := gdx

	// Signed distance from each point to the perpendicular line through midpoint.
	// The perpendicular line: all points where dot(P - mid, gateDir) = 0
	// We use dot product with the gate direction to measure "along the straight"
	sd1 := (p1.Lon-midLon)*cosLat*gdx + (p1.Lat-midLat)*gdy
	sd2 := (p2.Lon-midLon)*cosLat*gdx + (p2.Lat-midLat)*gdy

	// Sign change means the car crossed the perpendicular line
	if sd1*sd2 >= 0 {
		return false, 0
	}

	// Interpolate crossing position
	t := sd1 / (sd1 - sd2)
	crossLat := p1.Lat + t*(p2.Lat-p1.Lat)
	crossLon := p1.Lon + t*(p2.Lon-p1.Lon)

	// Verify crossing is near the gate (within gate length / 2 of perpendicular line)
	// Use perpendicular distance to check lateral offset
	perpDist := (crossLon-midLon)*cosLat*perpDx + (crossLat-midLat)*perpDy
	perpLen := math.Sqrt(perpDx*perpDx + perpDy*perpDy)
	if perpLen > 0 {
		perpDist /= perpLen
	}

	// Gate length in degrees (approximate)
	gateLen := math.Sqrt(gdx*gdx + gdy*gdy)
	if math.Abs(perpDist) > gateLen/2*1.5 { // allow 50% extra margin
		return false, 0
	}

	_ = perpDx // used above
	_ = perpDy

	crossTime := p1.Time + t*(p2.Time-p1.Time)
	return true, crossTime
}
