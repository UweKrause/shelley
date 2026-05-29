package modelsources

import (
	"net/http"
	"testing"

	"shelley.exe.dev/models"
)

func findBuilt(bs []models.Built, id string) *models.Built {
	for i := range bs {
		if bs[i].ID == id {
			return &bs[i]
		}
	}
	return nil
}

func TestPredictableBuilds(t *testing.T) {
	bs := Build(models.All(), []Source{Predictable()}, &http.Client{}, nil)
	if b := findBuilt(bs, "predictable"); b == nil {
		t.Fatalf("predictable not built; got %v", bs)
	}
}

func TestEnvSourceBuildsAllProviders(t *testing.T) {
	src := Env("a", "o", "g", "f")
	bs := Build(models.All(), []Source{src}, &http.Client{}, nil)
	// Order must match catalog order.
	var expected []string
	for _, m := range models.All() {
		// Env source covers Anthropic/OpenAI/Gemini/Fireworks only.
		switch m.Provider {
		case models.ProviderAnthropic, models.ProviderOpenAI, models.ProviderGemini, models.ProviderFireworks:
			expected = append(expected, m.ID)
		}
	}
	if len(bs) != len(expected) {
		t.Fatalf("built count %d != expected %d (got %v)", len(bs), len(expected), bs)
	}
	for i := range bs {
		if bs[i].ID != expected[i] {
			t.Errorf("index %d: got %q want %q", i, bs[i].ID, expected[i])
		}
	}
}

func TestEnvSourceLabels(t *testing.T) {
	bs := Build(models.All(), []Source{Env("a", "o", "g", "f")}, &http.Client{}, nil)
	for _, tt := range []struct {
		id, want string
	}{
		{"claude-opus-4.6", "$ANTHROPIC_API_KEY"},
		{"gpt-5.5", "$OPENAI_API_KEY"},
		{"gemini-3-pro", "$GEMINI_API_KEY"},
		{"gpt-oss-20b-fireworks", "$FIREWORKS_API_KEY"},
	} {
		b := findBuilt(bs, tt.id)
		if b == nil {
			t.Errorf("missing %q", tt.id)
			continue
		}
		if b.Source != tt.want {
			t.Errorf("%s source = %q, want %q", tt.id, b.Source, tt.want)
		}
	}
}

func TestGatewaySourceLabels(t *testing.T) {
	// Plain gateway.
	bs := Build(models.All(), []Source{Gateway("https://gw.example.com", "", "", "")}, &http.Client{}, nil)
	if b := findBuilt(bs, "claude-opus-4.6"); b == nil || b.Source != "exe.dev gateway" {
		t.Errorf("claude-opus-4.6 with plain gateway: %+v", b)
	}
	if b := findBuilt(bs, "gemini-3-pro"); b != nil {
		t.Errorf("gemini-3-pro should not be built by gateway, got %+v", b)
	}

	// Gateway with explicit anthropic key: provider label switches.
	bs = Build(models.All(), []Source{Gateway("https://gw.example.com", "real-key", "", "")}, &http.Client{}, nil)
	if b := findBuilt(bs, "claude-opus-4.6"); b == nil || b.Source != "$ANTHROPIC_API_KEY" {
		t.Errorf("claude-opus-4.6 with explicit anthropic key: %+v", b)
	}
	if b := findBuilt(bs, "gpt-5.5"); b == nil || b.Source != "exe.dev gateway" {
		t.Errorf("gpt-5.5 should still be gateway: %+v", b)
	}
}

func TestLLMIntegrationSourceLabelsAndFiltering(t *testing.T) {
	integ := &LLMIntegrationConfig{
		Name: "llm", Host: "llm.int.exe.xyz", URL: "https://llm.int.exe.xyz",
		Models: []IntegrationModel{
			{ID: "claude-opus-4-7"},
			{ID: "gpt-5.5"},
			{ID: "accounts/fireworks/models/glm-5p1"},
			{ID: "accounts/fireworks/models/gpt-oss-20b"},
		},
	}
	bs := Build(models.All(), []Source{LLMIntegration(integ, ""), Predictable()}, &http.Client{}, nil)
	wantLabel := "exe.dev llm integration (llm.int.exe.xyz)"
	for _, id := range []string{"claude-opus-4.7", "gpt-5.5", "glm-5.1-fireworks", "gpt-oss-20b-fireworks"} {
		b := findBuilt(bs, id)
		if b == nil {
			t.Errorf("%q should be built", id)
			continue
		}
		if b.Source != wantLabel {
			t.Errorf("%s source = %q, want %q", id, b.Source, wantLabel)
		}
	}
	for _, id := range []string{"gemini-3-pro", "gemini-3-flash", "claude-opus-4.6", "claude-sonnet-4.6"} {
		if b := findBuilt(bs, id); b != nil {
			t.Errorf("%q should NOT be built, got %+v", id, b)
		}
	}
	if findBuilt(bs, "predictable") == nil {
		t.Errorf("predictable should survive integration filter")
	}
}

func TestMultipleLLMIntegrationsUnionWithSuffix(t *testing.T) {
	primary := &LLMIntegrationConfig{
		Name: "llm", Host: "llm.int.exe.xyz", URL: "https://llm.int.exe.xyz",
		Models: []IntegrationModel{{ID: "claude-opus-4-7"}, {ID: "gpt-5.5"}},
	}
	secondary := &LLMIntegrationConfig{
		Name: "llm2", Host: "llm2.int.exe.xyz", URL: "https://llm2.int.exe.xyz",
		Models: []IntegrationModel{{ID: "claude-opus-4-7"}, {ID: "claude-sonnet-4-6"}},
	}
	bs := Build(models.All(), []Source{
		LLMIntegration(primary, ""),
		LLMIntegration(secondary, "@llm2"),
		Predictable(),
	}, &http.Client{}, nil)
	for _, id := range []string{"claude-opus-4.7", "gpt-5.5", "claude-opus-4.7@llm2", "claude-sonnet-4.6@llm2"} {
		if findBuilt(bs, id) == nil {
			t.Errorf("missing %q", id)
		}
	}
	if b := findBuilt(bs, "claude-opus-4.7"); b == nil || b.Source != "exe.dev llm integration (llm.int.exe.xyz)" {
		t.Errorf("primary collision lost: %+v", b)
	}
	if b := findBuilt(bs, "claude-opus-4.7@llm2"); b == nil || b.Source != "exe.dev llm integration (llm2.int.exe.xyz)" {
		t.Errorf("suffixed model wrong: %+v", b)
	}
}

func TestBuiltBaseURLResolution(t *testing.T) {
	// Env source supplies no URL: BaseURL should be the catalog default.
	bs := Build(models.All(), []Source{Env("a", "o", "g", "f")}, &http.Client{}, nil)
	for _, tt := range []struct {
		id, want string
	}{
		{"claude-opus-4.6", "https://api.anthropic.com"},
		{"gpt-5.5", "https://api.openai.com"},
		{"gpt-oss-20b-fireworks", "https://api.fireworks.ai/inference"},
		{"gemini-3-pro", "https://generativelanguage.googleapis.com"},
	} {
		b := findBuilt(bs, tt.id)
		if b == nil {
			t.Errorf("missing %q", tt.id)
			continue
		}
		if b.BaseURL != tt.want {
			t.Errorf("%s BaseURL = %q, want %q", tt.id, b.BaseURL, tt.want)
		}
	}

	// LLM-integration source supplies a URL: BaseURL should be that URL.
	integ := &LLMIntegrationConfig{
		Name: "llm", Host: "llm.int.exe.xyz", URL: "https://llm.int.exe.xyz",
		Models: []IntegrationModel{{ID: "claude-opus-4-7"}, {ID: "gpt-5.5"}},
	}
	bs = Build(models.All(), []Source{LLMIntegration(integ, "")}, &http.Client{}, nil)
	if b := findBuilt(bs, "claude-opus-4.7"); b == nil || b.BaseURL != "https://llm.int.exe.xyz" {
		t.Errorf("claude-opus-4.7 BaseURL: %+v", b)
	}
	if b := findBuilt(bs, "gpt-5.5"); b == nil || b.BaseURL != "https://llm.int.exe.xyz" {
		t.Errorf("gpt-5.5 BaseURL: %+v", b)
	}
}

func TestBuiltAPITypePopulated(t *testing.T) {
	bs := Build(models.All(), []Source{Env("a", "o", "g", "f"), Predictable()}, &http.Client{}, nil)
	for _, tt := range []struct {
		id   string
		want models.APIType
	}{
		{"claude-opus-4.6", models.APITypeAnthropicMessages},
		{"gpt-5.5", models.APITypeOpenAIResponses},
		{"gpt-oss-20b-fireworks", models.APITypeOpenAIChat},
		{"gemini-3-pro", models.APITypeGemini},
		{"predictable", models.APITypeBuiltIn},
	} {
		b := findBuilt(bs, tt.id)
		if b == nil {
			t.Errorf("missing %q", tt.id)
			continue
		}
		if b.APIType != tt.want {
			t.Errorf("%s APIType = %q, want %q", tt.id, b.APIType, tt.want)
		}
	}
}
