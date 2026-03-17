package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"

	"github.com/iryzhkov/86-challenge/analysis"
	"github.com/iryzhkov/86-challenge/track"
	"github.com/iryzhkov/86-challenge/vbo"
)

func main() {
	var (
		vboPath     string
		vboURL      string
		carClass    string
		tireBrand   string
		tireModel   string
		lapNum      int
		utilization float64
	)

	flag.StringVar(&vboPath, "file", "", "path to VBO file")
	flag.StringVar(&vboURL, "url", "", "URL to download VBO file from (e.g. https://86.ryzhkov.dev/session/download/ID)")
	flag.StringVar(&carClass, "class", "stock", "car class")
	flag.StringVar(&tireBrand, "tire-brand", "Yokohama", "tire brand")
	flag.StringVar(&tireModel, "tire-model", "V601", "tire model")
	flag.IntVar(&lapNum, "lap", 0, "specific lap to simulate (0 = best valid lap)")
	flag.Float64Var(&utilization, "utilization", 0.95, "grip utilization factor (0.90-0.95 realistic, 1.0 for physics limit)")
	flag.Parse()

	if vboPath == "" && vboURL == "" {
		fmt.Fprintln(os.Stderr, "Usage: lapsim -file path.vbo  OR  lapsim -url https://86.ryzhkov.dev/session/download/ID")
		os.Exit(1)
	}

	var data []byte
	var err error

	if vboURL != "" {
		fmt.Fprintf(os.Stderr, "Downloading %s...\n", vboURL)
		resp, err := http.Get(vboURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Download failed: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		data, err = io.ReadAll(resp.Body)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Read failed: %v\n", err)
			os.Exit(1)
		}
	} else {
		data, err = os.ReadFile(vboPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Read failed: %v\n", err)
			os.Exit(1)
		}
	}

	parsed, err := vbo.Parse(bytes.NewReader(data))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Parse failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Parsed %d data points at %.0f Hz\n", len(parsed.Points), parsed.SampleRate)

	// Split laps
	laps := track.SplitLaps(parsed.Points, parsed.SplitGates)
	if len(laps) == 0 {
		fmt.Fprintln(os.Stderr, "No laps detected")
		os.Exit(1)
	}

	// Find valid laps
	var validLaps []track.LapResult
	for _, l := range laps {
		if l.IsValid {
			validLaps = append(validLaps, l)
		}
	}
	if len(validLaps) == 0 {
		fmt.Fprintln(os.Stderr, "No valid laps found")
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Found %d laps (%d valid)\n", len(laps), len(validLaps))

	// Step 1: Build grip envelope from ALL valid laps
	fmt.Fprintln(os.Stderr, "\n=== GRIP ANALYSIS ===")
	fmt.Fprintf(os.Stderr, "Car class: %s | Tires: %s %s\n", carClass, tireBrand, tireModel)

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

	grip := analysis.BuildGripEnvelope(gripPoints, carClass, tireBrand, tireModel)

	fmt.Fprintf(os.Stderr, "Max Lateral G:  %.3f\n", grip.MaxLatG)
	fmt.Fprintf(os.Stderr, "Max Braking G:  %.3f\n", grip.MaxBrakeG)
	fmt.Fprintf(os.Stderr, "Top speed:      %.0f km/h\n", grip.MaxSpeedMs*3.6)
	fmt.Fprintln(os.Stderr, "\nGrip by speed:")
	for _, sb := range grip.SpeedBins {
		if sb.Samples > 10 {
			fmt.Fprintf(os.Stderr, "  %3.0f km/h: latG=%.3f  brakeG=%.3f  (%d samples)\n",
				sb.SpeedMs*3.6, sb.MaxLatG, sb.MaxBrakeG, sb.Samples)
		}
	}
	fmt.Fprintf(os.Stderr, "Lat G rate:     %.1f G/s (yaw inertia / suspension response)\n", grip.LatGRate)
	fmt.Fprintln(os.Stderr, "\nAcceleration curve (from full-throttle straights):")
	for _, ap := range grip.AccelCurve {
		if ap.Samples > 0 || ap.AccelG > 0 {
			marker := ""
			if ap.Samples < 5 {
				marker = " (interpolated)"
			}
			fmt.Fprintf(os.Stderr, "  %3.0f km/h: %.3f G%s\n", ap.SpeedMs*3.6, ap.AccelG, marker)
		}
	}

	// Vehicle characteristics estimate
	allTrack := analysis.BuildTrackModel(parsed.Points, validLaps[0].StartIdx, validLaps[0].EndIdx)
	assumedWeight := analysis.ClassWeight(carClass, "BRZ")
	vehicle := analysis.EstimateVehicle(&grip, allTrack, assumedWeight)
	if vehicle.EstPowerHP > 0 {
		fmt.Fprintf(os.Stderr, "\nEstimated vehicle characteristics (assuming %.0f kg / %.0f lbs):\n", assumedWeight, assumedWeight*2.20462)
		fmt.Fprintf(os.Stderr, "  Wheel HP:  %.0f hp\n", vehicle.EstPowerHP)
		fmt.Fprintf(os.Stderr, "  CdA:       %.2f m²\n", vehicle.CdA)
		fmt.Fprintf(os.Stderr, "  Top speed: %.0f mph\n", vehicle.TopSpeedKmh/1.60934)
	}

	// Step 2: Select lap to simulate
	var targetLap track.LapResult
	if lapNum > 0 {
		for _, l := range laps {
			if l.LapNumber == lapNum {
				targetLap = l
				break
			}
		}
		if targetLap.LapNumber == 0 {
			fmt.Fprintf(os.Stderr, "Lap %d not found\n", lapNum)
			os.Exit(1)
		}
	} else {
		// Use best valid lap
		targetLap = validLaps[0]
		for _, l := range validLaps[1:] {
			if l.LapTimeMs < targetLap.LapTimeMs {
				targetLap = l
			}
		}
	}

	fmt.Fprintf(os.Stderr, "\n=== SIMULATING LAP %d (actual: %s) ===\n",
		targetLap.LapNumber, formatTime(targetLap.LapTimeMs))

	// Step 3: Build track model from this lap
	trackModel := analysis.BuildTrackModel(parsed.Points, targetLap.StartIdx, targetLap.EndIdx)
	if len(trackModel) == 0 {
		fmt.Fprintln(os.Stderr, "Failed to build track model")
		os.Exit(1)
	}

	totalDist := trackModel[len(trackModel)-1].Distance
	fmt.Fprintf(os.Stderr, "Track length: %.0f m\n", totalDist)

	// Show vertical G highlights
	var minVG, maxVG float64 = 1, 1
	for _, tp := range trackModel {
		if tp.VerticalG < minVG {
			minVG = tp.VerticalG
		}
		if tp.VerticalG > maxVG {
			maxVG = tp.VerticalG
		}
	}
	fmt.Fprintf(os.Stderr, "Vertical G range: %.2f (crest) to %.2f (compression)\n", minVG, maxVG)

	fmt.Fprintf(os.Stderr, "Grip utilization: %.0f%%\n", utilization*100)

	// Step 4: Run simulation
	result := analysis.Simulate(trackModel, &grip, utilization)

	fmt.Fprintln(os.Stderr, "\n=== RESULTS ===")
	fmt.Fprintf(os.Stderr, "Actual best lap:     %s\n", formatTime(targetLap.LapTimeMs))
	fmt.Fprintf(os.Stderr, "Theoretical best:    %s\n", formatTime(result.TheoreticalLapTimeMs))
	fmt.Fprintf(os.Stderr, "Delta:               %s (%.1f%%)\n",
		formatDelta(result.DeltaMs), result.DeltaPct)

	// Segment breakdown
	fmt.Fprintln(os.Stderr, "\nSegment breakdown:")
	fmt.Fprintf(os.Stderr, "%-8s %8s %8s %8s %8s  %s\n",
		"Segment", "Dist(m)", "Actual", "Sim", "Delta", "Bottleneck")
	fmt.Fprintln(os.Stderr, strings.Repeat("-", 60))
	for _, seg := range result.Segments {
		fmt.Fprintf(os.Stderr, "%-8s %7.0f %8s %8s %8s  %s\n",
			seg.Name,
			seg.EndDist-seg.StartDist,
			formatTime(seg.ActualMs),
			formatTime(seg.SimMs),
			formatDelta(seg.DeltaMs),
			seg.Bottleneck)
	}

	// Find biggest speed deltas (where driver leaves the most time)
	fmt.Fprintln(os.Stderr, "\nBiggest opportunities (where you can go faster):")
	type opportunity struct {
		dist    float64
		deltaKmh float64
		speed   float64
	}
	var opps []opportunity
	windowSize := len(trackModel) / 20 // ~5% of track per window
	if windowSize < 5 {
		windowSize = 5
	}
	for i := windowSize; i < len(trackModel)-windowSize; i += windowSize {
		var avgDelta float64
		for j := i - windowSize/2; j < i+windowSize/2 && j < len(trackModel); j++ {
			avgDelta += result.SpeedDeltaMs[j]
		}
		avgDelta /= float64(windowSize)
		opps = append(opps, opportunity{
			dist:    trackModel[i].Distance,
			deltaKmh: avgDelta * 3.6,
			speed:   trackModel[i].SpeedMs * 3.6,
		})
	}

	// Show top 5 opportunities
	// Sort by delta descending
	for i := 0; i < len(opps); i++ {
		for j := i + 1; j < len(opps); j++ {
			if opps[j].deltaKmh > opps[i].deltaKmh {
				opps[i], opps[j] = opps[j], opps[i]
			}
		}
	}
	count := 5
	if count > len(opps) {
		count = len(opps)
	}
	for i := 0; i < count; i++ {
		o := opps[i]
		pctAround := o.dist / totalDist * 100
		fmt.Fprintf(os.Stderr, "  @ %.0f m (%.0f%% of track): +%.1f km/h possible (actual: %.0f km/h)\n",
			o.dist, pctAround, o.deltaKmh, o.speed)
	}

	// Print CSV to stdout for further analysis
	fmt.Println("distance_m,actual_speed_kmh,sim_speed_kmh,delta_kmh,curvature,vertical_g,height_m,lat_g,long_g")
	for i, tp := range trackModel {
		fmt.Printf("%.1f,%.1f,%.1f,%.1f,%.6f,%.3f,%.1f,%.3f,%.3f\n",
			tp.Distance,
			tp.SpeedMs*3.6,
			result.SimSpeedsMs[i]*3.6,
			result.SpeedDeltaMs[i]*3.6,
			tp.Curvature,
			tp.VerticalG,
			tp.HeightM,
			tp.LatG,
			tp.LongG,
		)
	}
}

func formatTime(ms int) string {
	if ms < 0 {
		return fmt.Sprintf("-%d:%02d.%03d", -ms/60000, (-ms/1000)%60, (-ms)%1000)
	}
	return fmt.Sprintf("%d:%02d.%03d", ms/60000, (ms/1000)%60, ms%1000)
}

func formatDelta(ms int) string {
	sign := "+"
	if ms < 0 {
		sign = "-"
		ms = int(math.Abs(float64(ms)))
	}
	return fmt.Sprintf("%s%d.%03d", sign, ms/1000, ms%1000)
}
