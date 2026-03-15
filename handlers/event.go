package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/iryzhkov/86-challenge/models"
)

func EventPage(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/event/")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	event, err := models.GetEvent(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	sessions, _ := models.GetSessionsForEvent(id)

	// Build per-class leaderboard: best lap per driver per class, with splits
	type entry struct {
		Rank       int
		DriverName string
		DriverID   int
		CarClass   string
		CarModel   string
		BestLapMs  int
		BestLap    string
		Splits     []string
		TireBrand  string
		TireModel  string
		TempF      float32
		SessionID  string
		LapID      int
	}

	classOrder := []string{"stock", "street", "touring", "unlimited"}
	// Track best per driver (across multiple sessions at same event)
	type driverKey struct {
		name  string
		class string
	}
	bestByDriver := map[driverKey]*entry{}

	for _, s := range sessions {
		// Get best lap for this session
		best, err := models.BestLapForSession(s.ID)
		if err != nil {
			continue
		}

		key := driverKey{s.DriverName, strings.ToLower(s.CarClass)}
		existing := bestByDriver[key]
		if existing == nil || best.LapTimeMs < existing.BestLapMs {
			var splits []string
			for _, sp := range best.SplitTimesMs {
				splits = append(splits, FormatLapTime(sp))
			}
			bestByDriver[key] = &entry{
				DriverName: s.DriverName,
				DriverID:   s.DriverID,
				CarClass:   s.CarClass,
				CarModel:   s.CarModel,
				BestLapMs:  best.LapTimeMs,
				BestLap:    FormatLapTime(best.LapTimeMs),
				Splits:     splits,
				TireBrand:  s.TireBrand,
				TireModel:  s.TireModel,
				TempF:      best.AmbientTempF,
				SessionID:  s.ID,
				LapID:      best.ID,
			}
		}
	}

	// Group by class and sort
	classEntries := map[string][]entry{}
	for _, e := range bestByDriver {
		cls := strings.ToLower(e.CarClass)
		if cls == "" {
			cls = "unclassed"
		}
		classEntries[cls] = append(classEntries[cls], *e)
	}
	for cls := range classEntries {
		entries := classEntries[cls]
		for i := 1; i < len(entries); i++ {
			for j := i; j > 0 && entries[j].BestLapMs < entries[j-1].BestLapMs; j-- {
				entries[j], entries[j-1] = entries[j-1], entries[j]
			}
		}
		for i := range entries {
			entries[i].Rank = i + 1
		}
		classEntries[cls] = entries
	}

	type classBlock struct {
		Name    string
		Entries []entry
	}
	var classes []classBlock
	for _, cls := range classOrder {
		if entries, ok := classEntries[cls]; ok {
			classes = append(classes, classBlock{
				Name:    strings.ToUpper(cls[:1]) + cls[1:],
				Entries: entries,
			})
		}
	}
	if entries, ok := classEntries["unclassed"]; ok {
		classes = append(classes, classBlock{Name: "Unclassed", Entries: entries})
	}

	// Determine number of splits for table headers
	numSplits := 0
	for _, cls := range classes {
		for _, e := range cls.Entries {
			if len(e.Splits) > numSplits {
				numSplits = len(e.Splits)
			}
		}
	}

	// Build split range for template iteration
	splitRange := make([]int, numSplits)
	for i := range splitRange {
		splitRange[i] = i
	}

	Templates["event.html"].ExecuteTemplate(w, "base", map[string]any{
		"Event":      event,
		"Classes":    classes,
		"NumSplits":  numSplits,
		"SplitRange": splitRange,
	})
}

func EventsListPage(w http.ResponseWriter, r *http.Request) {
	events, _ := models.ListEvents()
	Templates["events.html"].ExecuteTemplate(w, "base", map[string]any{
		"Events": events,
	})
}
