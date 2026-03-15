package handlers

import (
	"html/template"
	"net/http"

	"github.com/iryzhkov/86-challenge/models"
)

var Templates map[string]*template.Template

func Home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	sessions, _ := models.RecentSessions(10)

	type sessionWithBest struct {
		models.Session
		BestLapMs   int
		BestLapTime string
	}

	var data []sessionWithBest
	for _, s := range sessions {
		swb := sessionWithBest{Session: s}
		if best, err := models.BestLapForSession(s.ID); err == nil {
			swb.BestLapMs = best.LapTimeMs
			swb.BestLapTime = FormatLapTime(best.LapTimeMs)
		}
		data = append(data, swb)
	}

	// Current event: today's event, or the most recent past event (until the next one starts)
	currentEvent, _ := models.GetCurrentEvent()

	// Upcoming events (excluding the current one)
	upcoming, _ := models.ListUpcomingEvents(5)

	Templates["home.html"].ExecuteTemplate(w, "base", map[string]any{
		"RecentSessions": data,
		"CurrentEvent":   currentEvent,
		"UpcomingEvents": upcoming,
	})
}
