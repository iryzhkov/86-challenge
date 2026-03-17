package analysis

import (
	"math"

	"github.com/iryzhkov/86-challenge/vbo"
)

// TrackPoint represents a single point on the track centerline.
type TrackPoint struct {
	Distance    float64 // cumulative distance from start (m)
	Lat         float64
	Lon         float64
	HeightM     float64
	SpeedMs     float64 // actual speed from telemetry
	Heading     float64 // degrees
	Curvature   float64 // 1/radius (1/m), always positive
	Gradient    float64 // slope (rise/run), positive = uphill
	VerticalG   float64 // effective vertical G from track profile
	LatG        float64 // actual lateral G from telemetry
	LongG       float64 // actual longitudinal G from telemetry
	Gear        int
	ThrottlePct float64
	BrakePct    float64
	SteeringDeg float64
}

// BuildTrackModel creates a track model from a lap's telemetry data.
// It computes curvature, gradient, and vertical G at each point.
func BuildTrackModel(points []vbo.DataPoint, startIdx, endIdx int) []TrackPoint {
	if endIdx-startIdx < 10 {
		return nil
	}

	lapPoints := points[startIdx:endIdx]
	n := len(lapPoints)
	track := make([]TrackPoint, n)

	// First pass: compute distances and basic values
	var cumDist float64
	for i, p := range lapPoints {
		if i > 0 {
			cumDist += vbo.Haversine(lapPoints[i-1].Lat, lapPoints[i-1].Lon, p.Lat, p.Lon)
		}
		track[i] = TrackPoint{
			Distance:    cumDist,
			Lat:         p.Lat,
			Lon:         p.Lon,
			HeightM:     p.HeightM,
			Heading:     p.Heading,
			SpeedMs:     p.SpeedKmh / 3.6,
			LatG:        p.LatG,
			LongG:       p.LongG,
			Gear:        int(p.Gear),
			ThrottlePct: p.ThrottlePct,
			BrakePct:    p.BrakePct,
			SteeringDeg: p.SteeringDeg,
			VerticalG:   1.0, // default, computed below
		}
	}

	// Smooth height data heavily — GPS altitude is noisy (~2-3m accuracy).
	// We need clean second derivatives for vertical G, so smooth aggressively.
	// Window=31 (~120m at 10Hz/40m/s) removes noise while preserving major
	// elevation features (hills, dips).
	smoothedHeight := smoothArray(extractHeights(track), 31)
	for i := range track {
		track[i].HeightM = smoothedHeight[i]
	}

	// Compute curvature from heading changes: k = d(heading)/ds
	// We use heading-only (not G-force) because:
	// - G-force spikes from kerbs, bumps, and compression create fake curvature
	// - Heading from GPS is smooth and reflects the actual track geometry
	// - At 10Hz with 2-point derivative, heading captures corners well enough
	for i := 1; i < n-1; i++ {
		ds := track[i+1].Distance - track[i-1].Distance
		if ds < 0.1 {
			ds = 0.1
		}
		dHeading := lapPoints[i+1].Heading - lapPoints[i-1].Heading
		for dHeading > 180 {
			dHeading -= 360
		}
		for dHeading < -180 {
			dHeading += 360
		}
		track[i].Curvature = math.Abs(dHeading*math.Pi/180) / ds
	}
	track[0].Curvature = track[1].Curvature
	track[n-1].Curvature = track[n-2].Curvature

	// Smooth curvature (window=9, ~36m) to reduce GPS heading noise
	// and spread corner curvature over the full turning arc. This prevents
	// the heading derivative from creating a sharp peak that lags the
	// actual apex — the driver manages speed through the whole corner,
	// not at a single point.
	smoothedCurv := smoothArray(extractCurvatures(track), 5)
	for i := range track {
		track[i].Curvature = smoothedCurv[i]
	}

	// Compute gradient (dHeight/dDistance)
	for i := 1; i < n-1; i++ {
		ds := track[i+1].Distance - track[i-1].Distance
		dh := track[i+1].HeightM - track[i-1].HeightM
		if ds > 0.1 {
			track[i].Gradient = dh / ds
		}
	}
	track[0].Gradient = track[1].Gradient
	track[n-1].Gradient = track[n-2].Gradient

	// Compute vertical G from track profile curvature in vertical plane.
	// When the car goes through a compression (concave up), vertical G > 1.
	// When cresting (convex up), vertical G < 1.
	// verticalG = 1 + v² * d²h/ds²
	// Use wider spacing (5 points apart, ~20m) for stable second derivative.
	const vgSpan = 5
	for i := vgSpan; i < n-vgSpan; i++ {
		ds1 := track[i].Distance - track[i-vgSpan].Distance
		ds2 := track[i+vgSpan].Distance - track[i].Distance
		if ds1 > 0.5 && ds2 > 0.5 {
			grad1 := (track[i].HeightM - track[i-vgSpan].HeightM) / ds1
			grad2 := (track[i+vgSpan].HeightM - track[i].HeightM) / ds2
			dsAvg := (ds1 + ds2) / 2
			d2hds2 := (grad2 - grad1) / dsAvg

			speed := track[i].SpeedMs
			if speed < 5 {
				speed = 5
			}
			vertAccel := speed * speed * d2hds2
			track[i].VerticalG = 1.0 + vertAccel/9.81
		}
	}
	// Smooth vertical G to reduce remaining noise
	vgValues := make([]float64, n)
	for i := range track {
		vgValues[i] = track[i].VerticalG
	}
	vgSmoothed := smoothArray(vgValues, 11)
	for i := range track {
		track[i].VerticalG = vgSmoothed[i]
	}
	// Clamp to reasonable range
	for i := range track {
		if track[i].VerticalG < 0.5 {
			track[i].VerticalG = 0.5
		}
		if track[i].VerticalG > 1.8 {
			track[i].VerticalG = 1.8
		}
	}

	return track
}

// computeCurvature estimates curvature at the middle point using Menger curvature
// (circumradius of three points).
func computeCurvature(lat1, lon1, lat2, lon2, lat3, lon3 float64) float64 {
	// Convert to local meters (approximate)
	x1, y1 := toLocalMeters(lat1, lon1, lat2, lon2)
	x2, y2 := 0.0, 0.0
	x3, y3 := toLocalMeters(lat3, lon3, lat2, lon2)

	// Triangle area * 2
	area2 := (x2-x1)*(y3-y1) - (x3-x1)*(y2-y1)

	// Side lengths
	a := math.Sqrt((x2-x1)*(x2-x1) + (y2-y1)*(y2-y1))
	b := math.Sqrt((x3-x2)*(x3-x2) + (y3-y2)*(y3-y2))
	c := math.Sqrt((x3-x1)*(x3-x1) + (y3-y1)*(y3-y1))

	denom := a * b * c
	if denom < 0.01 {
		return 0 // points too close together
	}

	// Menger curvature: k = 4*area / (a*b*c)
	// Sign indicates direction: positive = left turn
	return 2.0 * area2 / denom
}

// toLocalMeters converts a lat/lon point to meters relative to a reference point.
func toLocalMeters(lat, lon, refLat, refLon float64) (float64, float64) {
	const earthR = 6371000.0
	dLat := (lat - refLat) * math.Pi / 180
	dLon := (lon - refLon) * math.Pi / 180
	y := dLat * earthR
	x := dLon * earthR * math.Cos(refLat*math.Pi/180)
	return x, y
}

func extractHeights(track []TrackPoint) []float64 {
	out := make([]float64, len(track))
	for i, t := range track {
		out[i] = t.HeightM
	}
	return out
}

func extractCurvatures(track []TrackPoint) []float64 {
	out := make([]float64, len(track))
	for i, t := range track {
		out[i] = t.Curvature
	}
	return out
}

// smoothArray applies a centered moving average with the given window size.
func smoothArray(data []float64, window int) []float64 {
	n := len(data)
	if n == 0 {
		return data
	}
	out := make([]float64, n)
	half := window / 2
	for i := range data {
		sum := 0.0
		count := 0
		for j := i - half; j <= i+half; j++ {
			if j >= 0 && j < n {
				sum += data[j]
				count++
			}
		}
		out[i] = sum / float64(count)
	}
	return out
}
