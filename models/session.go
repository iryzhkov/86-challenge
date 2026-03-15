package models

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"
)

func generateID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return fmt.Sprintf("%x", b) // 12 hex chars
}

type Session struct {
	ID             string
	StartTimeSecs  float64 // seconds since midnight from VBO data
	DriverID      int
	EventID       *int
	TrackID       int
	UploadedAt    time.Time
	Filename      string
	CarClass      string
	CarGeneration string
	CarModel      string
	ModPoints     int
	TireBrand     string
	TireModel     string
	Notes         string
	// Joined fields
	DriverName string
	TrackName  string
	TrackConfig string
}

func CreateSession(driverID int, eventID *int, trackID int, filename, carClass, generation, model, tireBrand, tireModel, notes string, modPoints int, startTimeSecs float64) (string, error) {
	id := generateID()
	_, err := DB.Exec(context.Background(),
		`INSERT INTO sessions (id, start_time_secs, driver_id, event_id, track_id, filename, car_class, car_generation, car_model, mod_points, tire_brand, tire_model, notes)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		id, startTimeSecs, driverID, eventID, trackID, filename, carClass, generation, model, modPoints, tireBrand, tireModel, notes)
	return id, err
}

func GetSession(id string) (*Session, error) {
	s := &Session{}
	err := DB.QueryRow(context.Background(),
		`SELECT s.id, COALESCE(s.start_time_secs,0), s.driver_id, s.event_id, s.track_id, s.uploaded_at, s.filename,
		        COALESCE(s.car_class,''), COALESCE(s.car_generation,''), COALESCE(s.car_model,''),
		        s.mod_points, COALESCE(s.tire_brand,''), COALESCE(s.tire_model,''), COALESCE(s.notes,''),
		        d.name, t.name, t.config
		 FROM sessions s
		 JOIN drivers d ON d.id = s.driver_id
		 JOIN tracks t ON t.id = s.track_id
		 WHERE s.id = $1`, id).Scan(
		&s.ID, &s.StartTimeSecs, &s.DriverID, &s.EventID, &s.TrackID, &s.UploadedAt, &s.Filename,
		&s.CarClass, &s.CarGeneration, &s.CarModel, &s.ModPoints,
		&s.TireBrand, &s.TireModel, &s.Notes,
		&s.DriverName, &s.TrackName, &s.TrackConfig)
	if err != nil {
		return nil, err
	}
	return s, nil
}

type SessionFilters struct {
	DriverName string
	TrackName  string
	CarClass   string
	TireBrand  string
	TireModel  string
	EventID    string
	DateFrom   string
	DateTo     string
}

func SearchSessions(f SessionFilters, limit int) ([]Session, error) {
	query := `SELECT s.id, COALESCE(s.start_time_secs,0), s.driver_id, s.track_id, s.uploaded_at, s.filename,
		COALESCE(s.car_class,''), COALESCE(s.car_generation,''), COALESCE(s.car_model,''),
		s.mod_points, COALESCE(s.tire_brand,''), COALESCE(s.tire_model,''),
		d.name, t.name, t.config
	 FROM sessions s
	 JOIN drivers d ON d.id = s.driver_id
	 JOIN tracks t ON t.id = s.track_id
	 WHERE 1=1`

	args := []any{}
	n := 1

	if f.DriverName != "" {
		query += fmt.Sprintf(` AND d.name ILIKE '%%' || $%d || '%%'`, n)
		args = append(args, f.DriverName)
		n++
	}
	if f.TrackName != "" {
		query += fmt.Sprintf(` AND t.name = $%d`, n)
		args = append(args, f.TrackName)
		n++
	}
	if f.CarClass != "" {
		query += fmt.Sprintf(` AND s.car_class = $%d`, n)
		args = append(args, f.CarClass)
		n++
	}
	if f.TireBrand != "" {
		query += fmt.Sprintf(` AND s.tire_brand ILIKE $%d`, n)
		args = append(args, f.TireBrand)
		n++
	}
	if f.TireModel != "" {
		query += fmt.Sprintf(` AND s.tire_model ILIKE $%d`, n)
		args = append(args, f.TireModel)
		n++
	}
	if f.EventID != "" {
		query += fmt.Sprintf(` AND s.event_id = $%d`, n)
		args = append(args, f.EventID)
		n++
	}
	if f.DateFrom != "" {
		query += fmt.Sprintf(` AND s.uploaded_at >= $%d::date`, n)
		args = append(args, f.DateFrom)
		n++
	}
	if f.DateTo != "" {
		query += fmt.Sprintf(` AND s.uploaded_at < $%d::date + INTERVAL '1 day'`, n)
		args = append(args, f.DateTo)
		n++
	}

	query += fmt.Sprintf(` ORDER BY s.uploaded_at DESC LIMIT $%d`, n)
	args = append(args, limit)

	rows, err := DB.Query(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.StartTimeSecs, &s.DriverID, &s.TrackID, &s.UploadedAt, &s.Filename,
			&s.CarClass, &s.CarGeneration, &s.CarModel, &s.ModPoints,
			&s.TireBrand, &s.TireModel,
			&s.DriverName, &s.TrackName, &s.TrackConfig); err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

// GetRelatedSessions returns all sessions by the same driver, same class, at the same event.
// Excludes the given session ID. Returns empty if no event.
func GetRelatedSessions(sessionID string, driverID int, eventID *int, carClass string) ([]Session, error) {
	if eventID == nil {
		return nil, nil
	}
	rows, err := DB.Query(context.Background(),
		`SELECT s.id, COALESCE(s.start_time_secs,0), s.driver_id, s.track_id, s.uploaded_at, s.filename,
		        COALESCE(s.car_class,''), COALESCE(s.car_generation,''), COALESCE(s.car_model,''),
		        s.mod_points, COALESCE(s.tire_brand,''), COALESCE(s.tire_model,''),
		        d.name, t.name, t.config
		 FROM sessions s
		 JOIN drivers d ON d.id = s.driver_id
		 JOIN tracks t ON t.id = s.track_id
		 WHERE s.event_id = $1 AND s.driver_id = $2 AND s.car_class = $3 AND s.id != $4
		 ORDER BY s.uploaded_at`,
		*eventID, driverID, carClass, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.StartTimeSecs, &s.DriverID, &s.TrackID, &s.UploadedAt, &s.Filename,
			&s.CarClass, &s.CarGeneration, &s.CarModel, &s.ModPoints,
			&s.TireBrand, &s.TireModel,
			&s.DriverName, &s.TrackName, &s.TrackConfig); err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

func ListDriverNames() ([]string, error) {
	rows, err := DB.Query(context.Background(),
		`SELECT name FROM drivers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		names = append(names, n)
	}
	return names, nil
}

func DeleteSession(id string) error {
	ctx := context.Background()
	// Delete in order: telemetry → laps → session
	DB.Exec(ctx, `DELETE FROM telemetry WHERE lap_id IN (SELECT id FROM laps WHERE session_id = $1)`, id)
	DB.Exec(ctx, `DELETE FROM laps WHERE session_id = $1`, id)
	_, err := DB.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	return err
}

func GetSessionsForDriver(driverID int) ([]Session, error) {
	rows, err := DB.Query(context.Background(),
		`SELECT s.id, COALESCE(s.start_time_secs,0), s.driver_id, s.track_id, s.uploaded_at, s.filename,
		        COALESCE(s.car_class,''), COALESCE(s.car_generation,''), COALESCE(s.car_model,''),
		        s.mod_points, COALESCE(s.tire_brand,''), COALESCE(s.tire_model,''),
		        d.name, t.name, t.config
		 FROM sessions s
		 JOIN drivers d ON d.id = s.driver_id
		 JOIN tracks t ON t.id = s.track_id
		 WHERE s.driver_id = $1
		 ORDER BY s.uploaded_at DESC`, driverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.StartTimeSecs, &s.DriverID, &s.TrackID, &s.UploadedAt, &s.Filename,
			&s.CarClass, &s.CarGeneration, &s.CarModel, &s.ModPoints,
			&s.TireBrand, &s.TireModel,
			&s.DriverName, &s.TrackName, &s.TrackConfig); err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

func RecentSessions(limit int) ([]Session, error) {
	rows, err := DB.Query(context.Background(),
		`SELECT s.id, COALESCE(s.start_time_secs,0), s.driver_id, s.track_id, s.uploaded_at, s.filename,
		        COALESCE(s.car_class,''), COALESCE(s.car_generation,''), COALESCE(s.car_model,''),
		        s.mod_points, COALESCE(s.tire_brand,''), COALESCE(s.tire_model,''),
		        d.name, t.name, t.config
		 FROM sessions s
		 JOIN drivers d ON d.id = s.driver_id
		 JOIN tracks t ON t.id = s.track_id
		 ORDER BY s.uploaded_at DESC
		 LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.StartTimeSecs, &s.DriverID, &s.TrackID, &s.UploadedAt, &s.Filename,
			&s.CarClass, &s.CarGeneration, &s.CarModel, &s.ModPoints,
			&s.TireBrand, &s.TireModel,
			&s.DriverName, &s.TrackName, &s.TrackConfig); err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}
