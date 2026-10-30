package httpapi

import (
	"net/http"
	"strconv"

	"gpt-pro-monitor/internal/model"
)

// 第三方供应商与对比。写操作（增删改、测试连接、立即检测）都会用到 API Key 或真的调上游，一律要求同源。

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "供应商 ID 无效"})
		return 0, false
	}
	return id, true
}

func (o Options) handleIQProviders(w http.ResponseWriter, r *http.Request) {
	if !o.iqGuard(w, r, false) {
		return
	}
	view, err := o.IQ.Providers(r.Context())
	if err != nil {
		o.writeIQError(w, err, "providers")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (o Options) handleIQCreateProvider(w http.ResponseWriter, r *http.Request) {
	if !o.iqGuard(w, r, true) {
		return
	}
	var input model.IQProviderInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式无效"})
		return
	}
	view, err := o.IQ.CreateProvider(r.Context(), input)
	if err != nil {
		o.writeIQError(w, err, "create provider")
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (o Options) handleIQUpdateProvider(w http.ResponseWriter, r *http.Request) {
	if !o.iqGuard(w, r, true) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var input model.IQProviderInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式无效"})
		return
	}
	view, err := o.IQ.UpdateProvider(r.Context(), id, input)
	if err != nil {
		o.writeIQError(w, err, "update provider")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (o Options) handleIQDeleteProvider(w http.ResponseWriter, r *http.Request) {
	if !o.iqGuard(w, r, true) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	keep := r.URL.Query().Get("keep_probes") == "true"
	if err := o.IQ.DeleteProvider(r.Context(), id, keep); err != nil {
		o.writeIQError(w, err, "delete provider")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (o Options) handleIQTestProvider(w http.ResponseWriter, r *http.Request) {
	if !o.iqGuard(w, r, true) {
		return
	}
	var request model.IQProviderTestRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式无效"})
		return
	}
	result, err := o.IQ.TestProvider(r.Context(), request)
	if err != nil {
		o.writeIQError(w, err, "test provider")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (o Options) handleIQRunProvider(w http.ResponseWriter, r *http.Request) {
	if !o.iqGuard(w, r, true) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var request struct {
		Model string `json:"model"`
	}
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式无效"})
			return
		}
	}
	response, err := o.IQ.RunProvider(r.Context(), id, request.Model)
	if err != nil {
		o.writeIQError(w, err, "run provider")
		return
	}
	writeJSON(w, http.StatusAccepted, response)
}
