package vbo

import (
	"bufio"
	"fmt"
	"time"
	"io"
	"math"
	"strconv"
	"strings"
)

// SplitGate represents a start/finish or split line defined by two GPS points.
type SplitGate struct {
	Type string // "Start" or "Split"
	Name string
	// Points are in VBO minutes format; convert with MinutesToDegrees.
	Lon1, Lat1 float64
	Lon2, Lat2 float64
}

// DataPoint is a single telemetry sample.
type DataPoint struct {
	Time       float64 // seconds since midnight (from HHMMSS.SS)
	Lat        float64 // decimal degrees
	Lon        float64 // decimal degrees
	SpeedKmh   float64
	Heading    float64
	HeightM    float64
	RPM        float64
	Gear       float64
	BrakePct   float64
	ThrottlePct float64
	SteeringDeg float64
	LatG       float64
	LongG      float64
	CoolantC   float64
	OilTempC   float64
}

// ParsedSession holds all data extracted from a VBO file.
type ParsedSession struct {
	SessionName string
	Notes       string
	FileDate    time.Time // date from "File created on" header
	SplitGates  []SplitGate
	Columns     []string
	Points      []DataPoint
	SampleRate  float64 // estimated Hz
}

// columnMap maps column names to their index in the data row.
type columnMap struct {
	time, lat, lon, velocity, heading, height int
	rpm, gear, brake, throttle, steering      int
	latG, longG, coolant, oilTemp             int
}

// Parse reads a VBO file and returns a ParsedSession.
func Parse(r io.Reader) (*ParsedSession, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	session := &ParsedSession{}
	section := ""

	for scanner.Scan() {
		line := scanner.Text()

		// Parse file creation date (first line: "File created on DD/MM/YYYY at HH:MM:SS")
		if strings.HasPrefix(line, "File created on ") {
			parts := strings.Fields(line)
			// parts: ["File", "created", "on", "14/03/2026", "at", "16:22:00"]
			if len(parts) >= 4 {
				if t, err := time.Parse("02/01/2006", parts[3]); err == nil {
					session.FileDate = t
				}
			}
			continue
		}

		// Detect section headers
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.Trim(line, "[]"))
			continue
		}

		switch section {
		case "session data":
			if strings.HasPrefix(line, "name ") {
				session.SessionName = strings.TrimPrefix(line, "name ")
			}
			if strings.HasPrefix(line, "notes ") {
				session.Notes = strings.TrimPrefix(line, "notes ")
			}

		case "laptiming":
			gate, err := parseSplitGate(line)
			if err == nil {
				session.SplitGates = append(session.SplitGates, gate)
			}

		case "column names":
			if strings.TrimSpace(line) != "" {
				session.Columns = strings.Fields(line)
			}

		case "data":
			if strings.TrimSpace(line) == "" {
				continue
			}
			if len(session.Columns) == 0 {
				return nil, fmt.Errorf("data section found before column names")
			}
			point, err := parseDataRow(line, session.Columns)
			if err != nil {
				continue // skip malformed rows
			}
			session.Points = append(session.Points, point)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading VBO: %w", err)
	}

	// Estimate sample rate
	if len(session.Points) >= 2 {
		first := session.Points[0].Time
		last := session.Points[len(session.Points)-1].Time
		duration := last - first
		if duration > 0 {
			session.SampleRate = float64(len(session.Points)-1) / duration
		}
	}

	return session, nil
}

// parseSplitGate parses a laptiming line like:
// Start   +7305.397740 +2195.187470 +7305.360162 +2195.232204 ¬ Start/Finish
func parseSplitGate(line string) (SplitGate, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return SplitGate{}, fmt.Errorf("empty line")
	}

	// Split on ¬ to get name
	parts := strings.SplitN(line, "¬", 2)
	name := ""
	if len(parts) == 2 {
		name = strings.TrimSpace(parts[1])
	}
	line = strings.TrimSpace(parts[0])

	fields := strings.Fields(line)
	if len(fields) < 5 {
		return SplitGate{}, fmt.Errorf("not enough fields: %d", len(fields))
	}

	gateType := fields[0]
	lon1, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return SplitGate{}, err
	}
	lat1, err := strconv.ParseFloat(fields[2], 64)
	if err != nil {
		return SplitGate{}, err
	}
	lon2, err := strconv.ParseFloat(fields[3], 64)
	if err != nil {
		return SplitGate{}, err
	}
	lat2, err := strconv.ParseFloat(fields[4], 64)
	if err != nil {
		return SplitGate{}, err
	}

	return SplitGate{
		Type: gateType,
		Name: name,
		Lon1: -MinutesToDegrees(lon1), // Western hemisphere
		Lat1: MinutesToDegrees(lat1),
		Lon2: -MinutesToDegrees(lon2),
		Lat2: MinutesToDegrees(lat2),
	}, nil
}

func parseDataRow(line string, columns []string) (DataPoint, error) {
	fields := strings.Fields(line)
	if len(fields) < len(columns) {
		return DataPoint{}, fmt.Errorf("row has %d fields, expected %d", len(fields), len(columns))
	}

	// Build index for first row (we could cache this, but it's fast enough)
	cm := buildColumnMap(columns)

	var p DataPoint

	// Time: HHMMSS.SS format
	if cm.time >= 0 {
		p.Time = parseHHMMSS(fields[cm.time+1]) // +1 because first field is sats count (not in column names? Actually sats IS first column)
	}
	// Actually, looking at the data: first field maps to first column name.
	// sats=014, time=162214.65, lat=+2195.181090, lon=+07305.322720, ...
	// columns: sats time lat long velocity heading height ...

	// Re-parse with correct mapping
	get := func(idx int) float64 {
		if idx < 0 || idx >= len(fields) {
			return 0
		}
		v, _ := strconv.ParseFloat(fields[idx], 64)
		return v
	}

	if cm.time >= 0 {
		p.Time = parseHHMMSS(fields[cm.time])
	}
	if cm.lat >= 0 {
		p.Lat = MinutesToDegrees(get(cm.lat))
	}
	if cm.lon >= 0 {
		p.Lon = -MinutesToDegrees(get(cm.lon)) // Western hemisphere = negative
	}
	if cm.velocity >= 0 {
		p.SpeedKmh = get(cm.velocity)
	}
	if cm.heading >= 0 {
		p.Heading = get(cm.heading)
	}
	if cm.height >= 0 {
		p.HeightM = get(cm.height)
	}
	if cm.rpm >= 0 {
		p.RPM = get(cm.rpm)
	}
	if cm.gear >= 0 {
		p.Gear = get(cm.gear)
	}
	if cm.brake >= 0 {
		p.BrakePct = get(cm.brake)
	}
	if cm.throttle >= 0 {
		p.ThrottlePct = get(cm.throttle)
	}
	if cm.steering >= 0 {
		p.SteeringDeg = get(cm.steering)
	}
	if cm.latG >= 0 {
		p.LatG = get(cm.latG)
	}
	if cm.longG >= 0 {
		p.LongG = get(cm.longG)
	}
	if cm.coolant >= 0 {
		p.CoolantC = get(cm.coolant)
	}
	if cm.oilTemp >= 0 {
		p.OilTempC = get(cm.oilTemp)
	}

	return p, nil
}

func buildColumnMap(columns []string) columnMap {
	cm := columnMap{
		time: -1, lat: -1, lon: -1, velocity: -1, heading: -1, height: -1,
		rpm: -1, gear: -1, brake: -1, throttle: -1, steering: -1,
		latG: -1, longG: -1, coolant: -1, oilTemp: -1,
	}

	// Priority: canbus > calc > plain for multi-source channels
	rpmPri, gearPri, brakePri, throttlePri := 0, 0, 0, 0
	steeringPri, latGPri, longGPri, coolantPri, oilPri := 0, 0, 0, 0, 0

	for i, col := range columns {
		lower := strings.ToLower(col)
		pri := sourcePriority(lower)

		switch {
		case lower == "time":
			cm.time = i
		case lower == "lat":
			cm.lat = i
		case lower == "long":
			cm.lon = i
		case lower == "heading":
			cm.heading = i
		case lower == "height":
			cm.height = i

		case strings.HasPrefix(lower, "velocity") || strings.HasPrefix(lower, "speed"):
			// Prefer canbus velocity
			if pri > 0 && (cm.velocity < 0 || pri > velocityPri(columns, cm.velocity)) {
				cm.velocity = i
			} else if cm.velocity < 0 {
				cm.velocity = i
			}

		case strings.HasPrefix(lower, "rpm"):
			if cm.rpm < 0 || pri > rpmPri {
				cm.rpm = i
				rpmPri = pri
			}
		case strings.HasPrefix(lower, "gear"):
			if cm.gear < 0 || pri > gearPri {
				cm.gear = i
				gearPri = pri
			}
		case strings.HasPrefix(lower, "brake_pos") || strings.HasPrefix(lower, "brake_pressure"):
			if cm.brake < 0 || pri > brakePri {
				cm.brake = i
				brakePri = pri
			}
		case strings.HasPrefix(lower, "accelerator_pos") || strings.HasPrefix(lower, "throttle"):
			if cm.throttle < 0 || pri > throttlePri {
				cm.throttle = i
				throttlePri = pri
			}
		case strings.HasPrefix(lower, "steering_angle"):
			if cm.steering < 0 || pri > steeringPri {
				cm.steering = i
				steeringPri = pri
			}
		case strings.HasPrefix(lower, "latacc"):
			if cm.latG < 0 || pri > latGPri {
				cm.latG = i
				latGPri = pri
			}
		case strings.HasPrefix(lower, "longacc"):
			if cm.longG < 0 || pri > longGPri {
				cm.longG = i
				longGPri = pri
			}
		case strings.HasPrefix(lower, "coolant_temp"):
			if cm.coolant < 0 || pri > coolantPri {
				cm.coolant = i
				coolantPri = pri
			}
		case strings.HasPrefix(lower, "engine_oil_temp") || strings.HasPrefix(lower, "oil_temp"):
			if cm.oilTemp < 0 || pri > oilPri {
				cm.oilTemp = i
				oilPri = pri
			}
		}
	}
	return cm
}

func sourcePriority(col string) int {
	switch {
	case strings.Contains(col, "-canbus"):
		return 3
	case strings.Contains(col, "-obd"):
		return 2
	case strings.Contains(col, "-calc"):
		return 1
	default:
		return 0
	}
}

func velocityPri(columns []string, idx int) int {
	if idx < 0 || idx >= len(columns) {
		return 0
	}
	return sourcePriority(strings.ToLower(columns[idx]))
}

// MinutesToDegrees converts VBO coordinate format (total minutes) to decimal degrees.
// VBO stores lat/lon as total arc-minutes: e.g. 2195.181 minutes = 36.5863°
func MinutesToDegrees(totalMinutes float64) float64 {
	sign := 1.0
	if totalMinutes < 0 {
		sign = -1.0
		totalMinutes = math.Abs(totalMinutes)
	}
	degrees := math.Floor(totalMinutes / 60.0)
	minutes := totalMinutes - degrees*60.0
	return sign * (degrees + minutes/60.0)
}

// parseHHMMSS converts "HHMMSS.SS" to seconds since midnight.
func parseHHMMSS(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	hours := math.Floor(v / 10000)
	remainder := v - hours*10000
	minutes := math.Floor(remainder / 100)
	seconds := remainder - minutes*100
	return hours*3600 + minutes*60 + seconds
}

// Haversine returns the distance in meters between two lat/lon points.
func Haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000 // Earth radius in meters
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * c
}
