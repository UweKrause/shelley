// Package modelsources composes built-in Shelley models from credential
// origins (exe.dev LLM integrations, the exe.dev gateway, provider env
// vars, and the predictable test service) and materializes them into a
// flat []models.Built that the server can register directly.
package modelsources

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"time"

	"shelley.exe.dev/llm/llmhttp"
	"shelley.exe.dev/models"
)

// providerConn is the connection configuration for one upstream provider
// reachable from a single Source.
//
// `baseURL` is a BARE origin/prefix (e.g. "https://llm.int.exe.xyz")
// with NO API-protocol path on it. The per-API-type service factory in
// models.Model.Build appends "/v1", "/v1/messages", "/v1beta", etc. so
// sources never have to encode protocol details. Empty falls back to
// the catalog's DefaultBaseURL, which is also a bare origin.
type providerConn struct {
	baseURL string
	apiKey  string // "implicit" when credentials are injected at the network edge
}

// Source is one origin from which built-in Shelley models can be
// materialized into the server's Manager. Sources are evaluated in
// order; the first to claim an ID wins.
type Source struct {
	// label is the default human-readable origin shown in the UI.
	label string

	// idSuffix is appended to each materialized model ID (e.g. "@llm2")
	// to disambiguate when multiple sources serve overlapping models.
	idSuffix string

	// providers is the per-provider connection config. A nil entry means
	// this source does not serve that provider.
	providers map[models.Provider]*providerConn

	// providerLabels overrides label on a per-provider basis (used for
	// the env source where each provider has its own env-var name).
	providerLabels map[models.Provider]string

	// allowedAPIModels, when non-empty, restricts this source to models
	// whose APIModelName is in the set (used for LLM integrations).
	allowedAPIModels map[string]bool
}

func (s *Source) labelFor(p models.Provider) string {
	if l, ok := s.providerLabels[p]; ok {
		return l
	}
	return s.label
}

// Predictable returns a Source that materializes only the predictable
// test model. Always safe to include in any deployment.
func Predictable() Source {
	return Source{
		label:     "builtin",
		providers: map[models.Provider]*providerConn{models.ProviderBuiltIn: {}},
	}
}

// Gateway returns a Source for the exe.dev gateway. The gateway serves
// Anthropic, OpenAI, and Fireworks but not Gemini; Gemini models must
// come from an env-var or LLM-integration source. Any non-empty
// explicit per-provider key overrides the gateway's implicit credential.
func Gateway(gatewayURL, anthropicKey, openAIKey, fireworksKey string) Source {
	key := func(k string) string {
		if k != "" {
			return k
		}
		return "implicit"
	}
	return Source{
		label: "exe.dev gateway",
		providers: map[models.Provider]*providerConn{
			models.ProviderAnthropic: {baseURL: gatewayURL + "/anthropic", apiKey: key(anthropicKey)},
			models.ProviderOpenAI:    {baseURL: gatewayURL + "/openai", apiKey: key(openAIKey)},
			models.ProviderFireworks: {baseURL: gatewayURL + "/fireworks/inference", apiKey: key(fireworksKey)},
		},
		providerLabels: explicitEnvLabels(anthropicKey, openAIKey, fireworksKey),
	}
}

// Env returns a Source for direct-to-provider env-var credentials. Only
// providers with a non-empty key are included.
func Env(anthropicKey, openAIKey, geminiKey, fireworksKey string) Source {
	prov := map[models.Provider]*providerConn{}
	labels := map[models.Provider]string{}
	add := func(p models.Provider, k, env string) {
		if k == "" {
			return
		}
		prov[p] = &providerConn{apiKey: k}
		labels[p] = "$" + env
	}
	add(models.ProviderAnthropic, anthropicKey, "ANTHROPIC_API_KEY")
	add(models.ProviderOpenAI, openAIKey, "OPENAI_API_KEY")
	add(models.ProviderGemini, geminiKey, "GEMINI_API_KEY")
	add(models.ProviderFireworks, fireworksKey, "FIREWORKS_API_KEY")
	return Source{label: "env", providers: prov, providerLabels: labels}
}

// LLMIntegration returns a Source backed by one exe.dev "llm"
// integration. idSuffix, when non-empty, is appended to each
// materialized model ID to disambiguate multiple integrations.
func LLMIntegration(integ *LLMIntegrationConfig, idSuffix string) Source {
	allowed := make(map[string]bool, len(integ.Models))
	for _, m := range integ.Models {
		allowed[m.ID] = true
	}
	return Source{
		label:    fmt.Sprintf("exe.dev llm integration (%s)", integ.Host),
		idSuffix: idSuffix,
		providers: map[models.Provider]*providerConn{
			models.ProviderAnthropic: {baseURL: integ.URL, apiKey: "implicit"},
			models.ProviderOpenAI:    {baseURL: integ.URL, apiKey: "implicit"},
			models.ProviderFireworks: {baseURL: integ.URL, apiKey: "implicit"},
			// Gemini: the integration's /v1/models is OpenAI-shaped and does
			// not expose Gemini-native endpoints. Omit.
		},
		allowedAPIModels: allowed,
	}
}

// explicitEnvLabels returns providerLabels that overlay env-var-style
// labels on top of a gateway source for any provider whose key was set
// explicitly. Gemini is omitted because the gateway never serves it.
func explicitEnvLabels(anthropic, openAI, fireworks string) map[models.Provider]string {
	labels := map[models.Provider]string{}
	if anthropic != "" {
		labels[models.ProviderAnthropic] = "$ANTHROPIC_API_KEY"
	}
	if openAI != "" {
		labels[models.ProviderOpenAI] = "$OPENAI_API_KEY"
	}
	if fireworks != "" {
		labels[models.ProviderFireworks] = "$FIREWORKS_API_KEY"
	}
	return labels
}

// Build walks the catalog × sources and produces ready-to-use
// models.Built values. Order: each Source in turn (preserving catalog
// order within), first to claim an ID wins.
func Build(catalog []models.Model, sources []Source, httpc *http.Client, logger *slog.Logger) []models.Built {
	if logger == nil {
		logger = slog.Default()
	}
	if httpc == nil {
		httpc = llmhttp.NewClient(nil)
	}
	var out []models.Built
	seen := map[string]bool{}
	for _, src := range sources {
		for _, m := range catalog {
			conn := src.providers[m.Provider]
			if conn == nil {
				continue
			}
			if src.allowedAPIModels != nil && !src.allowedAPIModels[m.APIModelName] {
				continue
			}
			id := m.ID + src.idSuffix
			if seen[id] {
				continue
			}
			seen[id] = true
			svc := m.Build(conn.baseURL, conn.apiKey, httpc)
			label := src.labelFor(m.Provider)
			baseURL := conn.baseURL
			if baseURL == "" {
				baseURL = m.DefaultBaseURL
			}
			out = append(out, models.Built{
				ID:          id,
				DisplayName: id,
				Provider:    m.Provider,
				Tags:        m.Tags,
				Source:      label,
				Service:     svc,
				APIType:     m.APIType,
				BaseURL:     baseURL,
			})
			logger.Debug("Materialized model", "id", id, "source", label)
		}
	}
	return out
}

// --- exe.dev LLM integration discovery ------------------------------------

// integrationDiscoveryTimeout bounds each HTTP call made during exe.dev
// integration discovery. Generous so a slow upstream during /v1/models
// can't silently drop the integration.
const integrationDiscoveryTimeout = 30 * time.Second

// IntegrationModel is one entry from an LLM integration's /v1/models list.
type IntegrationModel struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by,omitempty"`
}

// LLMIntegrationConfig describes one exe.dev "llm" integration that
// proxies requests to upstream LLM providers using credentials injected
// at the network edge.
type LLMIntegrationConfig struct {
	// Name is the integration name (e.g. "llm").
	Name string

	// Host is the integration hostname (e.g. "llm.int.exe.xyz"), shown to
	// users in source labels.
	Host string

	// URL is the integration base URL (no trailing slash, no path).
	URL string

	// Models is the set of models the integration serves, in the order
	// returned by /v1/models.
	Models []IntegrationModel
}

type reflectionIntegration struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type reflectionIntegrationsResponse struct {
	Integrations []reflectionIntegration `json:"integrations"`
}

type openAIModelsList struct {
	Data []IntegrationModel `json:"data"`
}

// DiscoverLLMIntegrations looks up every integration of type "llm" via
// the reflection endpoint and returns the resolved configs, sorted by
// name. Returns nil when we are not on an exe.dev VM (no /exe.dev
// directory), reflection is unreachable, or no "llm" integration is
// registered. An integration whose /v1/models fetch fails is logged and
// skipped; other integrations are still returned. Intentionally
// best-effort so the caller can fall back to gateway/env-var
// configuration.
func DiscoverLLMIntegrations(ctx context.Context, httpc *http.Client, logger *slog.Logger) []*LLMIntegrationConfig {
	if logger == nil {
		logger = slog.Default()
	}
	if _, err := os.Stat("/exe.dev"); err != nil {
		return nil
	}
	if httpc == nil {
		httpc = http.DefaultClient
	}

	var ints reflectionIntegrationsResponse
	if !fetchJSON(ctx, httpc, "https://reflection.int.exe.xyz/integrations", &ints) {
		return nil
	}

	var names []string
	for _, i := range ints.Integrations {
		if i.Type == "llm" && i.Name != "" {
			names = append(names, i.Name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)

	var out []*LLMIntegrationConfig
	for _, name := range names {
		host := fmt.Sprintf("%s.int.exe.xyz", name)
		base := "https://" + host
		var ml openAIModelsList
		if !fetchJSON(ctx, httpc, base+"/v1/models", &ml) {
			logger.Warn("LLM integration discovery: /v1/models fetch failed; skipping", "name", name, "host", host)
			continue
		}
		if len(ml.Data) == 0 {
			logger.Warn("LLM integration discovery: /v1/models returned no models; skipping", "name", name, "host", host)
			continue
		}
		out = append(out, &LLMIntegrationConfig{
			Name:   name,
			Host:   host,
			URL:    base,
			Models: ml.Data,
		})
		logger.Info("Discovered exe.dev LLM integration", "name", name, "host", host, "models", len(ml.Data))
	}
	return out
}

// fetchJSON GETs url with a per-call timeout and decodes JSON into out.
// Returns false on any error (network, status, decode).
func fetchJSON(ctx context.Context, httpc *http.Client, url string, out any) bool {
	ctx, cancel := context.WithTimeout(ctx, integrationDiscoveryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	return json.NewDecoder(resp.Body).Decode(out) == nil
}
