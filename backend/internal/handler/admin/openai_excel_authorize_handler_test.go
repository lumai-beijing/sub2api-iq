package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type excelManualAuthorizeHandlerAdminStub struct {
	service.AdminService

	beginSessionID string
	beginAuthURL   string
	beginErr       error
	beginCalls     int

	completeErr        error
	completeCalls      int
	completedAccountID int64
	completedSessionID string
	completedCallback  string
}

func (s *excelManualAuthorizeHandlerAdminStub) BeginExcelManualAuthorize(_ context.Context, accountID int64) (string, string, error) {
	s.beginCalls++
	if accountID != 42 {
		return "", "", service.ErrAccountNotFound
	}
	return s.beginSessionID, s.beginAuthURL, s.beginErr
}

func (s *excelManualAuthorizeHandlerAdminStub) CompleteExcelManualAuthorize(_ context.Context, accountID int64, sessionID, callbackURL string) error {
	s.completeCalls++
	s.completedAccountID = accountID
	s.completedSessionID = sessionID
	s.completedCallback = callbackURL
	return s.completeErr
}

func newExcelManualAuthorizeHandlerRouter(h *AccountHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/:id/openai-excel/authorize", h.BeginExcelManualAuthorize)
	router.POST("/:id/openai-excel/callback", h.CompleteExcelManualAuthorize)
	return router
}

func TestExcelAuthorizeHandlerAuthorizeReturnsAuthURL(t *testing.T) {
	stub := &excelManualAuthorizeHandlerAdminStub{beginSessionID: "session-1", beginAuthURL: "https://auth.openai.com/oauth/authorize?client_id=excel"}
	router := newExcelManualAuthorizeHandlerRouter(&AccountHandler{adminService: stub})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/42/openai-excel/authorize", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Data struct {
			SessionID string `json:"session_id"`
			AuthURL   string `json:"auth_url"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "session-1", body.Data.SessionID)
	require.Equal(t, stub.beginAuthURL, body.Data.AuthURL)
	require.Equal(t, 1, stub.beginCalls)
}

func TestExcelAuthorizeHandlerCallbackReturnsAuthorized(t *testing.T) {
	stub := &excelManualAuthorizeHandlerAdminStub{}
	router := newExcelManualAuthorizeHandlerRouter(&AccountHandler{adminService: stub})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/42/openai-excel/callback",
		strings.NewReader(`{"session_id":"session-1","callback_url":"https://bps.openai.com/basispoints/api/office/auth/callback?code=code-1&state=state-1"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Data struct {
			Authorized bool `json:"authorized"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.True(t, body.Data.Authorized)
	require.Equal(t, 1, stub.completeCalls)
	require.Equal(t, int64(42), stub.completedAccountID)
	require.Equal(t, "session-1", stub.completedSessionID)
	require.Contains(t, stub.completedCallback, "code=code-1")
}

func TestExcelAuthorizeHandlerRejectsMissingParameters(t *testing.T) {
	router := newExcelManualAuthorizeHandlerRouter(&AccountHandler{adminService: &excelManualAuthorizeHandlerAdminStub{}})
	for _, body := range []string{
		`{}`,
		`{"session_id":"session-1"}`,
		`{"callback_url":"https://bps.openai.com/x?code=c&state=s"}`,
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/42/openai-excel/callback", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusBadRequest, w.Code, body)
	}
}

func TestExcelAuthorizeHandlerUnavailableWithoutConcreteService(t *testing.T) {
	router := newExcelManualAuthorizeHandlerRouter(&AccountHandler{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/42/openai-excel/authorize", nil))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)

	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/42/openai-excel/callback", strings.NewReader(`{"session_id":"s","callback_url":"https://bps.openai.com/x?code=c&state=s"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}
