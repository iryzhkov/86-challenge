package models

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

var DB *pgxpool.Pool

func InitDB(databaseURL string) error {
	var err error
	DB, err = pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}

	if err := DB.Ping(context.Background()); err != nil {
		return fmt.Errorf("pinging database: %w", err)
	}

	return RunMigrations()
}

func RunMigrations() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS tracks (
			id SERIAL PRIMARY KEY,
			name TEXT NOT NULL,
			config TEXT NOT NULL,
			lat DOUBLE PRECISION,
			lon DOUBLE PRECISION,
			start_lat1 DOUBLE PRECISION,
			start_lon1 DOUBLE PRECISION,
			start_lat2 DOUBLE PRECISION,
			start_lon2 DOUBLE PRECISION,
			UNIQUE(name, config)
		)`,
		`CREATE TABLE IF NOT EXISTS track_splits (
			id SERIAL PRIMARY KEY,
			track_id INT REFERENCES tracks(id),
			split_index INT NOT NULL,
			name TEXT,
			lat1 DOUBLE PRECISION,
			lon1 DOUBLE PRECISION,
			lat2 DOUBLE PRECISION,
			lon2 DOUBLE PRECISION,
			vote_count INT DEFAULT 1,
			UNIQUE(track_id, split_index)
		)`,
		`CREATE TABLE IF NOT EXISTS drivers (
			id SERIAL PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			car_class TEXT,
			car_generation TEXT,
			car_model TEXT,
			car_year INT,
			tire_brand TEXT,
			tire_model TEXT,
			mod_points INT DEFAULT 0,
			created_at TIMESTAMPTZ DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS events (
			id SERIAL PRIMARY KEY,
			slug TEXT UNIQUE,
			name TEXT NOT NULL,
			track_id INT REFERENCES tracks(id),
			date DATE NOT NULL,
			organizer TEXT,
			weather_temp_f REAL,
			weather_humidity REAL,
			weather_wind_mph REAL,
			weather_conditions TEXT,
			source_url TEXT,
			UNIQUE(track_id, date)
		)`,
		`CREATE TABLE IF NOT EXISTS event_hourly_weather (
			id SERIAL PRIMARY KEY,
			event_id INT REFERENCES events(id),
			hour INT NOT NULL,
			temp_f REAL NOT NULL,
			humidity REAL,
			wind_mph REAL,
			UNIQUE(event_id, hour)
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			start_time_secs REAL,
			driver_id INT REFERENCES drivers(id),
			event_id INT REFERENCES events(id),
			track_id INT REFERENCES tracks(id),
			uploaded_at TIMESTAMPTZ DEFAULT NOW(),
			filename TEXT,
			car_class TEXT,
			car_generation TEXT,
			car_model TEXT,
			mod_points INT DEFAULT 0,
			tire_brand TEXT,
			tire_model TEXT,
			notes TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS laps (
			id SERIAL PRIMARY KEY,
			session_id TEXT REFERENCES sessions(id),
			lap_number INT NOT NULL,
			lap_time_ms INT NOT NULL,
			is_valid BOOLEAN DEFAULT TRUE,
			is_outlap BOOLEAN DEFAULT FALSE,
			is_inlap BOOLEAN DEFAULT FALSE,
			split_times_ms INT[],
			ambient_temp_f REAL,
			UNIQUE(session_id, lap_number)
		)`,
		`CREATE TABLE IF NOT EXISTS telemetry (
			id SERIAL PRIMARY KEY,
			lap_id INT REFERENCES laps(id) UNIQUE,
			sample_rate_hz REAL,
			timestamps_ms INT[],
			latitudes DOUBLE PRECISION[],
			longitudes DOUBLE PRECISION[],
			speeds_kmh REAL[],
			distances_m REAL[],
			rpms REAL[],
			gear REAL[],
			brake_pct REAL[],
			throttle_pct REAL[],
			steering_deg REAL[],
			lat_g REAL[],
			long_g REAL[],
			coolant_temp_c REAL[],
			oil_temp_c REAL[]
		)`,
	}

	ctx := context.Background()
	for _, m := range migrations {
		if _, err := DB.Exec(ctx, m); err != nil {
			return fmt.Errorf("running migration: %w", err)
		}
	}
	return nil
}

// SeedTracks inserts known tracks if they don't exist.
func SeedTracks() error {
	type trackSeed struct {
		Name   string
		Config string
		Lat    float64
		Lon    float64
	}
	tracks := []trackSeed{
		{"Laguna Seca", "Full", 36.5842, -121.7534},
		{"Sonoma Raceway", "Long", 38.1615, -122.4556},
		{"Sonoma Raceway", "Short", 38.1615, -122.4556},
		{"Thunderhill", "East Cyclone", 39.5383, -122.3316},
		{"Thunderhill", "East Bypass", 39.5383, -122.3316},
		{"Thunderhill", "West CW", 39.5383, -122.3316},
		{"Thunderhill", "West CCW", 39.5383, -122.3316},
		{"Thunderhill", "5-Mile", 39.5383, -122.3316},
		{"Buttonwillow", "CW13", 35.4906, -119.5442},
		{"Buttonwillow", "CCW13", 35.4906, -119.5442},
		{"Buttonwillow", "CW1", 35.4906, -119.5442},
	}

	ctx := context.Background()
	for _, t := range tracks {
		_, err := DB.Exec(ctx,
			`INSERT INTO tracks (name, config, lat, lon)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (name, config) DO NOTHING`,
			t.Name, t.Config, t.Lat, t.Lon)
		if err != nil {
			return fmt.Errorf("seeding track %s/%s: %w", t.Name, t.Config, err)
		}
	}
	return nil
}
