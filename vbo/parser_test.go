package vbo

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestMinutesToDegrees(t *testing.T) {
	// Laguna Seca lat: 2195.181 minutes → ~36.586° N
	lat := MinutesToDegrees(2195.181)
	if math.Abs(lat-36.5863) > 0.001 {
		t.Errorf("lat = %f, want ~36.5863", lat)
	}

	// Laguna Seca lon: 7305.322 minutes → ~121.755° (before negation)
	lon := MinutesToDegrees(7305.322)
	if math.Abs(lon-121.7554) > 0.001 {
		t.Errorf("lon = %f, want ~121.7554", lon)
	}
}

func TestParseHHMMSS(t *testing.T) {
	got := parseHHMMSS("162214.65")
	want := 16*3600 + 22*60 + 14.65
	if math.Abs(got-want) > 0.01 {
		t.Errorf("parseHHMMSS = %f, want %f", got, want)
	}
}

func TestParseAllVBOFiles(t *testing.T) {
	home := os.Getenv("HOME")
	files, err := filepath.Glob(home + "/Downloads/session_*.vbo")
	if err != nil || len(files) == 0 {
		t.Skip("No VBO files found in ~/Downloads")
	}

	for _, path := range files {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			f, err := os.Open(path)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer f.Close()

			session, err := Parse(f)
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}

			t.Logf("Session: %q, FileDate: %s", session.SessionName, session.FileDate.Format("2006-01-02"))
			t.Logf("Points: %d, Sample rate: %.1f Hz", len(session.Points), session.SampleRate)
			t.Logf("Split gates: %d", len(session.SplitGates))

			if len(session.Points) < 100 {
				t.Errorf("too few points: %d", len(session.Points))
			}

			if session.SessionName == "" {
				t.Error("empty session name")
			}

			// Check coordinates are reasonable (US West Coast)
			p := session.Points[len(session.Points)/2] // mid-session point
			t.Logf("Mid-session: lat=%.4f lon=%.4f speed=%.1f rpm=%.0f", p.Lat, p.Lon, p.SpeedKmh, p.RPM)

			if p.Lat < 34 || p.Lat > 41 {
				t.Errorf("lat %.4f outside California range", p.Lat)
			}
			if p.Lon > -118 || p.Lon < -124 {
				t.Errorf("lon %.4f outside California range", p.Lon)
			}

			// Verify gates have negative longitudes
			for i, g := range session.SplitGates {
				t.Logf("Gate %d: type=%s name=%q lat1=%.4f lon1=%.4f", i, g.Type, g.Name, g.Lat1, g.Lon1)
				if g.Lon1 > 0 || g.Lon2 > 0 {
					t.Errorf("gate %d has positive longitude (missing negation?)", i)
				}
			}

			// Check CAN bus data exists somewhere in the session
			hasSpeed := false
			for _, pt := range session.Points {
				if pt.SpeedKmh > 50 {
					hasSpeed = true
					break
				}
			}
			if !hasSpeed {
				t.Error("no points with speed > 50 km/h found")
			}
		})
	}
}
