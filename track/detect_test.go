package track

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/iryzhkov/86-challenge/vbo"
)

// Expected results for each file
var expectedTracks = map[string]struct {
	name   string
	config string
}{
	"session_20260314_092214_laguna_seca_start.vbo":                  {"Laguna Seca", "Full"},
	"session_20251129_132831_sonoma_resume2.vbo":                     {"Sonoma Raceway", ""},
	"session_20260214_113220_thunderhill_east_cyclone_resume2.vbo":   {"Thunderhill", ""},
	"session_20260227_114135_thunderhill_east_resume3.vbo":           {"Thunderhill", ""},
}

func TestDetectAllTracks(t *testing.T) {
	home := os.Getenv("HOME")
	files, err := filepath.Glob(home + "/Downloads/session_*.vbo")
	if err != nil || len(files) == 0 {
		t.Skip("No VBO files found")
	}

	for _, path := range files {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			f, err := os.Open(path)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer f.Close()

			session, err := vbo.Parse(f)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}

			trackName, trackConfig, found := DetectTrack(session.Points)
			t.Logf("Detected: %s %s (found=%v)", trackName, trackConfig, found)

			if !found {
				t.Fatal("Track not detected")
			}

			if expected, ok := expectedTracks[name]; ok {
				if expected.name != "" && trackName != expected.name {
					t.Errorf("track = %q, want %q", trackName, expected.name)
				}
			}
		})
	}
}

func TestSplitAllLaps(t *testing.T) {
	home := os.Getenv("HOME")
	files, err := filepath.Glob(home + "/Downloads/session_*.vbo")
	if err != nil || len(files) == 0 {
		t.Skip("No VBO files found")
	}

	for _, path := range files {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			f, err := os.Open(path)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer f.Close()

			session, err := vbo.Parse(f)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}

			laps := SplitLaps(session.Points, session.SplitGates)
			t.Logf("Found %d laps", len(laps))

			validCount := 0
			bestTime := 999999
			for _, lap := range laps {
				status := "valid"
				if lap.IsOutlap {
					status = "OUT"
				} else if lap.IsInlap {
					status = "IN"
				} else if !lap.IsValid {
					status = "invalid"
				}
				mins := lap.LapTimeMs / 60000
				secs := (lap.LapTimeMs % 60000) / 1000
				ms := lap.LapTimeMs % 1000
				t.Logf("  Lap %2d: %d:%02d.%03d [%s] splits=%v",
					lap.LapNumber, mins, secs, ms, status, lap.SplitTimes)

				if lap.IsValid {
					validCount++
					if lap.LapTimeMs < bestTime {
						bestTime = lap.LapTimeMs
					}
				}
			}

			if len(laps) == 0 {
				t.Error("No laps detected")
			}

			t.Logf("Valid laps: %d, Best: %d:%02d.%03d",
				validCount, bestTime/60000, (bestTime%60000)/1000, bestTime%1000)

			if validCount == 0 {
				t.Error("No valid laps found")
			}
		})
	}
}
