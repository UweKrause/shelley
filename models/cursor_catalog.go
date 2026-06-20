package models

import (
	"context"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"shelley.exe.dev/llm/cursor"
	"shelley.exe.dev/llm"
)

const CursorModelPrefix = "cursor-"

// DiscoverCursorModels returns catalog entries for models reported by
// cursor-agent models. IDs are prefixed with "cursor-" to avoid
// colliding with direct-provider catalog entries.
func DiscoverCursorModels(ctx context.Context, logger *slog.Logger) []Model {
	if !cursor.Available() {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "cursor-agent", "models")
	out, err := cmd.Output()
	if err != nil {
		if logger != nil {
			logger.Warn("Cursor model discovery failed", "error", err)
		}
		return nil
	}

	var models []Model
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.EqualFold(line, "Available models") {
			continue
		}
		id, label, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		id = strings.TrimSpace(id)
		label = strings.TrimSpace(label)
		if id == "" {
			continue
		}
		apiModel := id
		catalogID := CursorModelPrefix + id
		desc := label
		if desc == "" {
			desc = id
		}
		models = append(models, Model{
			ID:             catalogID,
			Provider:       ProviderCursor,
			Description:    desc + " (Cursor)",
			APIModelName:   apiModel,
			APIType:        APITypeCursor,
			DefaultBaseURL: "cursor-agent",
			Build:          cursorBuild(apiModel),
		})
	}
	if logger != nil && len(models) > 0 {
		logger.Info("Discovered Cursor models", "count", len(models))
	}
	return models
}

func cursorBuild(apiModel string) func(baseURL, apiKey string, httpc *http.Client) llm.Service {
	model := apiModel
	return func(_, apiKey string, _ *http.Client) llm.Service {
		return &cursor.Service{
			Model:  model,
			APIKey: apiKey,
		}
	}
}

// IsCursorModel reports whether modelID is a built-in Cursor-backed model.
func IsCursorModel(modelID string) bool {
	return strings.HasPrefix(modelID, CursorModelPrefix)
}

// BindConversationLLM wraps cursor-backed services with per-conversation
// session state. Other providers are returned unchanged.
func BindConversationLLM(svc llm.Service, modelID, conversationID, workingDir, sessionID string, onSession func(string) error) llm.Service {
	if !IsCursorModel(modelID) {
		return svc
	}
	bind := func(inner llm.Service) llm.Service {
		cs, ok := inner.(*cursor.Service)
		if !ok {
			return inner
		}
		return cs.Bind(conversationID, workingDir, sessionID, onSession)
	}
	if ls, ok := svc.(*loggingService); ok {
		return &loggingService{
			service:  bind(ls.service),
			logger:   ls.logger,
			modelID:  ls.modelID,
			provider: ls.provider,
		}
	}
	return bind(svc)
}

// PreferredCursorModelID picks a sensible default Cursor catalog entry.
func PreferredCursorModelID(catalog []Model) string {
	for _, id := range []string{"cursor-composer-2.5", "cursor-auto"} {
		for _, m := range catalog {
			if m.ID == id {
				return id
			}
		}
	}
	for _, m := range catalog {
		if IsCursorModel(m.ID) {
			return m.ID
		}
	}
	return ""
}
