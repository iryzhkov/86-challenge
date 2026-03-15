package scraper

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/iryzhkov/86-challenge/models"
)

const baseURL = "https://86challenge.us"

// ScrapeEvents fetches the 86challenge.us/events page and populates the events table.
func ScrapeEvents() error {
	seasons := []int{2025, 2026}

	var events []scrapedEvent
	for _, season := range seasons {
		url := fmt.Sprintf("%s/events?season=%d", baseURL, season)
		body, err := fetchPage(url)
		if err != nil {
			log.Printf("Warning: failed to fetch %d season events: %v", season, err)
			continue
		}

		seasonEvents := parseEvents(body, season)
		events = append(events, seasonEvents...)
	}
	log.Printf("Scraped %d events from 86challenge.us", len(events))

	ctx := context.Background()
	for _, e := range events {
		// Match track name to our database
		trackID, err := matchTrack(ctx, e.TrackName)
		if err != nil {
			log.Printf("Warning: could not match track %q: %v", e.TrackName, err)
			continue
		}

		// Get track info for slug
		var trackName, trackConfig string
		models.DB.QueryRow(ctx, `SELECT name, config FROM tracks WHERE id = $1`, trackID).Scan(&trackName, &trackConfig)
		slug := models.MakeEventSlug(e.Date, e.Organizer, trackName, trackConfig)

		_, err = models.DB.Exec(ctx,
			`INSERT INTO events (name, slug, track_id, date, organizer, source_url)
			 VALUES ($1, $2, $3, $4, $5, $6)
			 ON CONFLICT (track_id, date) DO UPDATE SET
			   name = EXCLUDED.name, slug = EXCLUDED.slug, organizer = EXCLUDED.organizer, source_url = EXCLUDED.source_url`,
			e.Name, slug, trackID, e.Date, e.Organizer, e.SourceURL)
		if err != nil {
			log.Printf("Warning: failed to insert event %q: %v", e.Name, err)
		}
	}

	return nil
}

type scrapedEvent struct {
	Name      string
	TrackName string
	Date      time.Time
	Organizer string
	SourceURL string
}

func parseEvents(html string, year int) []scrapedEvent {
	var events []scrapedEvent

	// The events page has rows with date, track name, and organizer.
	// We look for patterns like "Feb 14" or "Apr 04" followed by track names.
	// Format from the page: each event row has date, track, organizer info.

	// Match lines with dates and track names
	// The schedule typically shows: "Sat, Feb 14", "Thunderhill East Cyclone", "SpeedSF"
	dateRe := regexp.MustCompile(`(?i)(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s+(\d{1,2})`)

	// Known track names to look for
	trackNames := []string{
		"Thunderhill East Cyclone", "Thunderhill East Bypass",
		"Thunderhill West CCW", "Thunderhill West CW",
		"Thunderhill 5 Mile", "Thunderhill",
		"Laguna Seca",
		"Sonoma",
		"Buttonwillow 13 CW", "Buttonwillow 13 CCW",
		"Buttonwillow Circuit", "Buttonwillow",
	}

	// Split into lines and scan for event patterns
	lines := strings.Split(html, "\n")
	for i, line := range lines {
		dateLoc := dateRe.FindStringSubmatch(line)
		if dateLoc == nil {
			continue
		}

		month := dateLoc[1]
		day, _ := strconv.Atoi(dateLoc[2])

		// Look in surrounding lines for track name
		context := strings.Join(lines[max(0, i-3):min(len(lines), i+5)], " ")

		trackName := ""
		for _, tn := range trackNames {
			if strings.Contains(strings.ToLower(context), strings.ToLower(tn)) {
				trackName = tn
				break
			}
		}
		if trackName == "" {
			continue
		}

		// Determine organizer
		organizer := "SpeedSF"
		if strings.Contains(strings.ToLower(context), "speed ventures") {
			organizer = "Speed Ventures"
		}

		// Parse date using the provided season year
		t, err := time.Parse("Jan 2 2006", fmt.Sprintf("%s %d %d", month, day, year))
		if err != nil {
			continue
		}

		// Skip duplicate dates
		duplicate := false
		for _, e := range events {
			if e.Date.Equal(t) && e.TrackName == trackName {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}

		events = append(events, scrapedEvent{
			Name:      fmt.Sprintf("%d 86 Challenge Round %d - %s", year, len(events)+1, trackName),
			TrackName: trackName,
			Date:      t,
			Organizer: organizer,
			SourceURL: fmt.Sprintf("%s/events?season=%d", baseURL, year),
		})
	}

	return events
}

// matchTrack maps a scraped track name to a track_id in the database.
func matchTrack(ctx context.Context, name string) (int, error) {
	// Normalize the scraped name to match our database entries
	nameMap := map[string]struct{ name, config string }{
		"Thunderhill East Cyclone": {"Thunderhill", "East Cyclone"},
		"Thunderhill East Bypass":  {"Thunderhill", "East Bypass"},
		"Thunderhill West CCW":     {"Thunderhill", "West CCW"},
		"Thunderhill West CW":      {"Thunderhill", "West CW"},
		"Thunderhill 5 Mile":       {"Thunderhill", "5-Mile"},
		"Laguna Seca":              {"Laguna Seca", "Full"},
		"Sonoma":                   {"Sonoma Raceway", "Long"},
		"Buttonwillow 13 CW":      {"Buttonwillow", "CW13"},
		"Buttonwillow 13 CCW":     {"Buttonwillow", "CCW13"},
		"Buttonwillow Circuit":     {"Buttonwillow", "CW13"},
		"Buttonwillow":             {"Buttonwillow", "CW13"},
	}

	mapped, ok := nameMap[name]
	if !ok {
		return 0, fmt.Errorf("unknown track: %s", name)
	}

	var id int
	err := models.DB.QueryRow(ctx,
		`SELECT id FROM tracks WHERE name = $1 AND config = $2`,
		mapped.name, mapped.config).Scan(&id)
	return id, err
}

// FetchWeatherForPastEvents fetches weather data for events that have already occurred
// but don't have weather data yet.
func FetchWeatherForPastEvents() error {
	ctx := context.Background()
	rows, err := models.DB.Query(ctx,
		`SELECT e.id, t.lat, t.lon, e.date
		 FROM events e
		 JOIN tracks t ON t.id = e.track_id
		 WHERE e.date < CURRENT_DATE
		   AND e.weather_temp_f IS NULL
		   AND t.lat IS NOT NULL`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var pending []struct {
		id   int
		lat  float64
		lon  float64
		date time.Time
	}
	for rows.Next() {
		var p struct {
			id   int
			lat  float64
			lon  float64
			date time.Time
		}
		if err := rows.Scan(&p.id, &p.lat, &p.lon, &p.date); err != nil {
			continue
		}
		pending = append(pending, p)
	}

	for _, p := range pending {
		if err := models.FetchAndStoreWeather(p.id, p.lat, p.lon, p.date); err != nil {
			log.Printf("Warning: failed to fetch weather for event %d: %v", p.id, err)
		} else {
			log.Printf("Fetched weather for event %d (date %s)", p.id, p.date.Format("2006-01-02"))
		}
		time.Sleep(500 * time.Millisecond) // rate limit
	}
	return nil
}

// ScrapeResults fetches the results page and extracts driver standings.
func ScrapeResults() error {
	body, err := fetchPage(baseURL + "/results")
	if err != nil {
		return fmt.Errorf("fetching results page: %w", err)
	}

	drivers := parseDrivers(body)
	log.Printf("Scraped %d drivers from 86challenge.us results", len(drivers))

	ctx := context.Background()
	for _, d := range drivers {
		_, err := models.DB.Exec(ctx,
			`INSERT INTO drivers (name, car_class)
			 VALUES ($1, $2)
			 ON CONFLICT (name) DO UPDATE SET
			   car_class = COALESCE(NULLIF(EXCLUDED.car_class, ''), drivers.car_class)`,
			d.Name, d.Class)
		if err != nil {
			log.Printf("Warning: failed to upsert driver %q: %v", d.Name, err)
		}
	}

	return nil
}

type scrapedDriver struct {
	Name  string
	Class string
}

func parseDrivers(html string) []scrapedDriver {
	var drivers []scrapedDriver
	seen := map[string]bool{}

	// The results page has class sections (Stock, Street, Touring, Unlimited)
	// with driver names listed under each.
	classes := []string{"stock", "street", "touring", "unlimited"}

	lower := strings.ToLower(html)
	for _, class := range classes {
		// Find the class section
		idx := strings.Index(lower, class)
		if idx < 0 {
			continue
		}

		// Extract names from this section (look for capitalized names)
		// Names typically appear as links or in table cells
		section := html[idx:min(len(html), idx+5000)]

		// Match patterns like "Ivan Larionov" (two capitalized words)
		nameRe := regexp.MustCompile(`([A-Z][a-z]+(?:\s+[A-Z][a-z]+)+)`)
		matches := nameRe.FindAllString(section, -1)

		for _, name := range matches {
			// Skip common false positives
			name = strings.TrimSpace(name)
			if len(name) < 5 || seen[name] {
				continue
			}
			if isCommonWord(name) {
				continue
			}
			seen[name] = true
			drivers = append(drivers, scrapedDriver{
				Name:  name,
				Class: class,
			})
		}
	}

	return drivers
}

func isCommonWord(s string) bool {
	common := []string{
		"Stock Class", "Street Class", "Touring Class", "Unlimited Class",
		"Best Round", "Total Points", "Speed Ventures",
		"Round Results", "Point Ties", "Privacy Policy",
	}
	for _, c := range common {
		if strings.Contains(s, c) || s == c {
			return true
		}
	}
	return false
}

func fetchPage(url string) (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
