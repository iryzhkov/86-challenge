package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/iryzhkov/86-challenge/models"
)

func DriverPage(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/driver/")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	driver, err := models.GetDriver(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Get best lap per track per class
	records, _ := models.DriverTrackRecords(id)

	// Get all sessions
	sessions, _ := models.GetSessionsForDriver(id)
	type sessionWithBest struct {
		models.Session
		BestLapMs   int
		BestLapTime string
	}
	var sessionData []sessionWithBest
	for _, s := range sessions {
		swb := sessionWithBest{Session: s}
		if best, err := models.BestLapForSession(s.ID); err == nil {
			swb.BestLapMs = best.LapTimeMs
			swb.BestLapTime = FormatLapTime(best.LapTimeMs)
		}
		sessionData = append(sessionData, swb)
	}

	Templates["driver.html"].ExecuteTemplate(w, "base", map[string]any{
		"Driver":   driver,
		"Records":  records,
		"Sessions": sessionData,
	})
}
