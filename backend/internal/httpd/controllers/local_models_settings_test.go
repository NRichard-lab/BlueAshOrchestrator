package controllers_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/localproviders"
)

func decodeLocalModelsSettings(t *testing.T, body []byte) controllers.LocalModelsSettingsResponse {
	t.Helper()
	var settings controllers.LocalModelsSettingsResponse
	if err := json.Unmarshal(body, &settings); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	return settings
}

func TestLocalModelsSettingsDefaults(t *testing.T) {
	r, server, _ := localProvidersRouter(t)
	rec := doJSON(t, r, http.MethodGet, "/local-models/settings", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	settings := decodeLocalModelsSettings(t, rec.Body.Bytes())
	// The approved default: a short note, with "none" one setting away.
	if settings.Instructions != "compact" || strings.Join(settings.InstructionsLevels, ",") != "standard,compact,none" {
		t.Errorf("instructions = %q of %v", settings.Instructions, settings.InstructionsLevels)
	}
	if settings.Default != "" || len(settings.Models) != 0 || len(settings.EndpointsWithKeys) != 0 {
		t.Errorf("fresh settings = %+v, want nothing set", settings)
	}
	if settings.Readiness.State != localproviders.ReadinessConfigured || settings.Readiness.ChatModels != 2 {
		t.Errorf("readiness = %+v, want configured with the two chat models", settings.Readiness)
	}
	server.RequireNoGeneration(t)
}

func TestLocalModelsSettingsRoundTrip(t *testing.T) {
	r, server, dir := localProvidersRouter(t)
	if err := localproviders.SetEndpointKey(dir, "lab", "sk-secret-value"); err != nil {
		t.Fatal(err)
	}
	server.RequireKey("sk-secret-value")
	rec := doJSON(t, r, http.MethodPut, "/local-models/settings",
		`{"default":"lab/hf.co/org/coder:Q4","instructions":"none","models":{"lab/qwen3.8:27b":{"context":16384,"tools":false}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "sk-secret-value") {
		t.Fatal("the settings response carries an endpoint key")
	}
	settings := decodeLocalModelsSettings(t, rec.Body.Bytes())
	if settings.Default != "lab/hf.co/org/coder:Q4" || settings.Instructions != "none" {
		t.Errorf("settings = %+v", settings)
	}
	override, ok := settings.Models["lab/qwen3.8:27b"]
	if !ok || override.Context == nil || *override.Context != 16384 || override.Tools == nil || *override.Tools {
		t.Errorf("override = %+v", settings.Models)
	}
	if len(settings.EndpointsWithKeys) != 1 || settings.EndpointsWithKeys[0] != "lab" {
		t.Errorf("endpointsWithKeys = %v, want the name only", settings.EndpointsWithKeys)
	}

	// An absent field is left as it is.
	rec = doJSON(t, r, http.MethodPut, "/local-models/settings", `{"instructions":"standard"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	settings = decodeLocalModelsSettings(t, rec.Body.Bytes())
	if settings.Default != "lab/hf.co/org/coder:Q4" || settings.Instructions != "standard" || len(settings.Models) != 1 {
		t.Errorf("after a partial update = %+v", settings)
	}

	// An empty default clears it.
	rec = doJSON(t, r, http.MethodPut, "/local-models/settings", `{"default":""}`)
	if settings = decodeLocalModelsSettings(t, rec.Body.Bytes()); rec.Code != http.StatusOK || settings.Default != "" {
		t.Errorf("clearing the default: status = %d settings = %+v", rec.Code, settings)
	}
	stored, err := localproviders.LoadSettings(dir)
	if err != nil || stored.Default != "" || stored.Instructions != localproviders.InstructionsStandard {
		t.Errorf("stored = %+v, %v", stored, err)
	}
	server.RequireNoGeneration(t)
}

func TestLocalModelsSettingsValidation(t *testing.T) {
	r, server, dir := localProvidersRouter(t)
	for name, body := range map[string]string{
		"not json":            `{`,
		"unknown level":       `{"instructions":"verbose"}`,
		"blank level":         `{"instructions":" "}`,
		"default without / ":  `{"default":"qwen3.8:27b"}`,
		"unknown endpoint":    `{"default":"nowhere/qwen3.8:27b"}`,
		"bad field kept out ": `{"instructions":"none","default":"nowhere/x"}`,
	} {
		rec := doJSON(t, r, http.MethodPut, "/local-models/settings", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d body=%s, want 400", name, rec.Code, rec.Body.String())
		}
	}
	// A rejected request changes nothing, even its valid fields.
	stored, err := localproviders.LoadSettings(dir)
	if err != nil || stored.InstructionsLevel() != localproviders.InstructionsCompact || stored.Default != "" {
		t.Errorf("stored after rejected requests = %+v, %v", stored, err)
	}
	server.RequireNoGeneration(t)
}
