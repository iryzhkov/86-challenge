package models

import (
	"context"
	"fmt"

	"github.com/iryzhkov/86-challenge/vbo"
)

type Lap struct {
	ID            int
	SessionID     string
	LapNumber     int
	LapTimeMs     int
	IsValid       bool
	IsOutlap      bool
	IsInlap       bool
	SplitTimesMs  []int
	AmbientTempF  float32
	// Joined
	DriverName string
	TrackName  string
}

type Telemetry struct {
	ID         int
	LapID      int
	SampleRate float64
	Timestamps []int
	Latitudes  []float64
	Longitudes []float64
	SpeedsKmh  []float32
	DistancesM []float32
	RPMs       []float32
	Gear       []float32
	BrakePct   []float32
	ThrottlePct []float32
	SteeringDeg []float32
	LatG       []float32
	LongG      []float32
	CoolantC   []float32
	OilTempC   []float32
}

func CreateLap(sessionID string, lapNumber, lapTimeMs int, isValid, isOutlap, isInlap bool, splitTimes []int, ambientTempF float32) (int, error) {
	var id int
	var ambientPtr *float32
	if ambientTempF > 0 {
		ambientPtr = &ambientTempF
	}
	err := DB.QueryRow(context.Background(),
		`INSERT INTO laps (session_id, lap_number, lap_time_ms, is_valid, is_outlap, is_inlap, split_times_ms, ambient_temp_f)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id`,
		sessionID, lapNumber, lapTimeMs, isValid, isOutlap, isInlap, splitTimes, ambientPtr).Scan(&id)
	return id, err
}

func StoreTelemetry(lapID int, sampleRate float64, points []vbo.DataPoint, startIdx, endIdx int) error {
	if startIdx >= endIdx || endIdx > len(points) {
		return fmt.Errorf("invalid index range: %d-%d", startIdx, endIdx)
	}

	lapPoints := points[startIdx:endIdx]
	n := len(lapPoints)

	timestamps := make([]int, n)
	lats := make([]float64, n)
	lons := make([]float64, n)
	speeds := make([]float32, n)
	distances := make([]float32, n)
	rpms := make([]float32, n)
	gear := make([]float32, n)
	brake := make([]float32, n)
	throttle := make([]float32, n)
	steering := make([]float32, n)
	latG := make([]float32, n)
	longG := make([]float32, n)
	coolant := make([]float32, n)
	oil := make([]float32, n)

	startTime := lapPoints[0].Time
	var cumDist float64

	for i, p := range lapPoints {
		timestamps[i] = int((p.Time - startTime) * 1000)
		lats[i] = p.Lat
		lons[i] = p.Lon
		speeds[i] = float32(p.SpeedKmh)
		rpms[i] = float32(p.RPM)
		gear[i] = float32(p.Gear)
		brake[i] = float32(p.BrakePct)
		throttle[i] = float32(p.ThrottlePct)
		steering[i] = float32(p.SteeringDeg)
		latG[i] = float32(p.LatG)
		longG[i] = float32(p.LongG)
		coolant[i] = float32(p.CoolantC)
		oil[i] = float32(p.OilTempC)

		if i > 0 {
			cumDist += vbo.Haversine(lapPoints[i-1].Lat, lapPoints[i-1].Lon, p.Lat, p.Lon)
		}
		distances[i] = float32(cumDist)
	}

	_, err := DB.Exec(context.Background(),
		`INSERT INTO telemetry (lap_id, sample_rate_hz, timestamps_ms, latitudes, longitudes,
		 speeds_kmh, distances_m, rpms, gear, brake_pct, throttle_pct, steering_deg,
		 lat_g, long_g, coolant_temp_c, oil_temp_c)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		lapID, sampleRate, timestamps, lats, lons, speeds, distances,
		rpms, gear, brake, throttle, steering, latG, longG, coolant, oil)
	return err
}

func GetLapsForSession(sessionID string) ([]Lap, error) {
	rows, err := DB.Query(context.Background(),
		`SELECT id, session_id, lap_number, lap_time_ms, is_valid, is_outlap, is_inlap, split_times_ms, COALESCE(ambient_temp_f, 0)
		 FROM laps WHERE session_id = $1
		 ORDER BY lap_number`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var laps []Lap
	for rows.Next() {
		var l Lap
		if err := rows.Scan(&l.ID, &l.SessionID, &l.LapNumber, &l.LapTimeMs,
			&l.IsValid, &l.IsOutlap, &l.IsInlap, &l.SplitTimesMs, &l.AmbientTempF); err != nil {
			return nil, err
		}
		laps = append(laps, l)
	}
	return laps, nil
}

func GetTelemetry(lapID int) (*Telemetry, error) {
	t := &Telemetry{}
	err := DB.QueryRow(context.Background(),
		`SELECT id, lap_id, sample_rate_hz, timestamps_ms, latitudes, longitudes,
		        speeds_kmh, distances_m, rpms, gear, brake_pct, throttle_pct,
		        steering_deg, lat_g, long_g, coolant_temp_c, oil_temp_c
		 FROM telemetry WHERE lap_id = $1`, lapID).Scan(
		&t.ID, &t.LapID, &t.SampleRate, &t.Timestamps, &t.Latitudes, &t.Longitudes,
		&t.SpeedsKmh, &t.DistancesM, &t.RPMs, &t.Gear, &t.BrakePct, &t.ThrottlePct,
		&t.SteeringDeg, &t.LatG, &t.LongG, &t.CoolantC, &t.OilTempC)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// BestLapForSession returns the fastest valid lap in a session.
func BestLapForSession(sessionID string) (*Lap, error) {
	l := &Lap{}
	err := DB.QueryRow(context.Background(),
		`SELECT id, session_id, lap_number, lap_time_ms, is_valid, is_outlap, is_inlap, split_times_ms, COALESCE(ambient_temp_f, 0)
		 FROM laps
		 WHERE session_id = $1 AND is_valid = true
		 ORDER BY lap_time_ms ASC
		 LIMIT 1`, sessionID).Scan(
		&l.ID, &l.SessionID, &l.LapNumber, &l.LapTimeMs,
		&l.IsValid, &l.IsOutlap, &l.IsInlap, &l.SplitTimesMs, &l.AmbientTempF)
	if err != nil {
		return nil, err
	}
	return l, nil
}

// GetLapWithSession returns a lap and its parent session with joined fields.
func GetLapWithSession(lapID int) (*Lap, *Session, error) {
	l := &Lap{}
	s := &Session{}
	err := DB.QueryRow(context.Background(),
		`SELECT l.id, l.session_id, l.lap_number, l.lap_time_ms, l.is_valid, l.is_outlap, l.is_inlap, l.split_times_ms, COALESCE(l.ambient_temp_f, 0),
		        s.id, s.driver_id, s.track_id, s.uploaded_at, s.filename,
		        COALESCE(s.car_class,''), COALESCE(s.car_generation,''), COALESCE(s.car_model,''),
		        s.mod_points, COALESCE(s.tire_brand,''), COALESCE(s.tire_model,''),
		        d.name, t.name, t.config
		 FROM laps l
		 JOIN sessions s ON s.id = l.session_id
		 JOIN drivers d ON d.id = s.driver_id
		 JOIN tracks t ON t.id = s.track_id
		 WHERE l.id = $1`, lapID).Scan(
		&l.ID, &l.SessionID, &l.LapNumber, &l.LapTimeMs,
		&l.IsValid, &l.IsOutlap, &l.IsInlap, &l.SplitTimesMs, &l.AmbientTempF,
		&s.ID, &s.DriverID, &s.TrackID, &s.UploadedAt, &s.Filename,
		&s.CarClass, &s.CarGeneration, &s.CarModel,
		&s.ModPoints, &s.TireBrand, &s.TireModel,
		&s.DriverName, &s.TrackName, &s.TrackConfig)
	if err != nil {
		return nil, nil, err
	}
	return l, s, nil
}

// PersonalBestSplits returns the best split time for each split index
// for a driver at a specific track (across all sessions).
func PersonalBestSplits(driverID, trackID, numSplits int) ([]int, error) {
	best := make([]int, numSplits)
	for i := range best {
		best[i] = 999999999
	}

	rows, err := DB.Query(context.Background(),
		`SELECT l.split_times_ms FROM laps l
		 JOIN sessions s ON s.id = l.session_id
		 WHERE s.driver_id = $1 AND s.track_id = $2 AND l.is_valid = true`,
		driverID, trackID)
	if err != nil {
		return best, err
	}
	defer rows.Close()

	for rows.Next() {
		var splits []int
		if err := rows.Scan(&splits); err != nil {
			continue
		}
		for i, s := range splits {
			if i < numSplits && s > 0 && s < best[i] {
				best[i] = s
			}
		}
	}
	return best, nil
}

// EventBestSplits returns the best split time for each split index
// for a driver at a specific event.
func EventBestSplits(driverID, eventID, numSplits int) ([]int, error) {
	best := make([]int, numSplits)
	for i := range best {
		best[i] = 999999999
	}

	rows, err := DB.Query(context.Background(),
		`SELECT l.split_times_ms FROM laps l
		 JOIN sessions s ON s.id = l.session_id
		 WHERE s.driver_id = $1 AND s.event_id = $2 AND l.is_valid = true`,
		driverID, eventID)
	if err != nil {
		return best, err
	}
	defer rows.Close()

	for rows.Next() {
		var splits []int
		if err := rows.Scan(&splits); err != nil {
			continue
		}
		for i, s := range splits {
			if i < numSplits && s > 0 && s < best[i] {
				best[i] = s
			}
		}
	}
	return best, nil
}

// PersonalBestLap returns the best valid lap time for a driver at a track.
func PersonalBestLap(driverID, trackID int) (int, error) {
	var ms int
	err := DB.QueryRow(context.Background(),
		`SELECT MIN(l.lap_time_ms) FROM laps l
		 JOIN sessions s ON s.id = l.session_id
		 WHERE s.driver_id = $1 AND s.track_id = $2 AND l.is_valid = true`,
		driverID, trackID).Scan(&ms)
	return ms, err
}

// DriverTrackRecord is a driver's best lap at a track in a class.
type DriverTrackRecord struct {
	TrackName   string
	TrackConfig string
	CarClass    string
	BestLapMs   int
	BestLapTime string
	SessionID   string
	LapID       int
}

// DriverTrackRecords returns the best lap per track per class for a driver.
func DriverTrackRecords(driverID int) ([]DriverTrackRecord, error) {
	rows, err := DB.Query(context.Background(),
		`SELECT DISTINCT ON (t.name, t.config, s.car_class)
			t.name, t.config, COALESCE(s.car_class,''), l.lap_time_ms, s.id, l.id
		 FROM laps l
		 JOIN sessions s ON s.id = l.session_id
		 JOIN tracks t ON t.id = s.track_id
		 WHERE s.driver_id = $1 AND l.is_valid = true
		 ORDER BY t.name, t.config, s.car_class, l.lap_time_ms ASC`, driverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []DriverTrackRecord
	for rows.Next() {
		var r DriverTrackRecord
		if err := rows.Scan(&r.TrackName, &r.TrackConfig, &r.CarClass, &r.BestLapMs, &r.SessionID, &r.LapID); err != nil {
			continue
		}
		// Format inline
		ms := r.BestLapMs
		min := ms / 60000
		sec := (ms % 60000) / 1000
		mil := ms % 1000
		if min > 0 {
			r.BestLapTime = fmt.Sprintf("%d:%02d.%03d", min, sec, mil)
		} else {
			r.BestLapTime = fmt.Sprintf("%d.%03d", sec, mil)
		}
		records = append(records, r)
	}
	return records, nil
}

func GetTrackIDByNameConfig(name, config string) (int, error) {
	var id int
	err := DB.QueryRow(context.Background(),
		`SELECT id FROM tracks WHERE name = $1 AND config = $2`, name, config).Scan(&id)
	return id, err
}
