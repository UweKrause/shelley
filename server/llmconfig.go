package server

import (
	"log/slog"
	"net/http"

	"shelley.exe.dev/db"
	"shelley.exe.dev/models"
)

// Link represents a link displayed in the UI overflow menu.
type Link struct {
	Title   string `json:"title"`
	IconSVG string `json:"icon_svg,omitempty"` // SVG path data for the icon
	URL     string `json:"url"`
}

// LLMConfig holds all configuration for LLM services.
type LLMConfig struct {
	// Models is the list of ready-to-use built-in models. The server
	// registers them as-is; custom models are loaded separately from DB.
	Models []models.Built

	// DefaultModel is the default model to use (optional, defaults to models.Default()).
	DefaultModel string

	// DB is the database for custom models (optional).
	DB *db.DB

	// HTTPC is the shared HTTP client used by both the built-in models
	// (already baked into Models[*].Service) and custom DB-backed models
	// the Manager constructs. Pass nil to let the Manager create one.
	HTTPC *http.Client

	Logger *slog.Logger
}
