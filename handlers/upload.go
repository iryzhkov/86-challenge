package handlers

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/iryzhkov/86-challenge/models"
	"github.com/iryzhkov/86-challenge/track"
	"github.com/iryzhkov/86-challenge/vbo"
)

func UploadPage(w http.ResponseWriter, r *http.Request) {
	eventIDStr := r.URL.Query().Get("event")

	if eventIDStr != "" {
		eventID, err := strconv.Atoi(eventIDStr)
		if err != nil {
			http.Error(w, "Invalid event ID", http.StatusBadRequest)
			return
		}
		event, err := models.GetEvent(eventID)
		if err != nil {
			http.Error(w, "Event not found", http.StatusNotFound)
			return
		}
		Templates["upload.html"].ExecuteTemplate(w, "base", map[string]any{
			"Step":             2,
			"Event":            event,
			"DefaultTireBrand": "Yokohama",
			"DefaultTireModel": "V601",
		})
		return
	}

	recentEvents, _ := models.RecentPastEvents(5)
	tracks, _ := models.ListTracks()
	Templates["upload.html"].ExecuteTemplate(w, "base", map[string]any{
		"Step":         1,
		"RecentEvents": recentEvents,
		"Tracks":       tracks,
	})
}

func CreateEventAndRedirect(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("event_name"))
	trackIDStr := r.FormValue("track_id")
	date := r.FormValue("event_date")
	organizer := strings.TrimSpace(r.FormValue("organizer"))

	trackID, err := strconv.Atoi(trackIDStr)
	if err != nil || name == "" || date == "" {
		http.Error(w, "Name, track, and date are required", http.StatusBadRequest)
		return
	}

	parsedDate, err := time.Parse("2006-01-02", date)
	if err != nil {
		http.Error(w, "Invalid date format (use YYYY-MM-DD)", http.StatusBadRequest)
		return
	}

	if parsedDate.After(time.Now()) {
		http.Error(w, "Event date cannot be in the future", http.StatusBadRequest)
		return
	}

	eventID, _, err := models.CreateEvent(name, trackID, parsedDate, organizer)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create event: %v", err), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/upload?event=%d", eventID), http.StatusSeeOther)
}

func UploadProcess(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(200 << 20); err != nil { // 200MB max for multiple files
		http.Error(w, "Files too large", http.StatusBadRequest)
		return
	}

	eventIDStr := r.FormValue("event_id")
	eventID, err := strconv.Atoi(eventIDStr)
	if err != nil {
		http.Error(w, "No event selected", http.StatusBadRequest)
		return
	}
	event, err := models.GetEvent(eventID)
	if err != nil {
		http.Error(w, "Event not found", http.StatusNotFound)
		return
	}

	// Get driver info (shared across all files)
	driverName := strings.TrimSpace(r.FormValue("driver_name"))
	if driverName == "" {
		http.Error(w, "Driver name required", http.StatusBadRequest)
		return
	}
	driverID, err := models.GetOrCreateDriver(driverName)
	if err != nil {
		http.Error(w, "Failed to create driver", http.StatusInternalServerError)
		return
	}

	carClass := r.FormValue("car_class")
	carGen := r.FormValue("car_generation")
	carModel := r.FormValue("car_model")
	tireBrand := r.FormValue("tire_brand")
	tireModel := r.FormValue("tire_model")
	modPoints, _ := strconv.Atoi(r.FormValue("mod_points"))
	models.UpdateDriverSetup(driverID, carClass, carGen, carModel, tireBrand, tireModel, modPoints)

	// Process all uploaded VBO files
	files := r.MultipartForm.File["vbofile"]
	if len(files) == 0 {
		http.Error(w, "No files uploaded", http.StatusBadRequest)
		return
	}

	var sessionIDs []string
	var errors []string

	for _, fh := range files {
		// Check for duplicate upload
		if dup, _ := models.SessionExistsByFilename(driverID, eventID, fh.Filename); dup {
			errors = append(errors, fmt.Sprintf("%s: already uploaded for this driver and event", fh.Filename))
			continue
		}

		file, err := fh.Open()
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: failed to open", fh.Filename))
			continue
		}

		fileBytes, err := io.ReadAll(file)
		file.Close()
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: failed to read", fh.Filename))
			continue
		}

		sid, processErr := processVBOFile(fileBytes, fh.Filename, event, driverID, eventID,
			carClass, carGen, carModel, tireBrand, tireModel, r.FormValue("notes"), modPoints)
		if processErr != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", fh.Filename, processErr))
			continue
		}
		sessionIDs = append(sessionIDs, sid)
	}

	if len(sessionIDs) == 0 {
		http.Error(w, fmt.Sprintf("All uploads failed:\n%s", strings.Join(errors, "\n")), http.StatusBadRequest)
		return
	}

	// Log any partial errors
	for _, e := range errors {
		log.Printf("Upload warning: %s", e)
	}

	if len(sessionIDs) == 1 {
		http.Redirect(w, r, fmt.Sprintf("/session/%s", sessionIDs[0]), http.StatusSeeOther)
	} else {
		// Multiple sessions → redirect to event page
		http.Redirect(w, r, fmt.Sprintf("/event/%d", eventID), http.StatusSeeOther)
	}
}

func processVBOFile(fileBytes []byte, filename string, event *models.Event,
	driverID, eventID int, carClass, carGen, carModel, tireBrand, tireModel, notes string, modPoints int) (string, error) {

	parsed, err := vbo.Parse(bytes.NewReader(fileBytes))
	if err != nil {
		return "", fmt.Errorf("parse error: %w", err)
	}

	if len(parsed.Points) == 0 {
		return "", fmt.Errorf("no data points")
	}

	// Detect track and verify match
	trackName, trackConfig, found := track.DetectTrack(parsed.Points)
	if !found {
		return "", fmt.Errorf("could not detect track from GPS")
	}

	detectedTrackID, err := models.GetTrackIDByNameConfig(trackName, trackConfig)
	if err != nil {
		return "", fmt.Errorf("unknown track: %s %s", trackName, trackConfig)
	}

	if detectedTrackID != event.TrackID {
		return "", fmt.Errorf("track mismatch: VBO is %s %s, event is %s %s",
			trackName, trackConfig, event.TrackName, event.TrackConfig)
	}

	// Create session — use first data point's time-of-day
	var startTimeSecs float64
	if len(parsed.Points) > 0 {
		startTimeSecs = parsed.Points[0].Time
	}
	eid := eventID
	sessionID, err := models.CreateSession(driverID, &eid, event.TrackID, filename,
		carClass, carGen, carModel, tireBrand, tireModel, notes, modPoints, startTimeSecs)
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}

	// Save VBO file
	uploadDir := filepath.Join("uploads", sessionID)
	os.MkdirAll(uploadDir, 0755)
	os.WriteFile(filepath.Join(uploadDir, filename), fileBytes, 0644)

	// Split laps
	laps := track.SplitLaps(parsed.Points, parsed.SplitGates)

	for _, lap := range laps {
		var ambientTemp float32
		if lap.StartTime > 0 {
			// Convert VBO UTC time to local for weather lookup
			localSecs := utcToLocalSecs(lap.StartTime, event.Date)
			if t, err := models.LookupHourlyTemp(eventID, localSecs); err == nil {
				ambientTemp = t
			}
		}

		lapID, err := models.CreateLap(sessionID, lap.LapNumber, lap.LapTimeMs,
			lap.IsValid, lap.IsOutlap, lap.IsInlap, lap.SplitTimes, ambientTemp)
		if err != nil {
			log.Printf("Failed to create lap %d: %v", lap.LapNumber, err)
			continue
		}

		if err := models.StoreTelemetry(lapID, parsed.SampleRate, parsed.Points, lap.StartIdx, lap.EndIdx); err != nil {
			log.Printf("Failed to store telemetry for lap %d: %v", lap.LapNumber, err)
		}
	}

	log.Printf("Processed %s: %d laps, session %s", filename, len(laps), sessionID)
	return sessionID, nil
}

// utcToLocalSecs converts VBO UTC seconds-since-midnight to Pacific local time.
func utcToLocalSecs(utcSecs float64, eventDate time.Time) float64 {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return utcSecs - 8*3600
	}
	h := int(utcSecs) / 3600
	m := (int(utcSecs) % 3600) / 60
	s := int(utcSecs) % 60
	utcTime := time.Date(eventDate.Year(), eventDate.Month(), eventDate.Day(), h, m, s, 0, time.UTC)
	localTime := utcTime.In(loc)
	return float64(localTime.Hour()*3600 + localTime.Minute()*60 + localTime.Second())
}

// DeleteSession removes a session and all its data.
func DeleteSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.FormValue("session_id")
	if id == "" {
		http.Error(w, "Missing session ID", http.StatusBadRequest)
		return
	}

	session, err := models.GetSession(id)
	if err != nil {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	if err := models.DeleteSession(id); err != nil {
		http.Error(w, fmt.Sprintf("Failed to delete: %v", err), http.StatusInternalServerError)
		return
	}

	// Remove uploaded file
	os.RemoveAll(filepath.Join("uploads", id))

	// Redirect to event page if linked, otherwise home
	if session.EventID != nil {
		http.Redirect(w, r, fmt.Sprintf("/event/%d", *session.EventID), http.StatusSeeOther)
	} else {
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}
