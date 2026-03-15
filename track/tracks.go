package track

// TrackDef defines a known track and its configurations.
type TrackDef struct {
	Name       string
	Config     string
	CenterLat  float64
	CenterLon  float64
	// Start/finish gate (optional, for tracks without VBO laptiming)
	StartLat1  float64
	StartLon1  float64
	StartLat2  float64
	StartLon2  float64
	// Discriminator: a GPS point that the trace MUST pass within DiscRadius meters
	// of for this config to match. Used to distinguish configs that share a center.
	// Zero means no discriminator check.
	DiscLat    float64
	DiscLon    float64
	DiscRadius float64 // meters
	// Direction: "CW" or "CCW" if this config requires a specific winding direction.
	// Empty string means direction doesn't matter for this config.
	Direction string
}

// KnownTracks contains seed data for NorCal tracks used by 86 Challenge.
var KnownTracks = []TrackDef{
	{
		Name:      "Laguna Seca",
		Config:    "Full",
		CenterLat: 36.5842,
		CenterLon: -121.7534,
	},
	{
		Name:      "Sonoma Raceway",
		Config:    "Long",
		CenterLat: 38.1615,
		CenterLon: -122.4556,
		// Carousel section — only on Long config
		DiscLat:    38.1590,
		DiscLon:    -122.4620,
		DiscRadius: 400,
	},
	{
		Name:      "Sonoma Raceway",
		Config:    "Short",
		CenterLat: 38.1615,
		CenterLon: -122.4556,
	},
	{
		Name:      "Thunderhill",
		Config:    "East Cyclone",
		CenterLat: 39.5383,
		CenterLon: -122.3316,
		// Cyclone turns section — only visited on Cyclone config
		DiscLat:    39.5402,
		DiscLon:    -122.3320,
		DiscRadius: 45,
	},
	{
		Name:      "Thunderhill",
		Config:    "East Bypass",
		CenterLat: 39.5383,
		CenterLon: -122.3316,
		// Bypass road — only visited on Bypass config
		DiscLat:    39.5407,
		DiscLon:    -122.3300,
		DiscRadius: 45,
	},
	{
		Name:      "Thunderhill",
		Config:    "West CW",
		CenterLat: 39.5383,
		CenterLon: -122.3316,
		Direction:  "CW",
	},
	{
		Name:      "Thunderhill",
		Config:    "West CCW",
		CenterLat: 39.5383,
		CenterLon: -122.3316,
		Direction:  "CCW",
	},
	{
		Name:      "Thunderhill",
		Config:    "5-Mile",
		CenterLat: 39.5383,
		CenterLon: -122.3316,
	},
	{
		Name:      "Buttonwillow",
		Config:    "CW13",
		CenterLat: 35.4906,
		CenterLon: -119.5442,
	},
	{
		Name:      "Buttonwillow",
		Config:    "CCW13",
		CenterLat: 35.4906,
		CenterLon: -119.5442,
	},
	{
		Name:      "Buttonwillow",
		Config:    "CW1",
		CenterLat: 35.4906,
		CenterLon: -119.5442,
	},
}
