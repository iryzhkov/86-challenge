package handlers

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/iryzhkov/86-challenge/models"
)

func SessionView(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/session/")
	if id == "" {
		http.NotFound(w, r)
		return
	}

	session, err := models.GetSession(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	type splitDisplay struct {
		Value string
		Class string
	}
	type lapDisplay struct {
		models.Lap
		LapTimeFormatted string
		LapClass         string
		Splits           []splitDisplay
	}
	type sessionBlock struct {
		Session   *models.Session
		Laps      []lapDisplay
		IsCurrent bool
		Label     string // e.g. "9:22 AM Session 1"
	}

	// Get event info early (needed for timezone conversion)
	var event *models.Event
	if session.EventID != nil {
		event, _ = models.GetEvent(*session.EventID)
	}

	// Collect all sessions to display: current + related (same driver/class/event)
	// Sort chronologically by VBO start time
	allSessions := []*models.Session{session}
	related, _ := models.GetRelatedSessions(id, session.DriverID, session.EventID, session.CarClass)
	for i := range related {
		allSessions = append(allSessions, &related[i])
	}
	for i := 1; i < len(allSessions); i++ {
		for j := i; j > 0 && allSessions[j].StartTimeSecs < allSessions[j-1].StartTimeSecs; j-- {
			allSessions[j], allSessions[j-1] = allSessions[j-1], allSessions[j]
		}
	}

	// Get personal bests for highlighting
	numSplits := 0
	// First pass: determine max splits across all sessions
	for _, s := range allSessions {
		sLaps, _ := models.GetLapsForSession(s.ID)
		for _, l := range sLaps {
			if len(l.SplitTimesMs) > numSplits {
				numSplits = len(l.SplitTimesMs)
			}
		}
	}

	pbSplits, _ := models.PersonalBestSplits(session.DriverID, session.TrackID, numSplits)
	var eventSplits []int
	if session.EventID != nil {
		eventSplits, _ = models.EventBestSplits(session.DriverID, *session.EventID, numSplits)
	}
	pbLapMs, _ := models.PersonalBestLap(session.DriverID, session.TrackID)

	// Build session blocks
	var blocks []sessionBlock
	overallBestLapID := 0
	overallBestTime := 999999999

	for _, s := range allSessions {
		sLaps, err := models.GetLapsForSession(s.ID)
		if err != nil {
			log.Printf("Error loading laps for session %s: %v", s.ID, err)
			continue
		}

		block := sessionBlock{Session: s, IsCurrent: s.ID == id}
		sessionBestTime := 999999999
		sessionBestID := 0

		for _, l := range sLaps {
			dl := lapDisplay{
				Lap:              l,
				LapTimeFormatted: FormatLapTime(l.LapTimeMs),
			}

			if l.IsValid && l.LapTimeMs < sessionBestTime {
				sessionBestTime = l.LapTimeMs
				sessionBestID = l.ID
			}
			if l.IsValid && l.LapTimeMs < overallBestTime {
				overallBestTime = l.LapTimeMs
				overallBestLapID = l.ID
			}

			for i, sp := range l.SplitTimesMs {
				sd := splitDisplay{Value: FormatLapTime(sp)}
				if i < len(pbSplits) && sp > 0 && sp <= pbSplits[i] {
					sd.Class = "split-pb"
				} else if i < len(eventSplits) && sp > 0 && sp <= eventSplits[i] {
					sd.Class = "split-event"
				}
				dl.Splits = append(dl.Splits, sd)
			}

			block.Laps = append(block.Laps, dl)
		}

		// Set lap classes
		for i := range block.Laps {
			l := &block.Laps[i]
			if !l.IsValid {
				continue
			}
			if l.LapTimeMs == pbLapMs {
				l.LapClass = "lap-pb"
			} else if l.ID == sessionBestID {
				l.LapClass = "lap-best"
			}
		}

		blocks = append(blocks, block)
	}

	// Generate labels from VBO start time (seconds since midnight)
	// Sort by start time for numbering
	type idxTime struct {
		idx  int
		secs float64
	}
	var its []idxTime
	for i, b := range blocks {
		its = append(its, idxTime{i, b.Session.StartTimeSecs})
	}
	for i := 1; i < len(its); i++ {
		for j := i; j > 0 && its[j].secs < its[j-1].secs; j-- {
			its[j], its[j-1] = its[j-1], its[j]
		}
	}
	for seq, it := range its {
		secs := it.secs
		if secs > 0 {
			local := utcSecsToLocal(secs, event)
			h := int(local) / 3600
			m := (int(local) % 3600) / 60
			ampm := "AM"
			dh := h
			if dh >= 12 {
				ampm = "PM"
				if dh > 12 {
					dh -= 12
				}
			}
			if dh == 0 {
				dh = 12
			}
			blocks[it.idx].Label = fmt.Sprintf("%d:%02d %s Session %d", dh, m, ampm, seq+1)
		} else {
			blocks[it.idx].Label = fmt.Sprintf("Session %d", seq+1)
		}
	}

	var splitHeaders []string
	for i := 0; i < numSplits; i++ {
		splitHeaders = append(splitHeaders, fmt.Sprintf("S%d", i+1))
	}

	Templates["session.html"].ExecuteTemplate(w, "base", map[string]any{
		"Session":        session,
		"Event":          event,
		"SessionBlocks":  blocks,
		"BestLapID":      overallBestLapID,
		"NumSplits":      numSplits,
		"SplitHeaders":   splitHeaders,
		"HasMultiple":    len(blocks) > 1,
	})
}

// utcSecsToLocal converts UTC seconds-since-midnight to local Pacific time,
// accounting for DST using the event date. All NorCal tracks are America/Los_Angeles.
func utcSecsToLocal(utcSecs float64, event *models.Event) float64 {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return utcSecs - 8*3600 // fallback PST
	}

	// Use event date to determine DST offset
	var refDate time.Time
	if event != nil {
		refDate = event.Date
	} else {
		refDate = time.Now()
	}

	// Build a UTC time on the event date at the VBO time
	h := int(utcSecs) / 3600
	m := (int(utcSecs) % 3600) / 60
	s := int(utcSecs) % 60
	utcTime := time.Date(refDate.Year(), refDate.Month(), refDate.Day(), h, m, s, 0, time.UTC)
	localTime := utcTime.In(loc)

	return float64(localTime.Hour()*3600 + localTime.Minute()*60 + localTime.Second())
}

// FormatLapTime formats milliseconds as M:SS.mmm
func FormatLapTime(ms int) string {
	if ms <= 0 {
		return "-"
	}
	minutes := ms / 60000
	seconds := (ms % 60000) / 1000
	millis := ms % 1000
	if minutes > 0 {
		return fmt.Sprintf("%d:%02d.%03d", minutes, seconds, millis)
	}
	return fmt.Sprintf("%d.%03d", seconds, millis)
}

func DriverSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("driver_name")
	if len(q) < 2 {
		w.Write([]byte(""))
		return
	}

	drivers, err := models.SearchDrivers(q, 10)
	if err != nil {
		w.Write([]byte(""))
		return
	}

	var sb strings.Builder
	for _, d := range drivers {
		sb.WriteString(fmt.Sprintf(
			`<div class="p-2 hover:bg-gray-100 cursor-pointer"
			      onclick="document.getElementById('driver_name').value='%s'; document.getElementById('driver-results').innerHTML='';
			               document.querySelector('[name=car_class]').value='%s';
			               document.querySelector('[name=car_model]').value='%s';
			               document.querySelector('[name=tire_brand]').value='%s';
			               document.querySelector('[name=tire_model]').value='%s';
			               document.querySelector('[name=mod_points]').value='%d';">
				<span class="font-medium">%s</span>
				<span class="text-gray-500 text-sm ml-2">%s %s</span>
			</div>`,
			d.Name, d.CarClass, d.CarModel, d.TireBrand, d.TireModel, d.ModPoints,
			d.Name, d.CarModel, d.CarClass))
	}
	w.Write([]byte(sb.String()))
}

func DriverSetupAPI(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/driver/")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	d, err := models.GetDriver(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"name":"%s","car_class":"%s","car_generation":"%s","car_model":"%s","tire_brand":"%s","tire_model":"%s","mod_points":%d}`,
		d.Name, d.CarClass, d.CarGeneration, d.CarModel, d.TireBrand, d.TireModel, d.ModPoints)
}

// TelemetryAPI returns telemetry data as JSON for charts.
func TelemetryAPI(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/telemetry/")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	t, err := models.GetTelemetry(id)
	if err != nil {
		http.Error(w, "Telemetry not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, t)
}

// DownloadVBO serves the original VBO file for a session.
func DownloadVBO(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/session/download/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	session, err := models.GetSession(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	filePath := fmt.Sprintf("uploads/%s/%s", id, session.Filename)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, session.Filename))
	http.ServeFile(w, r, filePath)
}

// EventSearchAPI returns matching events as HTML for HTMX.
func EventSearchAPI(w http.ResponseWriter, r *http.Request) {
	trackIDStr := r.URL.Query().Get("track_id")
	date := r.URL.Query().Get("date")

	trackID, _ := strconv.Atoi(trackIDStr)
	events, err := models.SearchEvents(trackID, date)
	if err != nil || len(events) == 0 {
		w.Write([]byte(`<p class="text-gray-400 text-sm">No matching events found.</p>`))
		return
	}

	var sb strings.Builder
	for _, e := range events {
		sb.WriteString(fmt.Sprintf(
			`<a href="/upload?event=%d" class="block p-3 hover:bg-gray-50 border-b last:border-b-0">
				<span class="font-medium">%s</span>
				<span class="text-gray-500 text-sm ml-2">%s %s &mdash; %s</span>
			</a>`,
			e.ID, e.Name, e.TrackName, e.TrackConfig, e.Date.Format("Jan 2, 2006")))
	}
	w.Write([]byte(sb.String()))
}

func writeJSON(w io.Writer, t *models.Telemetry) {
	fmt.Fprintf(w, `{"distances":[`)
	for i, d := range t.DistancesM {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintf(w, "%.1f", d)
	}
	fmt.Fprintf(w, `],"speeds":[`)
	for i, s := range t.SpeedsKmh {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintf(w, "%.1f", s)
	}
	fmt.Fprintf(w, `],"rpms":[`)
	for i, r := range t.RPMs {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintf(w, "%.0f", r)
	}
	fmt.Fprintf(w, `],"brake":[`)
	for i, b := range t.BrakePct {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintf(w, "%.1f", b)
	}
	fmt.Fprintf(w, `],"throttle":[`)
	for i, th := range t.ThrottlePct {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintf(w, "%.1f", th)
	}
	fmt.Fprintf(w, `],"latG":[`)
	for i, g := range t.LatG {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintf(w, "%.3f", g)
	}
	fmt.Fprintf(w, `],"longG":[`)
	for i, g := range t.LongG {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintf(w, "%.3f", g)
	}
	fmt.Fprintf(w, `],"lats":[`)
	for i, l := range t.Latitudes {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintf(w, "%.6f", l)
	}
	fmt.Fprintf(w, `],"lons":[`)
	for i, l := range t.Longitudes {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprintf(w, "%.6f", l)
	}
	fmt.Fprint(w, `]}`)
}
