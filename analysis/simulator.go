package analysis

import (
	"fmt"
	"math"
)

const g = 9.81 // m/s²

// SimResult holds the output of the lap simulation.
type SimResult struct {
	TheoreticalLapTimeMs int
	ActualBestLapTimeMs  int
	DeltaMs              int
	DeltaPct             float64

	// Per-segment analysis
	Segments []SegmentResult

	// Speed profile: simulated vs actual
	TrackPoints   []TrackPoint
	SimSpeedsMs   []float64
	ActualSpeedsMs []float64
	SpeedDeltaMs  []float64 // positive = driver is slower than theoretical
}

// SegmentResult shows time delta for a track segment.
type SegmentResult struct {
	Name       string
	StartDist  float64
	EndDist    float64
	ActualMs   int
	SimMs      int
	DeltaMs    int
	Bottleneck string // "braking", "cornering", "acceleration"
}

// Simulate runs a forward-backward quasi-static lap simulation.
//
// The approach:
// 1. At each track point, compute max cornering speed from curvature and grip
// 2. Backward pass: from each point, compute max entry speed (braking limit)
// 3. Forward pass: from each point, compute max exit speed (acceleration limit)
// 4. The achievable speed is the minimum of all three constraints
// 5. Integrate speed over distance for lap time
// utilization is the fraction of the friction circle a driver can use (0.85-0.95
// for humans, 1.0 for physics limit). Even the best drivers can't use 100% of
// available grip at every instant — they need margin for corrections, transitions,
// and the fact that tire grip isn't perfectly predictable.
func Simulate(track []TrackPoint, grip *GripEnvelope, utilization float64) SimResult {
	n := len(track)
	if n < 10 {
		return SimResult{}
	}

	totalDist := track[n-1].Distance

	// Top speed: car can't exceed what the engine/drag allows
	topSpeed := grip.MaxSpeedMs
	if topSpeed < 10 {
		topSpeed = 50 // fallback
	}

	// Step 1: Max cornering speed at each point
	cornerSpeed := make([]float64, n)
	for i, tp := range track {
		absCurv := math.Abs(tp.Curvature)
		if absCurv < 0.0001 {
			cornerSpeed[i] = topSpeed
		} else {
			// v_max = sqrt(a_lat * g / curvature), scaled by utilization
			maxLat := grip.MaxLatGAtSpeed(tp.SpeedMs, tp.VerticalG) * utilization
			cornerSpeed[i] = math.Sqrt(maxLat * g / absCurv)
		}
		if cornerSpeed[i] > topSpeed {
			cornerSpeed[i] = topSpeed
		}
	}

	// Step 2: Backward pass (braking limit)
	// Starting from each point, what's the max speed you can arrive at
	// if you need to slow down to the next point's corner speed?
	brakeSpeed := make([]float64, n)
	brakeSpeed[n-1] = cornerSpeed[n-1]
	for i := n - 2; i >= 0; i-- {
		ds := track[i+1].Distance - track[i].Distance
		if ds < 0.01 {
			brakeSpeed[i] = brakeSpeed[i+1]
			continue
		}

		vNext := math.Min(brakeSpeed[i+1], cornerSpeed[i+1])
		dh := track[i+1].HeightM - track[i].HeightM // positive = uphill (helps braking)
		gradientBoost := dh / ds * g                   // longitudinal acceleration from gradient

		maxBrake := grip.MaxBrakeGAtSpeed(vNext, track[i].VerticalG) * utilization

		// Friction circle: total G can't exceed tire grip limit
		absCurv := math.Abs(track[i].Curvature)
		if absCurv > 0.0001 && vNext > 1 {
			latGUsed := vNext * vNext * absCurv / g
			maxGrip := grip.MaxLatGAtSpeed(vNext, track[i].VerticalG) * utilization
			gripLimitedBrake := math.Sqrt(math.Max(0, maxGrip*maxGrip-latGUsed*latGUsed))
			if gripLimitedBrake < maxBrake {
				maxBrake = gripLimitedBrake
			}
		}

		totalDecel := (maxBrake + gradientBoost/g) * g
		// v² = vNext² + 2 * a * ds (kinematics, going backward)
		vSq := vNext*vNext + 2*totalDecel*ds
		if vSq < 0 {
			vSq = 0
		}
		brakeSpeed[i] = math.Sqrt(vSq)
	}

	// Also propagate braking from start to the end (lap is circular)
	// The speed at the start must equal the speed at the end
	startSpeed := math.Min(brakeSpeed[0], cornerSpeed[0])
	brakeSpeed[n-1] = math.Min(brakeSpeed[n-1], startSpeed)
	for i := n - 2; i >= 0; i-- {
		ds := track[i+1].Distance - track[i].Distance
		if ds < 0.01 {
			continue
		}
		vNext := math.Min(brakeSpeed[i+1], cornerSpeed[i+1])
		maxBrake := grip.MaxBrakeGAtSpeed(vNext, track[i].VerticalG) * utilization
		dh := track[i+1].HeightM - track[i].HeightM
		gradientBoost := dh / ds * g
		totalDecel := (maxBrake + gradientBoost/g) * g
		vSq := vNext*vNext + 2*totalDecel*ds
		if vSq < 0 {
			vSq = 0
		}
		v := math.Sqrt(vSq)
		if v < brakeSpeed[i] {
			brakeSpeed[i] = v
		}
	}

	// Step 3: Forward pass (acceleration limit)
	// The lap is circular: speed at start must match speed at end.
	// We iterate the forward pass until the start speed converges.
	accelSpeed := make([]float64, n)
	startSpeed = math.Min(cornerSpeed[0], brakeSpeed[0])

	for iter := 0; iter < 3; iter++ {
		accelSpeed[0] = startSpeed
		for i := 1; i < n; i++ {
			ds := track[i].Distance - track[i-1].Distance
			if ds < 0.01 {
				accelSpeed[i] = accelSpeed[i-1]
				continue
			}

			vPrev := math.Min(accelSpeed[i-1], math.Min(cornerSpeed[i-1], brakeSpeed[i-1]))
			dh := track[i].HeightM - track[i-1].HeightM
			gradientDrag := dh / ds * g

			maxAccel := grip.MaxAccelGAtSpeed(vPrev, track[i].VerticalG) * utilization

			// Friction circle: combined G can't exceed tire grip
			absCurv := math.Abs(track[i].Curvature)
			if absCurv > 0.0001 && vPrev > 1 {
				latGUsed := vPrev * vPrev * absCurv / g
				maxGrip := grip.MaxLatGAtSpeed(vPrev, track[i].VerticalG) * utilization
				gripLimitedAccel := math.Sqrt(math.Max(0, maxGrip*maxGrip-latGUsed*latGUsed))
				if gripLimitedAccel < maxAccel {
					maxAccel = gripLimitedAccel
				}
			}

			totalAccel := (maxAccel - gradientDrag/g) * g
			if totalAccel < 0 {
				totalAccel = 0
			}
			vSq := vPrev*vPrev + 2*totalAccel*ds
			if vSq < 0 {
				vSq = 0
			}
			accelSpeed[i] = math.Sqrt(vSq)
			if accelSpeed[i] > topSpeed {
				accelSpeed[i] = topSpeed
			}
		}

		// Use the end-of-lap speed as the new start speed for the next iteration
		endSpeed := math.Min(accelSpeed[n-1], math.Min(cornerSpeed[n-1], brakeSpeed[n-1]))
		if endSpeed < startSpeed {
			startSpeed = endSpeed
		}
	}

	// Step 4: Final speed = min of all constraints
	simSpeed := make([]float64, n)
	for i := range simSpeed {
		simSpeed[i] = math.Min(cornerSpeed[i], math.Min(brakeSpeed[i], accelSpeed[i]))
		if simSpeed[i] < 1 {
			simSpeed[i] = 1
		}
		if simSpeed[i] > topSpeed {
			simSpeed[i] = topSpeed
		}
	}

	// Step 5: Lateral G rate limit (yaw inertia / weight transfer settling)
	// Measured from the actual telemetry — reflects the car's real suspension
	// response, tire slip angle build-up, and yaw inertia. A car with stiffer
	// suspension and stickier tires will have a higher rate.
	if grip.LatGRate > 0 {
		applyLatGRateLimit(simSpeed, track, cornerSpeed, brakeSpeed, grip.LatGRate, topSpeed)
	}

	// Step 5: Integrate for lap time
	simTimeSec := 0.0
	actualTimeSec := 0.0
	for i := 1; i < n; i++ {
		ds := track[i].Distance - track[i-1].Distance
		if ds < 0.01 {
			continue
		}
		avgSimSpeed := (simSpeed[i] + simSpeed[i-1]) / 2
		avgActSpeed := (track[i].SpeedMs + track[i-1].SpeedMs) / 2
		if avgSimSpeed > 0 {
			simTimeSec += ds / avgSimSpeed
		}
		if avgActSpeed > 0 {
			actualTimeSec += ds / avgActSpeed
		}
	}

	result := SimResult{
		TheoreticalLapTimeMs: int(simTimeSec * 1000),
		ActualBestLapTimeMs:  int(actualTimeSec * 1000),
		DeltaMs:              int(actualTimeSec*1000) - int(simTimeSec*1000),
		TrackPoints:          track,
		SimSpeedsMs:          simSpeed,
	}

	if result.TheoreticalLapTimeMs > 0 {
		result.DeltaPct = float64(result.DeltaMs) / float64(result.TheoreticalLapTimeMs) * 100
	}

	// Build actual speed array
	result.ActualSpeedsMs = make([]float64, n)
	result.SpeedDeltaMs = make([]float64, n)
	for i, tp := range track {
		result.ActualSpeedsMs[i] = tp.SpeedMs
		result.SpeedDeltaMs[i] = simSpeed[i] - tp.SpeedMs // positive = driver can go faster
	}

	// Segment analysis: divide track into ~10 segments
	numSegs := 10
	segLen := totalDist / float64(numSegs)
	for s := 0; s < numSegs; s++ {
		segStart := float64(s) * segLen
		segEnd := float64(s+1) * segLen
		var segSimTime, segActTime float64
		bottleneck := ""
		var maxBrakeDeficit, maxCornerDeficit, maxAccelDeficit float64

		for i := 1; i < n; i++ {
			if track[i].Distance < segStart || track[i-1].Distance > segEnd {
				continue
			}
			ds := track[i].Distance - track[i-1].Distance
			avgSim := (simSpeed[i] + simSpeed[i-1]) / 2
			avgAct := (track[i].SpeedMs + track[i-1].SpeedMs) / 2
			if avgSim > 0 {
				segSimTime += ds / avgSim
			}
			if avgAct > 0 {
				segActTime += ds / avgAct
			}

			// Determine what's limiting
			deficit := simSpeed[i] - track[i].SpeedMs
			if cornerSpeed[i] <= brakeSpeed[i] && cornerSpeed[i] <= accelSpeed[i] {
				if deficit > maxCornerDeficit {
					maxCornerDeficit = deficit
				}
			} else if brakeSpeed[i] <= accelSpeed[i] {
				if deficit > maxBrakeDeficit {
					maxBrakeDeficit = deficit
				}
			} else {
				if deficit > maxAccelDeficit {
					maxAccelDeficit = deficit
				}
			}
		}

		switch {
		case maxCornerDeficit >= maxBrakeDeficit && maxCornerDeficit >= maxAccelDeficit:
			bottleneck = "cornering"
		case maxBrakeDeficit >= maxAccelDeficit:
			bottleneck = "braking"
		default:
			bottleneck = "acceleration"
		}

		result.Segments = append(result.Segments, SegmentResult{
			Name:       fmt.Sprintf("Seg %d", s+1),
			StartDist:  segStart,
			EndDist:    segEnd,
			ActualMs:   int(segActTime * 1000),
			SimMs:      int(segSimTime * 1000),
			DeltaMs:    int(segActTime*1000) - int(segSimTime*1000),
			Bottleneck: bottleneck,
		})
	}

	return result
}

// applyLatGRateLimit constrains how quickly lateral G can change, modeling
// yaw inertia and weight transfer settling time. The car can't instantly
// go from straight-line to full cornering — it takes time to build slip
// angle, rotate the chassis, and settle the suspension.
//
// This is applied as iterative forward/backward passes on the speed profile.
// At each point, the lateral G implied by speed and curvature is computed.
// If the lateral G change rate exceeds the limit, speed is reduced so the
// car enters the corner more gradually.
// applyLatGRateLimit constrains how quickly lateral G can change.
//
// The key insight: at corner entry, the car is braking AND needs to start
// rotating. The lateral G the car needs to achieve is determined by the
// curvature and the TARGET speed (corner speed), not the current higher
// braking speed. So we track the "demanded" lateral G from the curvature
// and limit how fast this demand can ramp up.
//
// This affects corner entry (must brake earlier to have time to rotate)
// and corner exit (can't instantly unwind and accelerate).
func applyLatGRateLimit(simSpeed []float64, track []TrackPoint, cornerSpeed, brakeSpeed []float64, maxLatGRate, topSpeed float64) {
	n := len(simSpeed)
	if n < 10 {
		return
	}

	// Compute the "demanded" lateral G at each point: what the corner
	// requires at the sim speed. This is the actual lateral load.
	demandedLatG := make([]float64, n)
	for i := range demandedLatG {
		v := simSpeed[i]
		k := math.Abs(track[i].Curvature)
		demandedLatG[i] = v * v * k / g
	}

	// Forward pass: entering corners — lateral G can't build up faster
	// than the car can physically rotate and transfer weight.
	changed := true
	for pass := 0; pass < 3 && changed; pass++ {
		changed = false
		for i := 1; i < n; i++ {
			ds := track[i].Distance - track[i-1].Distance
			if ds < 0.01 {
				continue
			}
			avgV := (simSpeed[i] + simSpeed[i-1]) / 2
			dt := ds / math.Max(avgV, 1)
			maxChange := maxLatGRate * dt

			if demandedLatG[i] > demandedLatG[i-1]+maxChange {
				// Must limit speed so lateral G doesn't ramp too fast
				targetLatG := demandedLatG[i-1] + maxChange
				k := math.Abs(track[i].Curvature)
				if k > 0.0001 {
					maxV := math.Sqrt(targetLatG * g / k)
					if maxV < simSpeed[i] {
						simSpeed[i] = maxV
						demandedLatG[i] = targetLatG
						changed = true
					}
				}
			}
		}

		// Backward pass: exiting corners
		for i := n - 2; i >= 0; i-- {
			ds := track[i+1].Distance - track[i].Distance
			if ds < 0.01 {
				continue
			}
			avgV := (simSpeed[i] + simSpeed[i+1]) / 2
			dt := ds / math.Max(avgV, 1)
			maxChange := maxLatGRate * dt

			if demandedLatG[i] > demandedLatG[i+1]+maxChange {
				targetLatG := demandedLatG[i+1] + maxChange
				k := math.Abs(track[i].Curvature)
				if k > 0.0001 {
					maxV := math.Sqrt(targetLatG * g / k)
					if maxV < simSpeed[i] {
						simSpeed[i] = maxV
						demandedLatG[i] = targetLatG
						changed = true
					}
				}
			}
		}
	}

	// Clamp
	for i := range simSpeed {
		if simSpeed[i] < 1 {
			simSpeed[i] = 1
		}
		if simSpeed[i] > topSpeed {
			simSpeed[i] = topSpeed
		}
	}
}
