package handlers

import (
	"net/http"

	"github.com/iryzhkov/86-challenge/models"
)

func DriversListPage(w http.ResponseWriter, r *http.Request) {
	drivers, _ := models.ListAllDrivers()
	Templates["drivers.html"].ExecuteTemplate(w, "base", map[string]any{
		"Drivers": drivers,
	})
}
