package handlers

import (
	"net/http"

	"github.com/iryzhkov/86-challenge/models"
)

func SessionsListPage(w http.ResponseWriter, r *http.Request) {
	filters := models.SessionFilters{
		DriverName: r.URL.Query().Get("driver"),
		TrackName:  r.URL.Query().Get("track"),
		CarClass:   r.URL.Query().Get("class"),
		TireBrand:  r.URL.Query().Get("tire_brand"),
		TireModel:  r.URL.Query().Get("tire_model"),
		EventID:    r.URL.Query().Get("event_id"),
		DateFrom:   r.URL.Query().Get("date_from"),
		DateTo:     r.URL.Query().Get("date_to"),
	}

	sessions, _ := models.SearchSessions(filters, 100)

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

	drivers, _ := models.ListDriverNames()
	tracks, _ := models.ListTracks()
	events, _ := models.ListEvents()

	Templates["sessions.html"].ExecuteTemplate(w, "base", map[string]any{
		"Sessions": data,
		"Drivers":  drivers,
		"Tracks":   tracks,
		"Events":   events,
		"Filters":  filters,
	})
}
