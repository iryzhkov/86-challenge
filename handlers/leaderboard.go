package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/iryzhkov/86-challenge/models"
)

type LeaderboardEntry struct {
	Rank        int
	DriverID    int
	DriverName  string
	CarClass    string
	CarGen      string
	CarModel    string
	ModPoints   int
	BestLapMs   int
	BestLapTime string
	TireBrand   string
	TireModel   string
	TrackName   string
	TrackConfig string
	TrackID     int
	SessionID   string
	LapID       int
}

func LeaderboardPage(w http.ResponseWriter, r *http.Request) {
	trackName := r.URL.Query().Get("track")
	trackConfig := r.URL.Query().Get("config")
	carClass := r.URL.Query().Get("class")
	tireBrand := r.URL.Query().Get("tire_brand")
	tireModel := r.URL.Query().Get("tire_model")

	tracks, _ := getDistinctTracks()

	// Default to most popular track if none selected
	if trackName == "" {
		if popular, err := mostPopularTrack(); err == nil && popular != "" {
			trackName = popular
		}
	}

	var entries []LeaderboardEntry
	if trackName != "" {
		entries, _ = queryLeaderboard(trackName, trackConfig, carClass, tireBrand, tireModel)
	}

	Templates["leaderboard.html"].ExecuteTemplate(w, "base", map[string]any{
		"Entries":      entries,
		"Tracks":       tracks,
		"FilterTrack":  trackName,
		"FilterConfig": trackConfig,
		"FilterClass":  carClass,
		"FilterTire":   tireBrand,
		"FilterTireModel": tireModel,
	})
}

type TrackOption struct {
	Name    string
	Config  string
	Display string
}

func getDistinctTracks() ([]TrackOption, error) {
	rows, err := models.DB.Query(context.Background(),
		`SELECT name, config FROM tracks ORDER BY name, config`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tracks []TrackOption
	for rows.Next() {
		var t TrackOption
		rows.Scan(&t.Name, &t.Config)
		t.Display = t.Name + " - " + t.Config
		tracks = append(tracks, t)
	}
	return tracks, nil
}

func mostPopularTrack() (string, error) {
	var name string
	err := models.DB.QueryRow(context.Background(),
		`SELECT t.name FROM sessions s
		 JOIN tracks t ON t.id = s.track_id
		 GROUP BY t.name ORDER BY COUNT(*) DESC LIMIT 1`).Scan(&name)
	return name, err
}

func queryLeaderboard(trackName, trackConfig, carClass, tireBrand, tireModel string) ([]LeaderboardEntry, error) {
	query := `
		SELECT DISTINCT ON (d.name)
			d.id, d.name, COALESCE(s.car_class,''), COALESCE(s.car_generation,''),
			COALESCE(s.car_model,''), s.mod_points,
			l.lap_time_ms, COALESCE(s.tire_brand,''), COALESCE(s.tire_model,''),
			t.name, t.config, t.id, s.id, l.id
		FROM laps l
		JOIN sessions s ON s.id = l.session_id
		JOIN drivers d ON d.id = s.driver_id
		JOIN tracks t ON t.id = s.track_id
		WHERE l.is_valid = true
		  AND t.name = $1`

	args := []any{trackName}
	argN := 2

	if trackConfig != "" {
		query += fmt.Sprintf(` AND t.config = $%d`, argN)
		args = append(args, trackConfig)
		argN++
	}
	if carClass != "" {
		query += fmt.Sprintf(` AND s.car_class = $%d`, argN)
		args = append(args, carClass)
		argN++
	}
	if tireBrand != "" {
		query += fmt.Sprintf(` AND s.tire_brand ILIKE $%d`, argN)
		args = append(args, tireBrand)
		argN++
	}
	if tireModel != "" {
		query += fmt.Sprintf(` AND s.tire_model ILIKE $%d`, argN)
		args = append(args, tireModel)
		argN++
	}

	query += ` ORDER BY d.name, l.lap_time_ms ASC`

	rows, err := models.DB.Query(context.Background(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []LeaderboardEntry
	rank := 0
	for rows.Next() {
		rank++
		var e LeaderboardEntry
		rows.Scan(&e.DriverID, &e.DriverName, &e.CarClass, &e.CarGen, &e.CarModel,
			&e.ModPoints, &e.BestLapMs, &e.TireBrand, &e.TireModel,
			&e.TrackName, &e.TrackConfig, &e.TrackID, &e.SessionID, &e.LapID)
		e.Rank = rank
		e.BestLapTime = FormatLapTime(e.BestLapMs)
		entries = append(entries, e)
	}

	// Sort by lap time (DISTINCT ON gives per-driver best, but we need global sort)
	sortByLapTime(entries)
	for i := range entries {
		entries[i].Rank = i + 1
	}

	return entries, nil
}

func sortByLapTime(entries []LeaderboardEntry) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].BestLapMs < entries[j-1].BestLapMs; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
}
