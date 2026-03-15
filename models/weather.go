package models

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"
)

// FetchAndStoreWeather fetches historical daily + hourly weather from Open-Meteo for an event.
func FetchAndStoreWeather(eventID int, lat, lon float64, date time.Time) error {
	dateStr := date.Format("2006-01-02")

	url := fmt.Sprintf(
		"https://archive-api.open-meteo.com/v1/archive?latitude=%.4f&longitude=%.4f"+
			"&start_date=%s&end_date=%s"+
			"&daily=temperature_2m_max,temperature_2m_min,relative_humidity_2m_mean,wind_speed_10m_max,weather_code"+
			"&hourly=temperature_2m,relative_humidity_2m,wind_speed_10m"+
			"&temperature_unit=fahrenheit&wind_speed_unit=mph&timezone=America/Los_Angeles",
		lat, lon, dateStr, dateStr,
	)

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("fetching weather: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("weather API returned %d", resp.StatusCode)
	}

	var result struct {
		Daily struct {
			TempMax     []float64 `json:"temperature_2m_max"`
			TempMin     []float64 `json:"temperature_2m_min"`
			Humidity    []float64 `json:"relative_humidity_2m_mean"`
			WindMax     []float64 `json:"wind_speed_10m_max"`
			WeatherCode []int    `json:"weather_code"`
		} `json:"daily"`
		Hourly struct {
			Time     []string  `json:"time"`
			Temp     []float64 `json:"temperature_2m"`
			Humidity []float64 `json:"relative_humidity_2m"`
			Wind     []float64 `json:"wind_speed_10m"`
		} `json:"hourly"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decoding weather: %w", err)
	}

	ctx := context.Background()

	// Store daily summary
	if len(result.Daily.TempMax) > 0 {
		avgTemp := (result.Daily.TempMax[0] + result.Daily.TempMin[0]) / 2
		humidity := result.Daily.Humidity[0]
		wind := result.Daily.WindMax[0]
		conditions := weatherCodeToString(result.Daily.WeatherCode[0])

		DB.Exec(ctx,
			`UPDATE events SET weather_temp_f = $1, weather_humidity = $2,
			 weather_wind_mph = $3, weather_conditions = $4
			 WHERE id = $5`,
			avgTemp, humidity, wind, conditions, eventID)
	}

	// Store hourly data
	for i, t := range result.Hourly.Temp {
		// Parse hour from time string like "2026-03-14T09:00"
		hour := i % 24
		if i < len(result.Hourly.Time) {
			if parsed, err := time.Parse("2006-01-02T15:04", result.Hourly.Time[i]); err == nil {
				hour = parsed.Hour()
			}
		}

		humidity := 0.0
		if i < len(result.Hourly.Humidity) {
			humidity = result.Hourly.Humidity[i]
		}
		wind := 0.0
		if i < len(result.Hourly.Wind) {
			wind = result.Hourly.Wind[i]
		}

		DB.Exec(ctx,
			`INSERT INTO event_hourly_weather (event_id, hour, temp_f, humidity, wind_mph)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (event_id, hour) DO UPDATE SET
			   temp_f = EXCLUDED.temp_f, humidity = EXCLUDED.humidity, wind_mph = EXCLUDED.wind_mph`,
			eventID, hour, t, humidity, wind)
	}

	return nil
}

// LookupHourlyTemp returns the temperature for a given event and time-of-day (seconds since midnight).
// Interpolates between hours.
func LookupHourlyTemp(eventID int, timeOfDaySec float64) (float32, error) {
	hour := int(math.Floor(timeOfDaySec / 3600))
	if hour < 0 {
		hour = 0
	}
	if hour > 23 {
		hour = 23
	}

	var temp float32
	err := DB.QueryRow(context.Background(),
		`SELECT temp_f FROM event_hourly_weather WHERE event_id = $1 AND hour = $2`,
		eventID, hour).Scan(&temp)
	return temp, err
}

func weatherCodeToString(code int) string {
	switch {
	case code == 0:
		return "Clear"
	case code <= 3:
		return "Partly Cloudy"
	case code <= 49:
		return "Foggy"
	case code <= 59:
		return "Drizzle"
	case code <= 69:
		return "Rain"
	case code <= 79:
		return "Snow"
	case code <= 84:
		return "Rain Showers"
	case code <= 86:
		return "Snow Showers"
	case code <= 99:
		return "Thunderstorm"
	default:
		return "Unknown"
	}
}
