package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"gpt-pro-monitor/internal/model"
)

// iqGuard 统一处理智商检测路由的前置检查：登录、功能开关；写操作再加同源校验。
// 写操作只有两个：立即检测（会真的调上游）和改测试模型/暂停。
func (o Options) iqGuard(w http.ResponseWriter, r *http.Request, write bool) bool {
	if write && !sameOrigin(r, o.AllowedOrigin) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "请求来源无效"})
		return false
	}
	if _, ok := o.Auth.Authenticate(r); !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "未登录"})
		return false
	}
	if o.IQ == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "智商检测未启用"})
		return false
	}
	return true
}

func (o Options) writeIQError(w http.ResponseWriter, err error, action string) {
	var iqErr *model.IQError
	if errors.As(err, &iqErr) {
		writeJSON(w, iqErr.Status, map[string]string{"error": iqErr.Message})
		return
	}
	o.Logger.Error("iq "+action, "error", err)
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "暂时无法读取智商检测数据"})
}

func (o Options) handleIQOverview(w http.ResponseWriter, r *http.Request) {
	if !o.iqGuard(w, r, false) {
		return
	}
	overview, err := o.IQ.Overview(r.Context())
	if err != nil {
		o.writeIQError(w, err, "overview")
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

func (o Options) handleIQBoard(w http.ResponseWriter, r *http.Request) {
	if !o.iqGuard(w, r, false) {
		return
	}
	query := r.URL.Query()
	view, err := o.IQ.Board(r.Context(), query.Get("window"), query.Get("scope"))
	if err != nil {
		o.writeIQError(w, err, "board")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (o Options) handleIQCard(w http.ResponseWriter, r *http.Request) {
	if !o.iqGuard(w, r, false) {
		return
	}
	query := r.URL.Query()
	view, err := o.IQ.Card(r.Context(), query.Get("source"), strings.TrimSpace(query.Get("model")), query.Get("scope"))
	if err != nil {
		o.writeIQError(w, err, "card")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (o Options) handleIQGroups(w http.ResponseWriter, r *http.Request) {
	if !o.iqGuard(w, r, false) {
		return
	}
	view, err := o.IQ.Groups(r.Context(), r.URL.Query().Get("window"))
	if err != nil {
		o.writeIQError(w, err, "groups")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// positiveID 解析可选的正整数参数；空串返回 0。
func positiveID(raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	return id, err == nil && id > 0
}

func (o Options) handleIQAccounts(w http.ResponseWriter, r *http.Request) {
	if !o.iqGuard(w, r, false) {
		return
	}
	groupID, ok := positiveID(r.URL.Query().Get("group_id"))
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "group_id 无效"})
		return
	}
	view, err := o.IQ.Accounts(r.Context(), r.URL.Query().Get("window"), groupID)
	if err != nil {
		o.writeIQError(w, err, "accounts")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (o Options) handleIQProbes(w http.ResponseWriter, r *http.Request) {
	if !o.iqGuard(w, r, false) {
		return
	}
	params := r.URL.Query()
	query := model.IQProbeQuery{
		Source: strings.TrimSpace(params.Get("source")), Model: strings.TrimSpace(params.Get("model")),
		Outcome: strings.TrimSpace(params.Get("outcome")), Scope: strings.TrimSpace(params.Get("scope")),
	}
	// 兼容旧参数：provider_id=N 等于 source=provider:N。
	if raw := strings.TrimSpace(params.Get("provider_id")); raw != "" && query.Source == "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider_id 无效"})
			return
		}
		if id > 0 {
			query.Source = "provider:" + strconv.FormatInt(id, 10)
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("account_id")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account_id 无效"})
			return
		}
		query.AccountID = id
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit 无效"})
			return
		}
		query.Limit = limit
	}
	view, err := o.IQ.Probes(r.Context(), query)
	if err != nil {
		o.writeIQError(w, err, "probes")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (o Options) handleIQRun(w http.ResponseWriter, r *http.Request) {
	if !o.iqGuard(w, r, true) {
		return
	}
	var request model.IQRunRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式无效"})
			return
		}
	}
	if request.AccountID < 0 || request.GroupID < 0 || (request.AccountID > 0 && request.GroupID > 0) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account_id / group_id 无效"})
		return
	}
	response, err := o.IQ.RunNow(r.Context(), request)
	if err != nil {
		o.writeIQError(w, err, "run")
		return
	}
	writeJSON(w, http.StatusAccepted, response)
}

func (o Options) handleIQSettings(w http.ResponseWriter, r *http.Request) {
	if !o.iqGuard(w, r, false) {
		return
	}
	view, err := o.IQ.Settings(r.Context())
	if err != nil {
		o.writeIQError(w, err, "settings")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (o Options) handleIQUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if !o.iqGuard(w, r, true) {
		return
	}
	var request model.IQSettingsRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式无效"})
		return
	}
	view, err := o.IQ.UpdateSettings(r.Context(), request)
	if err != nil {
		o.writeIQError(w, err, "update settings")
		return
	}
	writeJSON(w, http.StatusOK, view)
}
