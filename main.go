package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strings"

	"github.com/iryzhkov/86-challenge/handlers"
	"github.com/iryzhkov/86-challenge/models"
	"github.com/iryzhkov/86-challenge/scraper"
)

func main() {
	cfg := LoadConfig()

	// Initialize database
	if err := models.InitDB(cfg.DatabaseURL); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	log.Println("Database connected and migrations applied")

	// Seed tracks
	if err := models.SeedTracks(); err != nil {
		log.Printf("Warning: failed to seed tracks: %v", err)
	}

	// Scrape events and drivers from 86challenge.us (non-blocking)
	go func() {
		if err := scraper.ScrapeEvents(); err != nil {
			log.Printf("Warning: event scrape failed: %v", err)
		}
		if err := scraper.ScrapeResults(); err != nil {
			log.Printf("Warning: results scrape failed: %v", err)
		}
		if err := scraper.FetchWeatherForPastEvents(); err != nil {
			log.Printf("Warning: weather fetch failed: %v", err)
		}
	}()

	// Parse templates — each page gets its own template set (base + page)
	funcMap := template.FuncMap{
		"add":    func(a, b int) int { return a + b },
		"eq":     func(a, b interface{}) bool { return a == b },
		"title":  strings.Title,
		"printf": func(f string, args ...interface{}) string { return fmt.Sprintf(f, args...) },
	}
	pages := []string{
		"home.html", "upload.html", "session.html", "leaderboard.html",
		"compare.html", "driver.html", "drivers.html", "event.html", "events.html", "sessions.html",
		"simulate.html",
	}
	handlers.Templates = make(map[string]*template.Template)
	for _, page := range pages {
		t, err := template.New("").Funcs(funcMap).ParseFiles("templates/base.html", "templates/"+page)
		if err != nil {
			log.Fatalf("Failed to parse template %s: %v", page, err)
		}
		handlers.Templates[page] = t
	}

	// Routes
	mux := http.NewServeMux()

	// Pages
	mux.HandleFunc("/", handlers.Home)
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			handlers.UploadProcess(w, r)
		} else {
			handlers.UploadPage(w, r)
		}
	})
	mux.HandleFunc("/upload/create-event", handlers.CreateEventAndRedirect)
	mux.HandleFunc("/session/delete", handlers.DeleteSession)
	mux.HandleFunc("/session/download/", handlers.DownloadVBO)
	mux.HandleFunc("/session/simulate/", handlers.SimulatePage)
	mux.HandleFunc("/session/", handlers.SessionView)
	mux.HandleFunc("/leaderboard", handlers.LeaderboardPage)
	mux.HandleFunc("/compare", handlers.ComparePage)
	mux.HandleFunc("/drivers", handlers.DriversListPage)
	mux.HandleFunc("/driver/", handlers.DriverPage)
	mux.HandleFunc("/sessions", handlers.SessionsListPage)
	mux.HandleFunc("/events", handlers.EventsListPage)
	mux.HandleFunc("/event/", handlers.EventPage)

	// API endpoints
	mux.HandleFunc("/api/drivers", handlers.DriverSearch)
	mux.HandleFunc("/api/driver/", handlers.DriverSetupAPI)
	mux.HandleFunc("/api/telemetry/", handlers.TelemetryAPI)
	mux.HandleFunc("/api/simulate/", handlers.SimulateAPI)
	mux.HandleFunc("/api/events", handlers.EventSearchAPI)

	// Static files
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	log.Printf("Starting server on :%s", cfg.Port)
	if err := http.ListenAndServe(":"+cfg.Port, mux); err != nil {
		log.Fatal(err)
	}
}
