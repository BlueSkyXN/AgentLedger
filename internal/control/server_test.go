package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cfg := config.Default()
	cfg.Database.Path = filepath.Join(t.TempDir(), "agent-ledger.db")
	cfg.Agents.Codex.Paths = []string{"~/private-codex"}
	base := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC).UnixMilli()
	_, err = database.Conn().Exec(`INSERT INTO usage_events (
		event_id, dedupe_key, dedupe_strategy, channel, provider, model_raw, model_normalized, timestamp_ms,
		session_id, message_id, input_tokens, output_tokens, total_tokens, output_duration_ms, output_tps,
		raw_usage_json, imported_at_ms, updated_at_ms
	) VALUES ('fp1', 'fp1', 'message_id', 'codex', 'openai', 'gpt-5', 'gpt-5', ?, 's1', 'm1', 100, 50, 150, 2500, 20.0, '{"secret":"hidden"}', 1, 1)`, base)
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
	return NewServer(cfg, database, Options{StaticDir: filepath.Join(t.TempDir(), "missing")})
}

func TestAPIHealthAndSummary(t *testing.T) {
	server := testServer(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/summary", nil)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v", err)
	}
	if payload["total_events"].(float64) != 1 {
		t.Fatalf("unexpected payload: %v", payload)
	}
}

func TestAPIValidation(t *testing.T) {
	server := testServer(t)
	cases := []string{
		"/api/v1/analytics/timeseries?bucket=hourly",
		"/api/v1/analytics/timeseries?by=raw",
		"/api/v1/analytics/breakdown?by=raw",
		"/api/v1/analytics/slow?sort=raw",
		"/api/v1/events?limit=0",
		"/api/v1/events?since=2026/05/01",
	}
	for _, path := range cases {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d body = %s", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestEventsConfigAndFilters(t *testing.T) {
	server := testServer(t)
	for _, path := range []string{"/api/v1/events", "/api/v1/config"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		server.Handler().ServeHTTP(recorder, request)
		body := recorder.Body.String()
		if strings.Contains(body, "raw_usage_json") || strings.Contains(body, "secret") {
			t.Fatalf("%s leaked private fields: %s", path, body)
		}
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/filter-options", nil)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("filter-options status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "codex") {
		t.Fatalf("filter options missing channel: %s", recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/v1/analytics/timeseries?bucket=daily&by=model", nil)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("timeseries breakdown status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"bucket"`) || !strings.Contains(recorder.Body.String(), "gpt-5") {
		t.Fatalf("timeseries breakdown missing bucket/model: %s", recorder.Body.String())
	}
}
