package httpapi

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"golang.org/x/crypto/bcrypt"

	"gpt-pro-monitor/internal/auth"
	"gpt-pro-monitor/internal/model"
)

type monitorStub struct{}

func (monitorStub) Snapshot(context.Context) (model.Snapshot, error) {
	return model.Snapshot{
		GeneratedAt: time.Unix(100, 0).UTC(),
		CachedAt:    time.Unix(100, 0).UTC(),
		Summary:     model.Summary{Total: 1},
		Accounts:    []model.Account{{ID: 1, Name: "Pro 1", ExpiryLevel: "critical"}},
	}, nil
}

type healthStub struct{ err error }

func (h healthStub) Ping(context.Context) error { return h.err }

func testHandler(t *testing.T) http.Handler {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := auth.NewManager(auth.Config{
		Username:      "admin",
		PasswordHash:  string(hash),
		SessionSecret: "01234567890123456789012345678901",
		SessionTTL:    time.Hour,
		MaxAttempts:   5,
		AttemptWindow: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	web := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>ok</html>"), Mode: 0o644},
	}
	var webFS fs.FS = web
	return NewHandler(Options{
		Monitor: monitorStub{},
		Health:  healthStub{},
		Auth:    manager,
		WebFS:   webFS,
		Logger:  slog.New(slog.NewTextHandler(&strings.Builder{}, nil)),
	})
}

func TestAPIRequiresAuthentication(t *testing.T) {
	handler := testHandler(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "https://monitor.test/api/v1/gpt-pro/accounts", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

func TestLoginAndReadSnapshot(t *testing.T) {
	handler := testHandler(t)
	loginRecorder := httptest.NewRecorder()
	loginRequest := httptest.NewRequest(http.MethodPost, "https://monitor.test/api/login", strings.NewReader(`{"username":"admin","password":"correct-password"}`))
	loginRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(loginRecorder, loginRequest)
	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", loginRecorder.Code, loginRecorder.Body.String())
	}
	cookies := loginRecorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %v", cookies)
	}

	dataRecorder := httptest.NewRecorder()
	dataRequest := httptest.NewRequest(http.MethodGet, "https://monitor.test/api/v1/gpt-pro/accounts", nil)
	dataRequest.AddCookie(cookies[0])
	handler.ServeHTTP(dataRecorder, dataRequest)
	if dataRecorder.Code != http.StatusOK {
		t.Fatalf("data status = %d, body=%s", dataRecorder.Code, dataRecorder.Body.String())
	}
	var snapshot model.Snapshot
	if err := json.Unmarshal(dataRecorder.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Summary.Total != 1 || len(snapshot.Accounts) != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestHealthAndSPA(t *testing.T) {
	handler := testHandler(t)
	healthRecorder := httptest.NewRecorder()
	handler.ServeHTTP(healthRecorder, httptest.NewRequest(http.MethodGet, "https://monitor.test/healthz", nil))
	if healthRecorder.Code != http.StatusOK {
		t.Fatalf("health status = %d", healthRecorder.Code)
	}

	spaRecorder := httptest.NewRecorder()
	handler.ServeHTTP(spaRecorder, httptest.NewRequest(http.MethodGet, "https://monitor.test/some/client/route", nil))
	if spaRecorder.Code != http.StatusOK || !strings.Contains(spaRecorder.Body.String(), "ok") {
		t.Fatalf("spa response = %d %q", spaRecorder.Code, spaRecorder.Body.String())
	}
}
