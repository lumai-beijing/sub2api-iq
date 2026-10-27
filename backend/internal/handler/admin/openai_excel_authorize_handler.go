package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// excelManualAuthorizeAdminService is the small surface implemented by the
// concrete admin service. The AdminService interface is intentionally left
// unchanged so existing stubs keep compiling; a missing implementation is
// surfaced as 503 instead of breaking route registration.
type excelManualAuthorizeAdminService interface {
	BeginExcelManualAuthorize(ctx context.Context, accountID int64) (string, string, error)
	CompleteExcelManualAuthorize(ctx context.Context, accountID int64, sessionID, callbackURL string) error
}

func (h *AccountHandler) excelManualAuthorizeService(c *gin.Context) (excelManualAuthorizeAdminService, bool) {
	svc, ok := h.adminService.(excelManualAuthorizeAdminService)
	if !ok || svc == nil {
		response.Error(c, http.StatusServiceUnavailable, "Excel manual authorization unavailable")
		return nil, false
	}
	return svc, true
}

func parseExcelManualAuthorizeAccountID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return 0, false
	}
	return id, true
}

// BeginExcelManualAuthorize handles POST /api/v1/admin/accounts/:id/openai-excel/authorize
func (h *AccountHandler) BeginExcelManualAuthorize(c *gin.Context) {
	accountID, ok := parseExcelManualAuthorizeAccountID(c)
	if !ok {
		return
	}
	svc, ok := h.excelManualAuthorizeService(c)
	if !ok {
		return
	}
	sessionID, authURL, err := svc.BeginExcelManualAuthorize(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"session_id": sessionID, "auth_url": authURL})
}

// CompleteExcelManualAuthorize handles POST /api/v1/admin/accounts/:id/openai-excel/callback
func (h *AccountHandler) CompleteExcelManualAuthorize(c *gin.Context) {
	accountID, ok := parseExcelManualAuthorizeAccountID(c)
	if !ok {
		return
	}
	var req struct {
		SessionID   string `json:"session_id" binding:"required"`
		CallbackURL string `json:"callback_url" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Session and callback URL are required")
		return
	}
	svc, ok := h.excelManualAuthorizeService(c)
	if !ok {
		return
	}
	if err := svc.CompleteExcelManualAuthorize(c.Request.Context(), accountID, req.SessionID, req.CallbackURL); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"authorized": true})
}
