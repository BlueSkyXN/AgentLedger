package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
	"github.com/BlueSkyXN/AgentLedger/internal/model"
)

func TestAPIV2ContractAndV1Removal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	event := &model.UsageEvent{
		EventID: "event", IdentityVersion: model.IdentityVersion, IdentityStrategy: "native_event", IdentityScope: "session",
		ContentSHA256: "hash", ParserVersion: "test-v1", EventGranularity: "request",
		Channel: "codex", SourceProduct: "codex-cli", Provider: "openai",
		ModelRaw: "unknown", ModelNormalized: "unknown", ModelResolution: model.ModelResolutionUnknown, ModelIsFallback: true,
		TimestampMs: 1_700_000_000_000, SessionKey: "session-key", SessionID: "native-session",
		InputTokens: 3, TotalTokens: 3, ImportedAtMs: 1, UpdatedAtMs: 1,
	}
	if _, err := database.UpsertEvent(event); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Database.Path = path
	cfg.Reports.Timezone = "UTC"
	server := httptest.NewServer(NewServer(cfg, database, Options{StaticDir: filepath.Join(t.TempDir(), "missing")}).Handler())
	defer server.Close()

	response, err := http.Get(server.URL + "/api/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("/api/v1 must be removed, got %d", response.StatusCode)
	}

	for _, path := range []string{
		"/api/v2/health", "/api/v2/status", "/api/v2/config",
		"/api/v2/analytics/summary", "/api/v2/analytics/timeseries?bucket=daily",
		"/api/v2/analytics/breakdown?by=source_product", "/api/v2/filter-options",
		"/api/v2/sessions?limit=1&offset=0", "/api/v2/events?limit=1&offset=0", "/api/v2/import-runs",
	} {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		if response.StatusCode != http.StatusOK {
			_ = response.Body.Close()
			t.Fatalf("GET %s status=%d", path, response.StatusCode)
		}
		var payload any
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			_ = response.Body.Close()
			t.Fatalf("GET %s invalid JSON: %v", path, err)
		}
		_ = response.Body.Close()
	}
}

func TestSessionsAndEventsArePaginatedWithoutRemovedFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for index, id := range []string{"a", "b"} {
		event := &model.UsageEvent{
			EventID: id, IdentityVersion: model.IdentityVersion, IdentityStrategy: "native_event", IdentityScope: "session",
			ContentSHA256: "hash-" + id, ParserVersion: "test-v1", EventGranularity: "request",
			Channel: "codex", SourceProduct: "codex-cli", ModelNormalized: "unknown", ModelResolution: model.ModelResolutionUnknown, ModelIsFallback: true,
			TimestampMs: 1_700_000_000_000 + int64(index), SessionKey: "session-" + id,
			SessionID: "native-session-" + id, ProjectPath: "/private/project-" + id,
			MessageID: "private-message-" + id, RequestID: "private-request-" + id,
			InputTokens: 1, TotalTokens: 1, ImportedAtMs: 1, UpdatedAtMs: 1,
		}
		if _, err := database.UpsertEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Database.Path = path
	cfg.Reports.Timezone = "UTC"
	handler := NewServer(cfg, database, Options{}).Handler()

	request := httptest.NewRequest(http.MethodGet, "/api/v2/events?limit=1&offset=1", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("events status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, required := range []string{`"items"`, `"limit":1`, `"offset":1`, `"total":2`, `"identity_strategy"`, `"session_key"`} {
		if !strings.Contains(body, required) {
			t.Errorf("events response missing %s: %s", required, body)
		}
	}
	for _, removed := range []string{
		"request_count", "ttft", "output_tps", "recorded_cost", "total_duration",
		"session_id", "project_path", "message_id", "request_id", "native-session", "/private/",
	} {
		if strings.Contains(body, removed) {
			t.Errorf("events response contains removed field %q: %s", removed, body)
		}
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v2/sessions?limit=1&offset=0", nil)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"total":2`) || !strings.Contains(recorder.Body.String(), `"primary_model"`) {
		t.Fatalf("sessions response status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAPIIsReadOnlyAndValidatesPagination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	cfg := config.Default()
	cfg.Database.Path = path
	handler := NewServer(cfg, database, Options{}).Handler()

	request := httptest.NewRequest(http.MethodPost, "/api/v2/status", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d", recorder.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v2/events?offset=-1", nil)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "invalid_request") {
		t.Fatalf("invalid offset status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestStaticHandlerRejectsSymlinkEscape(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	staticDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("inside-index"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("outside-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(staticDir, "leak.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	handler := NewServer(config.Default(), database, Options{StaticDir: staticDir}).Handler()
	request := httptest.NewRequest(http.MethodGet, "/leak.txt", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("symlink escape status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "outside-secret") {
		t.Fatal("static handler exposed a file outside the static root")
	}
}
