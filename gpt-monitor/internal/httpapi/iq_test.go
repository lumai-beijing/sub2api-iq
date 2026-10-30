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

type iqStub struct {
	boards       [][2]string
	cards        [][3]string
	accounts     []int64
	probeQueries []model.IQProbeQuery
	runs         []model.IQRunRequest
	settings     []model.IQSettingsRequest
	runErr       error
	created      []model.IQProviderInput
	updated      []int64
	deleted      [][2]any
	providerRuns [][2]any
}

func (s *iqStub) Overview(context.Context) (model.IQOverview, error) {
	return model.IQOverview{Level: "healthy", LevelLabel: "智商正常"}, nil
}
func (s *iqStub) Board(_ context.Context, window, scope string) (model.IQBoard, error) {
	s.boards = append(s.boards, [2]string{window, scope})
	return model.IQBoard{Window: window, Scope: scope}, nil
}
func (s *iqStub) Card(_ context.Context, source, m, scope string) (model.IQCardView, error) {
	s.cards = append(s.cards, [3]string{source, m, scope})
	return model.IQCardView{SourceKey: source}, nil
}
func (s *iqStub) Groups(_ context.Context, window string) (model.IQGroupsView, error) {
	return model.IQGroupsView{Window: window}, nil
}
func (s *iqStub) Accounts(_ context.Context, window string, groupID int64) (model.IQAccountsView, error) {
	if window == "bad" {
		return model.IQAccountsView{}, &model.IQError{Status: http.StatusBadRequest, Message: "window 只能是 24h / 7d / 30d / all"}
	}
	s.accounts = append(s.accounts, groupID)
	return model.IQAccountsView{Window: window, GroupID: groupID}, nil
}
func (s *iqStub) Probes(_ context.Context, q model.IQProbeQuery) (model.IQProbesView, error) {
	s.probeQueries = append(s.probeQueries, q)
	return model.IQProbesView{Probes: []model.IQProbeView{{AccountID: q.AccountID, Model: q.Model}}}, nil
}
func (s *iqStub) RunNow(_ context.Context, r model.IQRunRequest) (model.IQRunResponse, error) {
	s.runs = append(s.runs, r)
	if s.runErr != nil {
		return model.IQRunResponse{}, s.runErr
	}
	return model.IQRunResponse{RunID: "r1", Kind: "round", Targets: 2}, nil
}
func (s *iqStub) Settings(context.Context) (model.IQSettingsView, error) {
	return model.IQSettingsView{ModelsSource: "default"}, nil
}
func (s *iqStub) UpdateSettings(_ context.Context, r model.IQSettingsRequest) (model.IQSettingsView, error) {
	s.settings = append(s.settings, r)
	return model.IQSettingsView{ModelsSource: "settings"}, nil
}
func (s *iqStub) Providers(context.Context) (model.IQProvidersView, error) {
	return model.IQProvidersView{Providers: []model.IQProviderView{{ID: 1, Name: "p", APIKeyHint: "••••abcd"}}}, nil
}
func (s *iqStub) CreateProvider(_ context.Context, in model.IQProviderInput) (model.IQProviderView, error) {
	s.created = append(s.created, in)
	return model.IQProviderView{ID: 2, Name: in.Name}, nil
}
func (s *iqStub) UpdateProvider(_ context.Context, id int64, in model.IQProviderInput) (model.IQProviderView, error) {
	s.updated = append(s.updated, id)
	return model.IQProviderView{ID: id, Name: in.Name}, nil
}
func (s *iqStub) DeleteProvider(_ context.Context, id int64, keep bool) error {
	s.deleted = append(s.deleted, [2]any{id, keep})
	return nil
}
func (s *iqStub) TestProvider(context.Context, model.IQProviderTestRequest) (model.IQProviderTestResult, error) {
	return model.IQProviderTestResult{OK: true}, nil
}
func (s *iqStub) RunProvider(_ context.Context, id int64, m string) (model.IQRunResponse, error) {
	s.providerRuns = append(s.providerRuns, [2]any{id, m})
	return model.IQRunResponse{Kind: "provider", Targets: 2}, nil
}

func iqTestHandler(t *testing.T, iq IQService) (http.Handler, *http.Cookie) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := auth.NewManager(auth.Config{
		Username: "admin", PasswordHash: string(hash), SessionSecret: "01234567890123456789012345678901",
		SessionTTL: time.Hour, MaxAttempts: 5, AttemptWindow: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	var webFS fs.FS = fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>ok</html>")}}
	handler := NewHandler(Options{
		Monitor: monitorStub{}, Health: healthStub{}, Auth: manager, WebFS: webFS, IQ: iq,
		Logger: slog.New(slog.NewTextHandler(&strings.Builder{}, nil)),
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "https://monitor.test/api/login", strings.NewReader(`{"username":"admin","password":"correct-password"}`))
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("login failed: %d", recorder.Code)
	}
	return handler, recorder.Result().Cookies()[0]
}

func do(handler http.Handler, method, path, body string, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	request := httptest.NewRequest(method, "https://monitor.test"+path, reader)
	if body == "" {
		request.ContentLength = 0
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestIQProviderRoutes(t *testing.T) {
	stub := &iqStub{}
	handler, cookie := iqTestHandler(t, stub)
	body := `{"name":"中转","base_url":"https://x.example.com/v1","api_format":"chat_completions","api_key":"sk-1","models":"gpt-6-astra","enabled":true,"interval_seconds":900,"jitter_seconds":60,"samples_per_model":1}`
	if code := do(handler, http.MethodPost, "/api/v1/iq/providers", body, cookie, "").Code; code != http.StatusCreated {
		t.Fatalf("新增应 201，got %d", code)
	}
	if len(stub.created) != 1 || stub.created[0].IntervalSeconds != 900 || stub.created[0].APIKey != "sk-1" {
		t.Fatalf("新增参数没透传（内嵌的 schedule 字段要平铺解析）: %+v", stub.created)
	}
	if code := do(handler, http.MethodPost, "/api/v1/iq/providers", `{"name":"x","evil":1}`, cookie, "").Code; code != http.StatusBadRequest {
		t.Fatalf("未知字段应 400，got %d", code)
	}
	if code := do(handler, http.MethodPut, "/api/v1/iq/providers/7", body, cookie, "").Code; code != http.StatusOK || stub.updated[0] != 7 {
		t.Fatalf("编辑应 200 并带上 id，got %d %v", code, stub.updated)
	}
	if code := do(handler, http.MethodPut, "/api/v1/iq/providers/abc", body, cookie, "").Code; code != http.StatusBadRequest {
		t.Fatalf("非法 id 应 400，got %d", code)
	}
	if code := do(handler, http.MethodDelete, "/api/v1/iq/providers/7?keep_probes=true", "", cookie, "").Code; code != http.StatusNoContent ||
		stub.deleted[0][0] != int64(7) || stub.deleted[0][1] != true {
		t.Fatalf("删除应 204 并透传 keep_probes，got %d %v", code, stub.deleted)
	}
	if code := do(handler, http.MethodPost, "/api/v1/iq/providers/7/run", `{"model":"gpt-6-astra@xhigh"}`, cookie, "").Code; code != http.StatusAccepted ||
		stub.providerRuns[0][1] != "gpt-6-astra@xhigh" {
		t.Fatalf("立即检测应 202，got %d %v", code, stub.providerRuns)
	}
	if code := do(handler, http.MethodPost, "/api/v1/iq/providers/test", `{"base_url":"https://x.example.com/v1","api_key":"k"}`, cookie, "").Code; code != http.StatusOK {
		t.Fatalf("测试连接应 200，got %d", code)
	}
	for _, route := range [][3]string{
		{http.MethodPost, "/api/v1/iq/providers", body},
		{http.MethodPut, "/api/v1/iq/providers/7", body},
		{http.MethodDelete, "/api/v1/iq/providers/7", ""},
		{http.MethodPost, "/api/v1/iq/providers/test", `{}`},
		{http.MethodPost, "/api/v1/iq/providers/7/run", ""},
	} {
		if code := do(handler, route[0], route[1], route[2], cookie, "https://evil.example").Code; code != http.StatusForbidden {
			t.Errorf("跨站 %s %s 应 403，got %d", route[0], route[1], code)
		}
	}
	if code := do(handler, http.MethodGet, "/api/v1/iq/probes?provider_id=-1", "", cookie, "").Code; code != http.StatusBadRequest {
		t.Fatalf("非法 provider_id 应 400，got %d", code)
	}
	// 旧参数 provider_id=N 等于 source=provider:N
	do(handler, http.MethodGet, "/api/v1/iq/probes?provider_id=3", "", cookie, "")
	if q := stub.probeQueries[len(stub.probeQueries)-1]; q.Source != "provider:3" {
		t.Fatalf("provider_id 应翻成 source: %+v", q)
	}
}

func TestIQBoardCardGroupRoutes(t *testing.T) {
	stub := &iqStub{}
	handler, cookie := iqTestHandler(t, stub)
	if code := do(handler, http.MethodGet, "/api/v1/iq/board?window=7d&scope=random", "", cookie, "").Code; code != http.StatusOK ||
		stub.boards[0] != [2]string{"7d", "random"} {
		t.Fatalf("矩阵应 200 并透传口径，got %d %v", code, stub.boards)
	}
	if code := do(handler, http.MethodGet, "/api/v1/iq/card?source=group:6&model=gpt-6-astra%40medium&scope=all", "", cookie, "").Code; code != http.StatusOK ||
		stub.cards[0] != [3]string{"group:6", "gpt-6-astra@medium", "all"} {
		t.Fatalf("单格详情应 200 并透传参数，got %d %v", code, stub.cards)
	}
	if code := do(handler, http.MethodGet, "/api/v1/iq/groups?window=24h", "", cookie, "").Code; code != http.StatusOK {
		t.Fatalf("分组列表应 200，got %d", code)
	}
	if code := do(handler, http.MethodGet, "/api/v1/iq/accounts?window=7d&group_id=8", "", cookie, "").Code; code != http.StatusOK || stub.accounts[0] != 8 {
		t.Fatalf("账号排行应透传 group_id，got %d %v", code, stub.accounts)
	}
	if code := do(handler, http.MethodGet, "/api/v1/iq/accounts?group_id=abc", "", cookie, "").Code; code != http.StatusBadRequest {
		t.Fatalf("非法 group_id 应 400，got %d", code)
	}
	do(handler, http.MethodGet, "/api/v1/iq/probes?source=group:8&outcome=wrong&scope=random", "", cookie, "")
	if q := stub.probeQueries[0]; q.Source != "group:8" || q.Outcome != "wrong" || q.Scope != "random" {
		t.Fatalf("采样详情参数没透传: %+v", q)
	}
	if code := do(handler, http.MethodPost, "/api/v1/iq/run", `{"group_id":8}`, cookie, "").Code; code != http.StatusAccepted || stub.runs[0].GroupID != 8 {
		t.Fatalf("分组立即检测应 202，got %d %+v", code, stub.runs)
	}
	if code := do(handler, http.MethodPost, "/api/v1/iq/run", `{"group_id":8,"account_id":3}`, cookie, "").Code; code != http.StatusBadRequest {
		t.Fatalf("同时指定号和分组应 400，got %d", code)
	}
	if code := do(handler, http.MethodPut, "/api/v1/iq/settings", `{"groups":{"ids":[8,6],"per_round":3}}`, cookie, "").Code; code != http.StatusOK {
		t.Fatalf("改分组配置应 200，got %d", code)
	}
	if g := stub.settings[0].Groups; g == nil || g.IDs == nil || len(*g.IDs) != 2 || *g.PerRound != 3 {
		t.Fatalf("分组配置没透传: %+v", g)
	}
}

func TestIQRoutesRequireLogin(t *testing.T) {
	handler, _ := iqTestHandler(t, &iqStub{})
	for _, route := range [][2]string{
		{http.MethodGet, "/api/v1/iq/overview"}, {http.MethodGet, "/api/v1/iq/accounts"}, {http.MethodGet, "/api/v1/iq/probes"},
		{http.MethodPost, "/api/v1/iq/run"}, {http.MethodGet, "/api/v1/iq/settings"}, {http.MethodPut, "/api/v1/iq/settings"},
		{http.MethodGet, "/api/v1/iq/board"}, {http.MethodGet, "/api/v1/iq/card"}, {http.MethodGet, "/api/v1/iq/groups"},
		{http.MethodGet, "/api/v1/iq/providers"}, {http.MethodPost, "/api/v1/iq/providers"},
		{http.MethodPut, "/api/v1/iq/providers/1"}, {http.MethodDelete, "/api/v1/iq/providers/1"},
		{http.MethodPost, "/api/v1/iq/providers/test"}, {http.MethodPost, "/api/v1/iq/providers/1/run"},
	} {
		if code := do(handler, route[0], route[1], "", nil, "").Code; code != http.StatusUnauthorized {
			t.Errorf("%s %s 未登录应 401，got %d", route[0], route[1], code)
		}
	}
}

func TestIQDisabledReturns404AndFeatureFlag(t *testing.T) {
	handler, cookie := iqTestHandler(t, nil)
	if code := do(handler, http.MethodGet, "/api/v1/iq/overview", "", cookie, "").Code; code != http.StatusNotFound {
		t.Fatalf("未启用应 404，got %d", code)
	}
	var features map[string]bool
	_ = json.Unmarshal(do(handler, http.MethodGet, "/api/v1/features", "", nil, "").Body.Bytes(), &features)
	if features["iq"] {
		t.Fatalf("未启用时 features.iq 应为 false: %v", features)
	}
}

func TestIQWritesRejectCrossOrigin(t *testing.T) {
	stub := &iqStub{}
	handler, cookie := iqTestHandler(t, stub)
	if code := do(handler, http.MethodPost, "/api/v1/iq/run", "", cookie, "https://evil.example").Code; code != http.StatusForbidden {
		t.Fatalf("跨站立即检测应 403，got %d", code)
	}
	if code := do(handler, http.MethodPut, "/api/v1/iq/settings", `{"paused":true}`, cookie, "https://evil.example").Code; code != http.StatusForbidden {
		t.Fatalf("跨站改设置应 403，got %d", code)
	}
	if len(stub.runs) != 0 || len(stub.settings) != 0 {
		t.Fatalf("被拒的请求不应到达服务层")
	}
}

func TestIQRunAndSettings(t *testing.T) {
	stub := &iqStub{}
	handler, cookie := iqTestHandler(t, stub)

	if recorder := do(handler, http.MethodPost, "/api/v1/iq/run", "", cookie, "https://monitor.test"); recorder.Code != http.StatusAccepted {
		t.Fatalf("空 body 立即检测应 202，got %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := do(handler, http.MethodPost, "/api/v1/iq/run", `{"account_id":7,"model":"gpt-6-astra@xhigh"}`, cookie, ""); recorder.Code != http.StatusAccepted {
		t.Fatalf("单号检测应 202，got %d", recorder.Code)
	}
	if stub.runs[1].AccountID != 7 || stub.runs[1].Model != "gpt-6-astra@xhigh" {
		t.Fatalf("参数没透传: %+v", stub.runs[1])
	}
	if code := do(handler, http.MethodPost, "/api/v1/iq/run", `{"account_id":7,"evil":1}`, cookie, "").Code; code != http.StatusBadRequest {
		t.Fatalf("未知字段应 400，got %d", code)
	}

	stub.runErr = &model.IQError{Status: http.StatusConflict, Message: "上一轮检测还没结束"}
	recorder := do(handler, http.MethodPost, "/api/v1/iq/run", "", cookie, "")
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "上一轮检测还没结束") {
		t.Fatalf("业务错误应原样带状态码和文案: %d %s", recorder.Code, recorder.Body.String())
	}

	if code := do(handler, http.MethodPut, "/api/v1/iq/settings", `{"models":"gpt-6-astra@xhigh","paused":false}`, cookie, "").Code; code != http.StatusOK {
		t.Fatalf("改设置应 200，got %d", code)
	}
	if stub.settings[0].Models == nil || *stub.settings[0].Models != "gpt-6-astra@xhigh" {
		t.Fatalf("设置没透传: %+v", stub.settings[0])
	}
	body := `{"schedule":{"enabled":false,"interval_seconds":600,"jitter_seconds":120,"sample_mode":"count","sample_count":8}}`
	if code := do(handler, http.MethodPut, "/api/v1/iq/settings", body, cookie, "").Code; code != http.StatusOK {
		t.Fatalf("改定时配置应 200，got %d", code)
	}
	schedule := stub.settings[1].Schedule
	if schedule == nil || schedule.Enabled == nil || *schedule.Enabled || *schedule.IntervalSeconds != 600 ||
		*schedule.JitterSeconds != 120 || *schedule.SampleMode != "count" || *schedule.SampleCount != 8 || schedule.SamplePercent != nil {
		t.Fatalf("定时配置没按字段透传（没传的字段应为 nil）: %+v", schedule)
	}
	if code := do(handler, http.MethodPut, "/api/v1/iq/settings", `{"schedule":{"interval":5}}`, cookie, "").Code; code != http.StatusBadRequest {
		t.Fatalf("定时配置里的未知字段应 400，got %d", code)
	}

	if recorder := do(handler, http.MethodGet, "/api/v1/iq/accounts?window=bad", "", cookie, ""); recorder.Code != http.StatusBadRequest {
		t.Fatalf("非法窗口应 400，got %d", recorder.Code)
	}
	if code := do(handler, http.MethodGet, "/api/v1/iq/probes?account_id=abc", "", cookie, "").Code; code != http.StatusBadRequest {
		t.Fatalf("非法 account_id 应 400，got %d", code)
	}
	var probes model.IQProbesView
	_ = json.Unmarshal(do(handler, http.MethodGet, "/api/v1/iq/probes?account_id=9&model=gpt-6-astra%40xhigh", "", cookie, "").Body.Bytes(), &probes)
	if len(probes.Probes) != 1 || probes.Probes[0].AccountID != 9 || probes.Probes[0].Model != "gpt-6-astra@xhigh" {
		t.Fatalf("查询参数没透传: %+v", probes)
	}
}
