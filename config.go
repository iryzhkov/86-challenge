package main

import (
	"os"
)

type Config struct {
	DatabaseURL   string
	AdminPassword string
	Port          string
}

func LoadConfig() Config {
	cfg := Config{
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		AdminPassword: os.Getenv("ADMIN_PASSWORD"),
		Port:          os.Getenv("PORT"),
	}
	if cfg.Port == "" {
		cfg.Port = "8086"
	}
	if cfg.DatabaseURL == "" {
		cfg.DatabaseURL = "postgres://challenge:changeme@localhost:5432/challenge?sslmode=disable"
	}
	if cfg.AdminPassword == "" {
		cfg.AdminPassword = "changeme"
	}
	return cfg
}
