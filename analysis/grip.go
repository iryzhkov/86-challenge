package analysis

import (
	"math"
	"sort"
)

// GripEnvelope represents the measured grip limits of the vehicle
// for a specific car class + tire combination.
type GripEnvelope struct {
	CarClass  string
	TireBrand string
	TireModel string

	MaxLatG    float64 // peak lateral G (98th percentile)
	MaxBrakeG  float64 // peak braking G (positive = decel)
	MaxSpeedMs float64 // observed top speed (m/s)

	// Lateral G rate: how fast the car can change lateral acceleration (G/s).
	// Reflects yaw inertia, suspension response, and tire slip angle build-up.
	// Measured from the actual telemetry data.
	LatGRate float64

	// Speed-binned grip limits (accounts for aero/tire behavior at speed)
	SpeedBins []SpeedBin

	// Straight-line acceleration curve: derived from full-throttle data on straights.
	AccelCurve []AccelPoint
}

// SpeedBin holds cornering/braking grip data for a speed range.
type SpeedBin struct {
	SpeedMs   float64 // center of bin (m/s)
	MaxLatG   float64
	MaxBrakeG float64
	Samples   int
}

// AccelPoint is one point on the straight-line acceleration curve.
type AccelPoint struct {
	SpeedMs float64
	AccelG  float64 // max sustained acceleration at this speed
	Samples int
}

// TelemetryPoint is a single point used for grip analysis.
type TelemetryPoint struct {
	SpeedMs     float64
	LatG        float64
	LongG       float64
	VerticalG   float64 // effective vertical G (1.0 = flat, >1 = compression, <1 = crest)
	Gear        int
	ThrottlePct float64
	BrakePct    float64
	SteeringDeg float64
}

// BuildGripEnvelope analyzes telemetry points to build a friction circle
// and straight-line acceleration curve.
//
// Grip normalization: when the car is in compression (verticalG > 1), tires
// have more normal force. We normalize measured G-forces by vertical load
// to find the "base" friction coefficient.
//
// Acceleration curve: built from full-throttle straight-line data.
// Upshift transients (brief throttle cuts during gear changes) are filtered
// by using the 90th percentile within each speed bin — upshift dips are
// low outliers and get excluded naturally.
func BuildGripEnvelope(points []TelemetryPoint, carClass, tireBrand, tireModel string) GripEnvelope {
	env := GripEnvelope{
		CarClass:  carClass,
		TireBrand: tireBrand,
		TireModel: tireModel,
	}

	if len(points) == 0 {
		return env
	}

	const loadSensitivity = 0.1 // typical for street tires
	const binWidth = 5.0        // m/s per bin (finer resolution)
	const numBins = 16          // up to 80 m/s (~288 km/h)

	type binData struct {
		latGs   []float64
		brakeGs []float64
	}
	gripBins := make([]binData, numBins)

	// Separate bins for straight-line acceleration
	accelBins := make([][]float64, numBins)

	var allLatG, allBrakeG []float64
	var maxSpeed float64

	for i, p := range points {
		if p.SpeedMs > maxSpeed {
			maxSpeed = p.SpeedMs
		}
		if p.SpeedMs < 3 {
			continue
		}

		bin := int(p.SpeedMs / binWidth)
		if bin >= numBins {
			bin = numBins - 1
		}

		// Normalize by vertical load for grip analysis
		vgFactor := math.Pow(math.Max(p.VerticalG, 0.3), 1.0-loadSensitivity)
		normLatG := math.Abs(p.LatG) / vgFactor
		normLongG := p.LongG / vgFactor

		allLatG = append(allLatG, normLatG)
		gripBins[bin].latGs = append(gripBins[bin].latGs, normLatG)

		if normLongG < -0.05 {
			allBrakeG = append(allBrakeG, -normLongG)
			gripBins[bin].brakeGs = append(gripBins[bin].brakeGs, -normLongG)
		}

		// Straight-line acceleration: high throttle, low steering, low brake
		// This captures what the car actually does at full power on straights.
		if p.ThrottlePct > 80 && math.Abs(p.SteeringDeg) < 20 && p.BrakePct < 5 {
			// Filter upshifts: check if this point is in the middle of a gear change.
			// During upshifts, throttle is briefly cut and acceleration drops.
			// We detect this by checking if the gear differs from neighbors.
			inUpshift := false
			if i > 0 && i < len(points)-1 {
				prevGear := points[i-1].Gear
				nextGear := points[i+1].Gear
				// If gear is different from either neighbor, likely mid-shift
				if p.Gear != prevGear || p.Gear != nextGear {
					inUpshift = true
				}
				// Also catch the throttle cut: if throttle was much higher recently
				// or acceleration is negative despite high throttle, it's a transient
				if p.LongG < -0.1 && p.ThrottlePct > 90 {
					inUpshift = true
				}
			}

			if !inUpshift && p.LongG > 0 {
				accelBins[bin] = append(accelBins[bin], p.LongG)
			}
		}
	}

	env.MaxLatG = percentile(allLatG, 0.98)
	env.MaxBrakeG = percentile(allBrakeG, 0.98)
	env.MaxSpeedMs = maxSpeed

	// Measure lateral G rate: how fast the car changes lateral acceleration.
	// This reflects yaw inertia, suspension response, and tire characteristics.
	// We compute |d(latG)/dt| between consecutive points at meaningful speeds
	// and take the 95th percentile as the car's limit.
	var latGRates []float64
	for i := 1; i < len(points); i++ {
		if points[i].SpeedMs < 10 || points[i-1].SpeedMs < 10 {
			continue
		}
		// Estimate dt from distance/speed (telemetry points are evenly spaced in time)
		// At 10Hz, dt ≈ 0.1s
		const dt = 0.1 // assume 10Hz sample rate
		dLatG := math.Abs(points[i].LatG - points[i-1].LatG)
		rate := dLatG / dt
		if rate > 0.1 && rate < 50 { // filter noise
			latGRates = append(latGRates, rate)
		}
	}
	if len(latGRates) > 100 {
		env.LatGRate = percentile(latGRates, 0.95)
	} else {
		env.LatGRate = 2.5 // fallback
	}

	// Build cornering/braking speed bins
	for i := 0; i < numBins; i++ {
		sb := SpeedBin{
			SpeedMs: float64(i)*binWidth + binWidth/2,
			Samples: len(gripBins[i].latGs),
		}
		if sb.Samples > 10 {
			sb.MaxLatG = percentile(gripBins[i].latGs, 0.98)
			sb.MaxBrakeG = percentile(gripBins[i].brakeGs, 0.95)
		} else {
			sb.MaxLatG = env.MaxLatG
			sb.MaxBrakeG = env.MaxBrakeG
		}
		env.SpeedBins = append(env.SpeedBins, sb)
	}

	// Build acceleration curve from straight-line data
	// Use 90th percentile to filter out any remaining upshift transients
	for i := 0; i < numBins; i++ {
		ap := AccelPoint{
			SpeedMs: float64(i)*binWidth + binWidth/2,
			Samples: len(accelBins[i]),
		}
		if ap.Samples > 5 {
			ap.AccelG = percentile(accelBins[i], 0.90)
		}
		env.AccelCurve = append(env.AccelCurve, ap)
	}

	// Fill gaps in accel curve by interpolating between populated bins
	fillAccelCurveGaps(env.AccelCurve)

	return env
}

// fillAccelCurveGaps fills bins with no data by interpolating from neighbors.
// Beyond the max observed speed, acceleration drops to zero (top speed).
func fillAccelCurveGaps(curve []AccelPoint) {
	// Find last bin with data
	lastWithData := -1
	for i := len(curve) - 1; i >= 0; i-- {
		if curve[i].Samples > 5 {
			lastWithData = i
			break
		}
	}

	// Bins beyond max data: acceleration = 0 (at or beyond top speed)
	for i := lastWithData + 1; i < len(curve); i++ {
		curve[i].AccelG = 0
		curve[i].Samples = 0
	}

	// Interpolate gaps between populated bins (only within the data range)
	for i := 0; i <= lastWithData; i++ {
		if curve[i].Samples > 5 {
			continue
		}
		// Find prev and next with data
		prev, next := -1, -1
		for j := i - 1; j >= 0; j-- {
			if curve[j].Samples > 5 {
				prev = j
				break
			}
		}
		for j := i + 1; j <= lastWithData; j++ {
			if curve[j].Samples > 5 {
				next = j
				break
			}
		}

		switch {
		case prev >= 0 && next >= 0:
			t := (curve[i].SpeedMs - curve[prev].SpeedMs) / (curve[next].SpeedMs - curve[prev].SpeedMs)
			curve[i].AccelG = curve[prev].AccelG*(1-t) + curve[next].AccelG*t
		case prev >= 0:
			curve[i].AccelG = curve[prev].AccelG
		case next >= 0:
			curve[i].AccelG = curve[next].AccelG
		}
	}
}

// MaxLatGAtSpeed returns the interpolated max lateral G for a given speed,
// adjusted for vertical load.
func (e *GripEnvelope) MaxLatGAtSpeed(speedMs, verticalG float64) float64 {
	baseG := interpolateBins(e.SpeedBins, speedMs, func(b SpeedBin) float64 { return b.MaxLatG })
	return baseG * verticalGFactor(verticalG)
}

// MaxBrakeGAtSpeed returns the interpolated max braking G for a given speed,
// adjusted for vertical load.
func (e *GripEnvelope) MaxBrakeGAtSpeed(speedMs, verticalG float64) float64 {
	baseG := interpolateBins(e.SpeedBins, speedMs, func(b SpeedBin) float64 { return b.MaxBrakeG })
	return baseG * verticalGFactor(verticalG)
}

// MaxAccelGAtSpeed returns the max straight-line acceleration at a given speed.
// This comes from the measured full-throttle data, not a theoretical model.
func (e *GripEnvelope) MaxAccelGAtSpeed(speedMs, verticalG float64) float64 {
	if len(e.AccelCurve) == 0 {
		return 0.3 // fallback
	}

	// Interpolate from accel curve
	for i := 0; i < len(e.AccelCurve)-1; i++ {
		if speedMs <= e.AccelCurve[i+1].SpeedMs {
			lo := e.AccelCurve[i]
			hi := e.AccelCurve[i+1]
			t := (speedMs - lo.SpeedMs) / (hi.SpeedMs - lo.SpeedMs)
			t = math.Max(0, math.Min(1, t))
			baseAccel := lo.AccelG*(1-t) + hi.AccelG*t
			// Vertical G helps acceleration too (more traction)
			return baseAccel * verticalGFactor(verticalG)
		}
	}
	// Beyond curve: no more acceleration (at top speed)
	return 0
}

func interpolateBins(bins []SpeedBin, speedMs float64, getter func(SpeedBin) float64) float64 {
	if len(bins) == 0 {
		return 1.0
	}

	for i := 0; i < len(bins)-1; i++ {
		if speedMs <= bins[i+1].SpeedMs {
			lo := bins[i]
			hi := bins[i+1]
			t := (speedMs - lo.SpeedMs) / (hi.SpeedMs - lo.SpeedMs)
			t = math.Max(0, math.Min(1, t))
			return getter(lo)*(1-t) + getter(hi)*t
		}
	}
	return getter(bins[len(bins)-1])
}

// verticalGFactor computes the grip multiplier from vertical load.
// Uses tire load sensitivity: grip scales as verticalG^(1-alpha).
func verticalGFactor(verticalG float64) float64 {
	const loadSensitivity = 0.1
	if verticalG <= 0.1 {
		verticalG = 0.1
	}
	return math.Pow(verticalG, 1.0-loadSensitivity)
}

func percentile(data []float64, p float64) float64 {
	if len(data) == 0 {
		return 0
	}
	sorted := make([]float64, len(data))
	copy(sorted, data)
	sort.Float64s(sorted)
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}
