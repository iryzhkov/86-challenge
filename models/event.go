package models

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type Event struct {
	ID                int
	Slug              string
	Name              string
	TrackID           int
	Date              time.Time
	Organizer         string
	WeatherTempF      float32
	WeatherHumidity   float32
	WeatherWindMph    float32
	WeatherConditions string
	SourceURL         string
	// Joined
	TrackName   string
	TrackConfig string
}

// MakeEventSlug generates a URL-friendly slug like "2026-02-14-speedsf-thunderhill-east-cyclone"
func MakeEventSlug(date time.Time, organizer, trackName, trackConfig string) string {
	parts := []string{date.Format("2006-01-02")}
	if organizer != "" {
		parts = append(parts, slugify(organizer))
	}
	parts = append(parts, slugify(trackName))
	if trackConfig != "" {
		parts = append(parts, slugify(trackConfig))
	}
	return strings.Join(parts, "-")
}

func slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, s)
	// Collapse multiple dashes
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

const eventSelectCols = `e.id, COALESCE(e.slug,''), e.name, e.track_id, e.date, COALESCE(e.organizer,''),
	COALESCE(e.weather_temp_f,0), COALESCE(e.weather_humidity,0), COALESCE(e.weather_wind_mph,0),
	COALESCE(e.weather_conditions,''), COALESCE(e.source_url,''),
	t.name, t.config`

func scanEvent(scan func(dest ...any) error) (*Event, error) {
	e := &Event{}
	err := scan(&e.ID, &e.Slug, &e.Name, &e.TrackID, &e.Date, &e.Organizer,
		&e.WeatherTempF, &e.WeatherHumidity, &e.WeatherWindMph,
		&e.WeatherConditions, &e.SourceURL,
		&e.TrackName, &e.TrackConfig)
	if err != nil {
		return nil, err
	}
	return e, nil
}

func GetEvent(id int) (*Event, error) {
	return scanEvent(DB.QueryRow(context.Background(),
		fmt.Sprintf(`SELECT %s FROM events e JOIN tracks t ON t.id = e.track_id WHERE e.id = $1`, eventSelectCols),
		id).Scan)
}

func GetEventBySlug(slug string) (*Event, error) {
	return scanEvent(DB.QueryRow(context.Background(),
		fmt.Sprintf(`SELECT %s FROM events e JOIN tracks t ON t.id = e.track_id WHERE e.slug = $1`, eventSelectCols),
		slug).Scan)
}

func ListEvents() ([]Event, error) {
	rows, err := DB.Query(context.Background(),
		fmt.Sprintf(`SELECT %s FROM events e JOIN tracks t ON t.id = e.track_id ORDER BY e.date DESC`, eventSelectCols))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		e, err := scanEvent(rows.Scan)
		if err != nil {
			return nil, err
		}
		events = append(events, *e)
	}
	return events, nil
}

func ListUpcomingEvents(limit int) ([]Event, error) {
	rows, err := DB.Query(context.Background(),
		fmt.Sprintf(`SELECT %s FROM events e JOIN tracks t ON t.id = e.track_id
		 WHERE e.date >= CURRENT_DATE ORDER BY e.date ASC LIMIT $1`, eventSelectCols), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		e, err := scanEvent(rows.Scan)
		if err != nil {
			return nil, err
		}
		events = append(events, *e)
	}
	return events, nil
}

func GetTodaysEvent() (*Event, error) {
	return scanEvent(DB.QueryRow(context.Background(),
		fmt.Sprintf(`SELECT %s FROM events e JOIN tracks t ON t.id = e.track_id WHERE e.date = CURRENT_DATE LIMIT 1`, eventSelectCols)).Scan)
}

// GetCurrentEvent returns the most recent past event (treated as "current" until the next one starts).
func GetCurrentEvent() (*Event, error) {
	return scanEvent(DB.QueryRow(context.Background(),
		fmt.Sprintf(`SELECT %s FROM events e JOIN tracks t ON t.id = e.track_id
		 WHERE e.date <= CURRENT_DATE ORDER BY e.date DESC LIMIT 1`, eventSelectCols)).Scan)
}

// MatchEventForUpload finds the closest event for a given track and date range.
func MatchEventForUpload(trackID int, sessionDate time.Time) (*Event, error) {
	return scanEvent(DB.QueryRow(context.Background(),
		fmt.Sprintf(`SELECT %s FROM events e JOIN tracks t ON t.id = e.track_id
		 WHERE e.track_id = $1
		   AND e.date BETWEEN $2::date - INTERVAL '3 days' AND $2::date + INTERVAL '3 days'
		 ORDER BY ABS(e.date - $2::date) LIMIT 1`, eventSelectCols),
		trackID, sessionDate).Scan)
}

// CreateEvent creates a new event. Returns the event ID.
// Uses ON CONFLICT to handle duplicate (track_id, date).
func CreateEvent(name string, trackID int, date time.Time, organizer string) (int, string, error) {
	// Get track info for slug
	var trackName, trackConfig string
	DB.QueryRow(context.Background(),
		`SELECT name, config FROM tracks WHERE id = $1`, trackID).Scan(&trackName, &trackConfig)

	slug := MakeEventSlug(date, organizer, trackName, trackConfig)

	var id int
	err := DB.QueryRow(context.Background(),
		`INSERT INTO events (name, slug, track_id, date, organizer)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (track_id, date) DO UPDATE SET name = EXCLUDED.name
		 RETURNING id`,
		name, slug, trackID, date, organizer).Scan(&id)
	return id, slug, err
}

func GetSessionsForEvent(eventID int) ([]Session, error) {
	rows, err := DB.Query(context.Background(),
		`SELECT s.id, COALESCE(s.start_time_secs,0), s.driver_id, s.track_id, s.uploaded_at, s.filename,
		        COALESCE(s.car_class,''), COALESCE(s.car_generation,''), COALESCE(s.car_model,''),
		        s.mod_points, COALESCE(s.tire_brand,''), COALESCE(s.tire_model,''),
		        d.name, t.name, t.config
		 FROM sessions s
		 JOIN drivers d ON d.id = s.driver_id
		 JOIN tracks t ON t.id = s.track_id
		 WHERE s.event_id = $1
		 ORDER BY s.uploaded_at DESC`, eventID)
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

func RecentPastEvents(limit int) ([]Event, error) {
	rows, err := DB.Query(context.Background(),
		fmt.Sprintf(`SELECT %s FROM events e JOIN tracks t ON t.id = e.track_id
		 WHERE e.date <= CURRENT_DATE ORDER BY e.date DESC LIMIT $1`, eventSelectCols), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		e, err := scanEvent(rows.Scan)
		if err != nil {
			return nil, err
		}
		events = append(events, *e)
	}
	return events, nil
}

func SearchEvents(trackID int, date string) ([]Event, error) {
	query := fmt.Sprintf(`SELECT %s FROM events e JOIN tracks t ON t.id = e.track_id WHERE e.date <= CURRENT_DATE`, eventSelectCols)
	args := []any{}
	argN := 1

	if trackID > 0 {
		query += fmt.Sprintf(` AND e.track_id = $%d`, argN)
		args = append(args, trackID)
		argN++
	}
	if date != "" {
		query += fmt.Sprintf(` AND e.date = $%d`, argN)
		args = append(args, date)
		argN++
	}

	query += ` ORDER BY e.date DESC LIMIT 10`

	rows, err := DB.Query(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		e, err := scanEvent(rows.Scan)
		if err != nil {
			return nil, err
		}
		events = append(events, *e)
	}
	return events, nil
}

// ListPastEventsWithSessions returns past events that have at least one session uploaded.
func ListPastEventsWithSessions() ([]Event, error) {
	rows, err := DB.Query(context.Background(),
		fmt.Sprintf(`SELECT %s FROM events e JOIN tracks t ON t.id = e.track_id
		 WHERE e.date < CURRENT_DATE
		   AND EXISTS (SELECT 1 FROM sessions s WHERE s.event_id = e.id)
		 ORDER BY e.date DESC`, eventSelectCols))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		e, err := scanEvent(rows.Scan)
		if err != nil {
			return nil, err
		}
		events = append(events, *e)
	}
	return events, nil
}

func ListTracks() ([]struct{ ID int; Name, Config string }, error) {
	rows, err := DB.Query(context.Background(),
		`SELECT id, name, config FROM tracks ORDER BY name, config`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tracks []struct{ ID int; Name, Config string }
	for rows.Next() {
		var t struct{ ID int; Name, Config string }
		rows.Scan(&t.ID, &t.Name, &t.Config)
		tracks = append(tracks, t)
	}
	return tracks, nil
}
