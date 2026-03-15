package handlers

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/iryzhkov/86-challenge/models"
)

type CompareLap struct {
	LapID      int
	SessionID  string
	DriverName string
	LapTimeMs  int
	LapTime    string
	TrackName  string
	CarModel   string
	Color      string
	SplitTimes []int // sector times in ms
}

func ComparePage(w http.ResponseWriter, r *http.Request) {
	lapIDs := r.URL.Query().Get("laps")
	if lapIDs == "" {
		Templates["compare.html"].ExecuteTemplate(w, "base", nil)
		return
	}

	colors := []string{"#dc2626", "#2563eb", "#16a34a", "#7c3aed", "#ea580c"}
	var laps []CompareLap

	for _, idStr := range strings.Split(lapIDs, ",") {
		id, err := strconv.Atoi(strings.TrimSpace(idStr))
		if err != nil {
			continue
		}
		lap, session, err := models.GetLapWithSession(id)
		if err != nil {
			continue
		}
		laps = append(laps, CompareLap{
			LapID:      lap.ID,
			SessionID:  session.ID,
			DriverName: session.DriverName,
			LapTimeMs:  lap.LapTimeMs,
			LapTime:    FormatLapTime(lap.LapTimeMs),
			TrackName:  session.TrackName,
			CarModel:   session.CarModel,
			SplitTimes: lap.SplitTimesMs,
		})
	}

	// Sort by lap time — fastest first (becomes the reference)
	sort.Slice(laps, func(i, j int) bool {
		return laps[i].LapTimeMs < laps[j].LapTimeMs
	})

	// Assign colors after sorting
	for i := range laps {
		if i < len(colors) {
			laps[i].Color = colors[i]
		} else {
			laps[i].Color = colors[len(colors)-1]
		}
	}

	// Rebuild sorted lap IDs for the URL
	var sortedIDs []string
	for _, l := range laps {
		sortedIDs = append(sortedIDs, fmt.Sprintf("%d", l.LapID))
	}

	Templates["compare.html"].ExecuteTemplate(w, "base", map[string]any{
		"Laps":      laps,
		"LapIDsCSV": strings.Join(sortedIDs, ","),
	})
}
