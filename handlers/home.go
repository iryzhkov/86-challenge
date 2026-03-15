package handlers

import (
	"html/template"
	"net/http"
	"time"

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

	// Current event: most recent past event
	currentEvent, _ := models.GetCurrentEvent()

	// Upcoming events: up to 2, only within 3 weeks
	allUpcoming, _ := models.ListUpcomingEvents(10)
	threeWeeks := time.Now().AddDate(0, 0, 21)
	var upcoming []models.Event
	for _, e := range allUpcoming {
		if e.Date.Before(threeWeeks) && len(upcoming) < 2 {
			upcoming = append(upcoming, e)
		}
	}

	// Recent past events: last 2, excluding the current one
	var recentPast []models.Event
	if pastEvents, err := models.RecentPastEvents(3); err == nil {
		for _, e := range pastEvents {
			if currentEvent != nil && e.ID == currentEvent.ID {
				continue
			}
			if len(recentPast) < 2 {
				recentPast = append(recentPast, e)
			}
		}
	}

	Templates["home.html"].ExecuteTemplate(w, "base", map[string]any{
		"RecentSessions": data,
		"CurrentEvent":   currentEvent,
		"UpcomingEvents": upcoming,
		"RecentPast":     recentPast,
	})
}
