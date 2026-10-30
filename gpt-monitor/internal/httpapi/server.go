package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gpt-pro-monitor/internal/auth"
	"gpt-pro-monitor/internal/model"
)

type MonitorService interface {
	Snapshot(ctx context.Context) (model.Snapshot, error)
}

// CodexService 是 292 打票看板的数据源。为 nil 时相关路由直接返回 404。
type CodexService interface {
	Overview(ctx context.Context) (model.CodexOverview, error)
	DailyTrend(ctx context.Context) (model.CodexDailyTrend, error)
}

// IQService 是智商检测。为 nil 时相关路由直接返回 404。
type IQService interface {
	Overview(ctx context.Context) (model.IQOverview, error)
	Board(ctx context.Context, window, scope string) (model.IQBoard, error)
	Card(ctx context.Context, source, model, scope string) (model.IQCardView, error)
	Accounts(ctx context.Context, window string, groupID int64) (model.IQAccountsView, error)
	Groups(ctx context.Context, window string) (model.IQGroupsView, error)
	Probes(ctx context.Context, query model.IQProbeQuery) (model.IQProbesView, error)
	RunNow(ctx context.Context, request model.IQRunRequest) (model.IQRunResponse, error)
	Settings(ctx context.Context) (model.IQSettingsView, error)
	UpdateSettings(ctx context.Context, request model.IQSettingsRequest) (model.IQSettingsView, error)
	Providers(ctx context.Context) (model.IQProvidersView, error)
	CreateProvider(ctx context.Context, input model.IQProviderInput) (model.IQProviderView, error)
	UpdateProvider(ctx context.Context, id int64, input model.IQProviderInput) (model.IQProviderView, error)
	DeleteProvider(ctx context.Context, id int64, keepProbes bool) error
	TestProvider(ctx context.Context, request model.IQProviderTestRequest) (model.IQProviderTestResult, error)
	RunProvider(ctx context.Context, id int64, model string) (model.IQRunResponse, error)
}

type HealthChecker interface {
	Ping(ctx context.Context) error
}

type Options struct {
	Monitor       MonitorService
	Codex         CodexService
	IQ            IQService
	Health        HealthChecker
	Auth          *auth.Manager
	WebFS         fs.FS
	Logger        *slog.Logger
	AllowedOrigin string
}

func NewHandler(options Options) http.Handler {
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", options.handleLogin)
	mux.HandleFunc("POST /api/logout", options.handleLogout)
	mux.HandleFunc("GET /api/session", options.handleSession)
	mux.HandleFunc("GET /api/v1/gpt-pro/accounts", options.handleAccounts)
	mux.HandleFunc("GET /api/v1/codex-ticket/overview", options.handleCodexOverview)
	mux.HandleFunc("GET /api/v1/codex-ticket/daily-trend", options.handleCodexDailyTrend)
	mux.HandleFunc("GET /api/v1/iq/overview", options.handleIQOverview)
	mux.HandleFunc("GET /api/v1/iq/board", options.handleIQBoard)
	mux.HandleFunc("GET /api/v1/iq/card", options.handleIQCard)
	mux.HandleFunc("GET /api/v1/iq/groups", options.handleIQGroups)
	mux.HandleFunc("GET /api/v1/iq/accounts", options.handleIQAccounts)
	mux.HandleFunc("GET /api/v1/iq/probes", options.handleIQProbes)
	mux.HandleFunc("POST /api/v1/iq/run", options.handleIQRun)
	mux.HandleFunc("GET /api/v1/iq/settings", options.handleIQSettings)
	mux.HandleFunc("PUT /api/v1/iq/settings", options.handleIQUpdateSettings)
	mux.HandleFunc("GET /api/v1/iq/providers", options.handleIQProviders)
	mux.HandleFunc("POST /api/v1/iq/providers", options.handleIQCreateProvider)
	mux.HandleFunc("POST /api/v1/iq/providers/test", options.handleIQTestProvider)
	mux.HandleFunc("PUT /api/v1/iq/providers/{id}", options.handleIQUpdateProvider)
	mux.HandleFunc("DELETE /api/v1/iq/providers/{id}", options.handleIQDeleteProvider)
	mux.HandleFunc("POST /api/v1/iq/providers/{id}/run", options.handleIQRunProvider)
	mux.HandleFunc("GET /api/v1/features", options.handleFeatures)
	mux.HandleFunc("GET /healthz", options.handleHealth)
	mux.Handle("GET /", spaHandler{fsys: options.WebFS})
	return securityHeaders(mux)
}

func (o Options) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r, o.AllowedOrigin) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "请求来源无效"})
		return
	}
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式无效"})
		return
	}
	session, cookie, err := o.Auth.Login(r, request.Username, request.Password)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrRateLimited):
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "登录尝试过多，请稍后再试"})
		case errors.Is(err, auth.ErrInvalidCredentials):
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "用户名或密码错误"})
		default:
			o.Logger.Error("login failed", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "登录失败"})
		}
		return
	}
	http.SetCookie(w, cookie)
	writeJSON(w, http.StatusOK, session)
}

func (o Options) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r, o.AllowedOrigin) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "请求来源无效"})
		return
	}
	o.Auth.Logout(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (o Options) handleSession(w http.ResponseWriter, r *http.Request) {
	session, ok := o.Auth.Authenticate(r)
	if !ok {
		writeJSON(w, http.StatusOK, auth.Session{Authenticated: false})
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (o Options) handleAccounts(w http.ResponseWriter, r *http.Request) {
	if _, ok := o.Auth.Authenticate(r); !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "未登录"})
		return
	}
	snapshot, err := o.Monitor.Snapshot(r.Context())
	if err != nil {
		o.Logger.Error("load monitor snapshot", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "暂时无法读取账号快照"})
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (o Options) handleCodexOverview(w http.ResponseWriter, r *http.Request) {
	if _, ok := o.Auth.Authenticate(r); !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "未登录"})
		return
	}
	if o.Codex == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "打票看板未启用"})
		return
	}
	overview, err := o.Codex.Overview(r.Context())
	if err != nil {
		o.Logger.Error("load codex overview", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "暂时无法读取打票看板"})
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

func (o Options) handleCodexDailyTrend(w http.ResponseWriter, r *http.Request) {
	if _, ok := o.Auth.Authenticate(r); !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "未登录"})
		return
	}
	if o.Codex == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "打票看板未启用"})
		return
	}
	trend, err := o.Codex.DailyTrend(r.Context())
	if err != nil {
		o.Logger.Error("load codex daily trend", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "暂时无法读取按天趋势"})
		return
	}
	writeJSON(w, http.StatusOK, trend)
}

func (o Options) handleFeatures(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"codex_ticket": o.Codex != nil, "iq": o.IQ != nil})
}

func (o Options) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := o.Health.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status":   "degraded",
			"database": "unavailable",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"database": "ok",
	})
}

type spaHandler struct {
	fsys fs.FS
}

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if strings.HasPrefix(path, "api/") || path == "healthz" {
		http.NotFound(w, r)
		return
	}
	if file, err := h.fsys.Open(path); err == nil {
		info, statErr := file.Stat()
		_ = file.Close()
		if statErr == nil && !info.IsDir() {
			http.FileServer(http.FS(h.fsys)).ServeHTTP(w, r)
			return
		}
	}
	index, err := fs.ReadFile(h.fsys, "index.html")
	if err != nil {
		http.Error(w, "frontend unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(index)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func sameOrigin(r *http.Request, allowedOrigin string) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	if allowedOrigin != "" {
		parsed, err := url.Parse(origin)
		return err == nil && strings.EqualFold(parsed.Scheme+"://"+parsed.Host, strings.TrimRight(allowedOrigin, "/"))
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; script-src 'self'")
		next.ServeHTTP(w, r)
	})
}
