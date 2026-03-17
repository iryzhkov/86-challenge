package analysis

import (
	"math"
)

// VehicleEstimate holds estimated car characteristics derived from telemetry.
type VehicleEstimate struct {
	PowerToWeightHP float64 // hp per kg (at wheels)
	EstPowerHP      float64 // estimated wheel horsepower (assuming known weight)
	EstWeightKg     float64 // assumed weight (from car class)
	CdA             float64 // drag coefficient * frontal area (m²)
	TopSpeedKmh     float64 // observed top speed
	PowerCurve      []PowerPoint
}

// PowerPoint is wheel power at a specific speed.
type PowerPoint struct {
	SpeedKmh float64
	PowerHP  float64
}

// EstimateVehicle derives car characteristics from the acceleration curve
// and telemetry data.
//
// Physics: at full throttle on a straight, the forces are:
//   F_engine = m * a + F_drag + F_rolling
//   P_wheels = F_engine * v
//
// Where:
//   F_drag = 0.5 * Cd * A * rho * v²
//   F_rolling = Crr * m * g
//
// We fit for P/m (power-to-weight) and CdA/m from the acceleration curve:
//   a(v) = P/(m*v) - (CdA*rho)/(2m) * v² - Crr*g
//
// With gradient data, uphill/downhill acceleration differences validate
// the model since: delta_a = g * delta_gradient
func EstimateVehicle(grip *GripEnvelope, trackPoints []TrackPoint, assumedWeightKg float64) VehicleEstimate {
	const (
		rho = 1.225 // air density kg/m³
		crr = 0.015 // rolling resistance coefficient (street tires)
		grav = 9.81
	)

	est := VehicleEstimate{
		EstWeightKg: assumedWeightKg,
		TopSpeedKmh: grip.MaxSpeedMs * 3.6,
	}

	// Collect full-throttle data points: speed, acceleration, gradient
	// from the track points where throttle is high and steering is low
	type dataPoint struct {
		speedMs float64
		accelG  float64
		gradient float64
	}
	var data []dataPoint

	for i := 1; i < len(trackPoints)-1; i++ {
		tp := trackPoints[i]
		if tp.ThrottlePct < 80 || math.Abs(tp.SteeringDeg) > 15 || tp.BrakePct > 2 {
			continue
		}
		if tp.SpeedMs < 10 {
			continue
		}
		// Skip gear changes (check for acceleration dips)
		if tp.LongG < 0 {
			continue
		}
		// Check gear consistency with neighbors
		prev := trackPoints[i-1]
		next := trackPoints[i+1]
		if tp.Gear != prev.Gear || tp.Gear != next.Gear {
			continue
		}

		data = append(data, dataPoint{
			speedMs:  tp.SpeedMs,
			accelG:   tp.LongG,
			gradient: tp.Gradient,
		})
	}

	if len(data) < 20 {
		return est
	}

	// Fit: a(v) + Crr*g + g*gradient = P/(m*v) - CdA*rho/(2m) * v²
	// Let y = (a + Crr*g + g*gradient) * g  [convert to m/s²... wait, a is in G]
	// Actually a is in G, so a*g = acceleration in m/s²
	// y = a*g + Crr*g² + g²*gradient
	// y = P/(m*v) - CdA*rho/(2m) * v²
	//
	// Linear regression: y = c1 * (1/v) + c2 * v²
	// where c1 = P/m, c2 = -CdA*rho/(2m)

	var sumX1Y, sumX2Y, sumX1X1, sumX2X2, sumX1X2 float64
	for _, d := range data {
		v := d.speedMs
		y := d.accelG*grav + crr*grav + grav*d.gradient
		x1 := 1.0 / v
		x2 := v * v

		sumX1Y += x1 * y
		sumX2Y += x2 * y
		sumX1X1 += x1 * x1
		sumX2X2 += x2 * x2
		sumX1X2 += x1 * x2
	}

	// Solve 2x2 linear system: [X1X1 X1X2; X1X2 X2X2] * [c1; c2] = [X1Y; X2Y]
	det := sumX1X1*sumX2X2 - sumX1X2*sumX1X2
	if math.Abs(det) < 1e-10 {
		return est
	}

	c1 := (sumX2X2*sumX1Y - sumX1X2*sumX2Y) / det // P/m in W/kg
	c2 := (sumX1X1*sumX2Y - sumX1X2*sumX1Y) / det // -CdA*rho/(2m) in 1/m

	// P/m = c1 (watts per kg)
	powerToWeightW := c1
	est.PowerToWeightHP = powerToWeightW / 745.7 // convert to hp/kg

	// CdA/m = -2*c2/rho
	cdaPerMass := -2.0 * c2 / rho
	est.CdA = cdaPerMass * assumedWeightKg

	// Estimated power at wheels
	est.EstPowerHP = est.PowerToWeightHP * assumedWeightKg

	// Build power curve: P(v) = (a(v)*g + Crr*g + drag/m) * m * v
	// Use the acceleration curve bins
	for _, ap := range grip.AccelCurve {
		if ap.AccelG <= 0 || ap.Samples < 3 {
			continue
		}
		v := ap.SpeedMs
		// Wheel power = (acceleration + rolling resistance + aero drag) * mass * speed
		dragForcePerMass := 0.5 * rho * cdaPerMass * v * v
		totalForcePerMass := ap.AccelG*grav + crr*grav + dragForcePerMass
		powerW := totalForcePerMass * assumedWeightKg * v
		powerHP := powerW / 745.7

		est.PowerCurve = append(est.PowerCurve, PowerPoint{
			SpeedKmh: v * 3.6,
			PowerHP:  powerHP,
		})
	}

	// Sanity checks
	if est.EstPowerHP < 50 || est.EstPowerHP > 1000 {
		est.EstPowerHP = 0
	}
	if est.CdA < 0.05 || est.CdA > 3.0 {
		est.CdA = 0
	}

	return est
}

// ClassWeight returns an assumed curb weight for a car class.
func ClassWeight(carClass, carModel string) float64 {
	// GR86/BRZ/FR-S base weights
	switch {
	case carModel == "BRZ" || carModel == "GR86" || carModel == "86" || carModel == "FR-S":
		return 1290 // kg, roughly 2844 lbs
	default:
		return 1300 // generic lightweight sports car
	}
}
