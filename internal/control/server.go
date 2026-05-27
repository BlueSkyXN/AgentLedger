package control

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BlueSkyXN/AgentLedger/internal/analytics"
	"github.com/BlueSkyXN/AgentLedger/internal/config"
	"github.com/BlueSkyXN/AgentLedger/internal/db"
)

//go:embed embed/placeholder.html
var embeddedFS embed.FS

const Version = "0.1.0"

type Options struct {
	StaticDir string
}

type Server struct {
	cfg       *config.Config
	database  *db.Database
	staticDir string
	assetMode string
}

func NewServer(cfg *config.Config, database *db.Database, options Options) *Server {
	staticDir := strings.TrimSpace(options.StaticDir)
	if staticDir == "" {
		staticDir = "web/dist"
	}
	assetMode := "embedded-placeholder"
	if hasStaticPanel(staticDir) {
		assetMode = "filesystem"
	}
	return &Server{cfg: cfg, database: database, staticDir: staticDir, assetMode: assetMode}
}

func (s *Server) AssetMode() string {
	return s.assetMode
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/", s.handleAPI)
	mux.HandleFunc("/", s.handleStatic)
	return mux
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "read-only API only supports GET")
		return
	}

	switch r.URL.Path {
	case "/api/v1/health":
		s.handleHealth(w, r)
	case "/api/v1/status":
		s.handleStatus(w, r)
	case "/api/v1/config":
		s.handleConfig(w, r)
	case "/api/v1/analytics/summary":
		s.handleSummary(w, r)
	case "/api/v1/analytics/timeseries":
		s.handleTimeseries(w, r)
	case "/api/v1/analytics/breakdown":
		s.handleBreakdown(w, r)
	case "/api/v1/analytics/slow":
		s.handleSlow(w, r)
	case "/api/v1/filter-options":
		s.handleFilterOptions(w, r)
	case "/api/v1/sessions":
		s.handleSessions(w, r)
	case "/api/v1/import-runs":
		s.handleImportRuns(w, r)
	case "/api/v1/events":
		s.handleEvents(w, r)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	size := int64(0)
	if info, err := os.Stat(s.cfg.DBPath()); err == nil {
		size = info.Size()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"version":        Version,
		"database":       s.cfg.DBPath(),
		"database_bytes": size,
		"asset_mode":     s.assetMode,
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	stats, err := s.database.GetStats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"database":                s.cfg.DBPath(),
		"schema_version":          stats["schema_version"],
		"total_events":            stats["total_events"],
		"total_import_runs":       stats["total_import_runs"],
		"total_tokens":            stats["total_tokens"],
		"total_recorded_cost_usd": stats["total_recorded_cost_usd"],
	})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"config_path": redactPath(config.ConfigPath()),
		"database": map[string]any{
			"path": redactPath(s.cfg.DBPath()),
		},
		"import": map[string]any{
			"gracing_minutes": s.cfg.Import.GracingMinutes,
		},
		"agents": map[string]any{
			"claude": agentSnapshot(s.cfg.Agents.Claude),
			"codex":  agentSnapshot(s.cfg.Agents.Codex),
			"gemini": agentSnapshot(s.cfg.Agents.Gemini),
			"qwen":   agentSnapshot(s.cfg.Agents.Qwen),
		},
		"privacy_note": "面板 API 只读，不返回 raw usage JSON。",
	})
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	filters, ok := parseFilters(w, r, s.cfg.Reports.Timezone)
	if !ok {
		return
	}
	result, err := analytics.BuildSummary(s.database.Conn(), filters)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleTimeseries(w http.ResponseWriter, r *http.Request) {
	filters, ok := parseFilters(w, r, s.cfg.Reports.Timezone)
	if !ok {
		return
	}
	bucket := r.URL.Query().Get("bucket")
	by := strings.TrimSpace(r.URL.Query().Get("by"))
	if by != "" {
		rows, err := analytics.BuildTimeseriesBreakdown(s.database.Conn(), bucket, by, filters)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, rows)
		return
	}
	rows, err := analytics.BuildTimeseries(s.database.Conn(), bucket, filters)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) handleBreakdown(w http.ResponseWriter, r *http.Request) {
	filters, ok := parseFilters(w, r, s.cfg.Reports.Timezone)
	if !ok {
		return
	}
	rows, err := analytics.BuildBreakdown(s.database.Conn(), r.URL.Query().Get("by"), filters)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) handleSlow(w http.ResponseWriter, r *http.Request) {
	filters, ok := parseFilters(w, r, s.cfg.Reports.Timezone)
	if !ok {
		return
	}
	limit, ok := parseLimit(w, r, 50, 1, 500)
	if !ok {
		return
	}
	rows, err := analytics.BuildSlow(s.database.Conn(), r.URL.Query().Get("sort"), filters, limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) handleFilterOptions(w http.ResponseWriter, r *http.Request) {
	result, err := analytics.BuildFilterOptions(s.database.Conn())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	filters, ok := parseFilters(w, r, s.cfg.Reports.Timezone)
	if !ok {
		return
	}
	limit, ok := parseLimit(w, r, 50, 1, 500)
	if !ok {
		return
	}
	rows, err := analytics.BuildSessions(s.database.Conn(), filters, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) handleImportRuns(w http.ResponseWriter, r *http.Request) {
	limit, ok := parseLimit(w, r, 20, 1, 200)
	if !ok {
		return
	}
	rows, err := analytics.ListImportRuns(s.database.Conn(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	filters, ok := parseFilters(w, r, s.cfg.Reports.Timezone)
	if !ok {
		return
	}
	limit, ok := parseLimit(w, r, 200, 1, 1000)
	if !ok {
		return
	}
	rows, err := analytics.ListEvents(s.database.Conn(), filters, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if s.assetMode != "filesystem" {
		data, err := fs.ReadFile(embeddedFS, "embed/placeholder.html")
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
		return
	}

	root, err := filepath.Abs(s.staticDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rel := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
	if rel == "." || rel == string(filepath.Separator) {
		rel = "index.html"
	}
	candidate := filepath.Join(root, rel)
	absCandidate, err := filepath.Abs(candidate)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	relToRoot, err := filepath.Rel(root, absCandidate)
	if err != nil || relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) || filepath.IsAbs(relToRoot) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if info, err := os.Stat(absCandidate); err == nil && !info.IsDir() {
		http.ServeFile(w, r, absCandidate)
		return
	}
	http.ServeFile(w, r, filepath.Join(root, "index.html"))
}

func parseFilters(w http.ResponseWriter, r *http.Request, timezone string) (analytics.Filters, bool) {
	filters := analytics.Filters{
		Since:    strings.TrimSpace(r.URL.Query().Get("since")),
		Until:    strings.TrimSpace(r.URL.Query().Get("until")),
		Channel:  strings.TrimSpace(r.URL.Query().Get("channel")),
		Provider: strings.TrimSpace(r.URL.Query().Get("provider")),
		Model:    strings.TrimSpace(r.URL.Query().Get("model")),
		Session:  strings.TrimSpace(r.URL.Query().Get("session")),
		Timezone: timezone,
	}
	if filters.Since != "" && !validDate(filters.Since) {
		writeError(w, http.StatusBadRequest, "since must use YYYY-MM-DD")
		return filters, false
	}
	if filters.Until != "" && !validDate(filters.Until) {
		writeError(w, http.StatusBadRequest, "until must use YYYY-MM-DD")
		return filters, false
	}
	return filters, true
}

func parseLimit(w http.ResponseWriter, r *http.Request, defaultValue, minValue, maxValue int) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return defaultValue, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minValue || value > maxValue {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("limit must be between %d and %d", minValue, maxValue))
		return 0, false
	}
	return value, true
}

func validDate(value string) bool {
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func hasStaticPanel(staticDir string) bool {
	info, err := os.Stat(filepath.Join(staticDir, "index.html"))
	return err == nil && !info.IsDir()
}

func agentSnapshot(agent config.AgentConfig) map[string]any {
	paths := make([]string, 0, len(agent.Paths))
	for _, path := range agent.Paths {
		paths = append(paths, redactPath(path))
	}
	return map[string]any{"enabled": agent.Enabled, "paths": paths}
}

func redactPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	cleanHome := filepath.Clean(home)
	cleanPath := filepath.Clean(config.ExpandHome(path))
	if cleanPath == cleanHome {
		return "~"
	}
	if strings.HasPrefix(cleanPath, cleanHome+string(filepath.Separator)) {
		return "~" + strings.TrimPrefix(cleanPath, cleanHome)
	}
	return path
}
