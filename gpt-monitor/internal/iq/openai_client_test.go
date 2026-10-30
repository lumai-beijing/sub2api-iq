package iq

import (
	"context"
	"encoding/json"
	"io"
	mrand "math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gpt-pro-monitor/internal/model"
)

func TestNormalizeBaseURL(t *testing.T) {
	ok := map[string]string{
		"https://api.example.com/v1":                   "https://api.example.com/v1",
		"https://api.example.com/v1/":                  "https://api.example.com/v1",
		" https://api.example.com/v1/chat/completions": "https://api.example.com/v1",
		"https://api.example.com/v1/responses":         "https://api.example.com/v1",
		"https://api.example.com/openai/v1/models":     "https://api.example.com/openai/v1",
		"http://10.0.0.5:3000":                         "http://10.0.0.5:3000",
	}
	for in, want := range ok {
		got, err := NormalizeBaseURL(in)
		if err != nil || got != want {
			t.Errorf("NormalizeBaseURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "api.example.com/v1", "ftp://x.com", "https://user:pw@x.com/v1", "https://x.com/v1?key=1", "https://"} {
		if _, err := NormalizeBaseURL(bad); err == nil {
			t.Errorf("NormalizeBaseURL(%q) 应报错", bad)
		}
	}
}

func TestRejectPrivateAddress(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:443", "10.1.2.3:80", "172.18.0.4:8080", "192.168.0.1:443", "169.254.169.254:80",
		"100.64.1.1:443", "198.18.7.4:443", "[::1]:443", "[fd00::1]:443", "0.0.0.0:80", "[::ffff:127.0.0.1]:443"} {
		if err := rejectPrivateAddress("tcp", addr, nil); err == nil {
			t.Errorf("%s 应被拒绝", addr)
		}
	}
	for _, addr := range []string{"8.8.8.8:443", "43.106.107.147:443", "[2606:4700::1111]:443"} {
		if err := rejectPrivateAddress("tcp", addr, nil); err != nil {
			t.Errorf("%s 不应被拒绝: %v", addr, err)
		}
	}
}

// fakeOpenAI 模拟一个第三方 OpenAI 兼容接口。mode 决定怎么回。
type fakeOpenAI struct {
	mode     string // chat-stream | chat-json | responses-stream | responses-json | error | redirect
	lastBody map[string]any
	lastPath string
	lastAuth string
}

func (f *fakeOpenAI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.lastPath, f.lastAuth = r.URL.Path, r.Header.Get("Authorization")
	if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models") {
		if f.mode == "error" {
			http.Error(w, `{"error":{"message":"Invalid API key: sk-secret-123456789"}}`, http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"gpt-6-astra"},{"id":"gpt-5.6-sol"}]}`)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	f.lastBody = map[string]any{}
	_ = json.Unmarshal(raw, &f.lastBody)
	switch f.mode {
	case "error":
		http.Error(w, `{"error":{"message":"Invalid API key: sk-secret-123456789","type":"invalid_request_error"}}`, http.StatusUnauthorized)
	case "redirect":
		http.Redirect(w, r, "https://evil.example/steal", http.StatusFound)
	case "chat-json":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"gpt-6-astra-0901","choices":[{"message":{"role":"assistant","content":"{\"answer\": 21, \"reasoning\": \"最坏情况\"}"}}]}`)
	case "responses-json":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"gpt-6-astra","output":[{"type":"message","content":[{"type":"output_text","text":"{\"answer\": 29}"}]}]}`)
	case "responses-stream":
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range []string{
			`{"type":"response.created","response":{"model":"gpt-6-astra"}}`,
			`{"type":"response.output_text.delta","delta":"{\"answer\": "}`,
			`{"type":"response.output_text.delta","delta":"21}"}`,
			`{"type":"response.completed","response":{"model":"gpt-6-astra-final"}}`,
		} {
			_, _ = io.WriteString(w, "event: x\ndata: "+e+"\n\n")
		}
	default: // chat-stream
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		for _, e := range []string{
			`{"model":"gpt-5.6-sol","choices":[{"delta":{"role":"assistant"}}]}`,
			`{"model":"gpt-5.6-sol","choices":[{"delta":{"content":"{\"answer\": 2"}}]}`,
			`{"model":"gpt-5.6-sol","choices":[{"delta":{"content":"1, \"reasoning\": \"x\"}"},"finish_reason":"stop"}]}`,
		} {
			_, _ = io.WriteString(w, "data: "+e+"\n\n")
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}
}

func TestOpenAIClientAskBothFormats(t *testing.T) {
	fake := &fakeOpenAI{}
	server := httptest.NewServer(fake)
	defer server.Close()
	client := NewOpenAIClient(10*time.Second, true) // httptest 是 127.0.0.1，测试里放开内网限制
	ctx := context.Background()
	target := func(format string) ProviderTarget {
		return ProviderTarget{BaseURL: server.URL + "/v1", APIFormat: format, APIKey: "sk-secret-123456789"}
	}

	cases := []struct {
		mode, format, wantText, wantModel, wantPath string
	}{
		{"chat-stream", model.IQProviderChat, `{"answer": 21, "reasoning": "x"}`, "gpt-5.6-sol", "/v1/chat/completions"},
		{"chat-json", model.IQProviderChat, `{"answer": 21, "reasoning": "最坏情况"}`, "gpt-6-astra-0901", "/v1/chat/completions"},
		{"responses-stream", model.IQProviderResponses, `{"answer": 21}`, "gpt-6-astra-final", "/v1/responses"},
		{"responses-json", model.IQProviderResponses, `{"answer": 29}`, "gpt-6-astra", "/v1/responses"},
	}
	for _, c := range cases {
		fake.mode = c.mode
		result := client.Ask(ctx, target(c.format), ModelSpec{Model: "gpt-6-astra", ReasoningEffort: "xhigh"}, "题目")
		if result.Err != nil || result.Text != c.wantText || result.UpstreamModel != c.wantModel || fake.lastPath != c.wantPath {
			t.Errorf("%s: got text=%q model=%q path=%q err=%v", c.mode, result.Text, result.UpstreamModel, fake.lastPath, result.Err)
		}
		if fake.lastAuth != "Bearer sk-secret-123456789" {
			t.Errorf("%s: 应带 Bearer Key", c.mode)
		}
	}

	// 推理强度：chat 用 reasoning_effort，responses 用 reasoning.effort
	fake.mode = "chat-stream"
	client.Ask(ctx, target(model.IQProviderChat), ModelSpec{Model: "m", ReasoningEffort: "high"}, "q")
	if fake.lastBody["reasoning_effort"] != "high" || fake.lastBody["stream"] != true {
		t.Errorf("chat 请求体错: %v", fake.lastBody)
	}
	fake.mode = "responses-stream"
	client.Ask(ctx, target(model.IQProviderResponses), ModelSpec{Model: "m", ReasoningEffort: "high"}, "q")
	if reasoning, _ := fake.lastBody["reasoning"].(map[string]any); reasoning["effort"] != "high" {
		t.Errorf("responses 请求体错: %v", fake.lastBody)
	}
	// 不填推理强度时不带这个字段（有的供应商对未知字段报 400）
	fake.mode = "chat-stream"
	client.Ask(ctx, target(model.IQProviderChat), ModelSpec{Model: "m"}, "q")
	if _, ok := fake.lastBody["reasoning_effort"]; ok {
		t.Errorf("没填推理强度不应带 reasoning_effort: %v", fake.lastBody)
	}
}

// recordingRelay 记下每次请求的会话头和请求体，用来验证「每次检测都是新会话」。
type recordingRelay struct {
	mu       sync.Mutex
	requests []recordedRequest
}

type recordedRequest struct {
	headers http.Header
	body    map[string]any
}

func (r *recordingRelay) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	raw, _ := io.ReadAll(req.Body)
	body := map[string]any{}
	_ = json.Unmarshal(raw, &body)
	r.mu.Lock()
	r.requests = append(r.requests, recordedRequest{headers: req.Header.Clone(), body: body})
	r.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if strings.HasSuffix(req.URL.Path, "/responses") {
		_, _ = io.WriteString(w, `{"model":"m","output_text":"{\"answer\": 21}"}`)
		return
	}
	_, _ = io.WriteString(w, `{"model":"m","choices":[{"message":{"content":"{\"answer\": 21}"}}]}`)
}

// firstUserText 取请求里第一条用户消息的文本——中转按内容兜底算会话哈希时看的就是它。
func firstUserText(t *testing.T, body map[string]any) string {
	t.Helper()
	if messages, ok := body["messages"].([]any); ok {
		return messages[0].(map[string]any)["content"].(string)
	}
	input := body["input"].([]any)[0].(map[string]any)
	return input["content"].([]any)[0].(map[string]any)["text"].(string)
}

func TestEveryProbeIsAFreshSession(t *testing.T) {
	relay := &recordingRelay{}
	server := httptest.NewServer(relay)
	defer server.Close()
	client := NewOpenAIClient(5*time.Second, true)

	for _, format := range []string{model.IQProviderChat, model.IQProviderResponses} {
		relay.requests = nil
		target := ProviderTarget{BaseURL: server.URL + "/v1", APIFormat: format, APIKey: "k"}
		// 和服务里一样：每次检测都换一个随机题面。
		rng := mrand.New(mrand.NewPCG(5, 6))
		sent := map[string]bool{}
		for i := 0; i < 5; i++ {
			prompt := CandyQuestion.Render(rng).Prompt
			sent[prompt] = true
			result := client.Ask(context.Background(), target, ModelSpec{Model: "gpt-6-astra", ReasoningEffort: "xhigh"}, prompt)
			if graded := GradeAnswer(result.Text, CandyQuestion.Answer); result.Err != nil || graded.Outcome != OutcomeCorrect {
				t.Fatalf("%s: 应正常判分: %+v %v", format, graded, result.Err)
			}
		}
		ids, prompts := map[string]bool{}, map[string]bool{}
		for _, req := range relay.requests {
			// 中转认的几种会话头都要带，且彼此一致
			id := req.headers.Get("Session-Id")
			for _, name := range []string{"session_id", "X-Session-Id", "conversation_id"} {
				if got := req.headers.Values(name); len(got) != 1 || got[0] != id {
					// session_id / conversation_id 是带下划线的原样头名，Header.Get 取不到，直接查 map
					if raw := req.headers[name]; len(raw) != 1 || raw[0] != id {
						t.Fatalf("%s: 会话头 %s 应等于 %s，got %v / %v", format, name, id, got, raw)
					}
				}
			}
			if len(id) != 36 || strings.Count(id, "-") != 4 {
				t.Fatalf("%s: 会话 ID 应是 UUID: %q", format, id)
			}
			ids[id] = true
			text := firstUserText(t, req.body)
			if !sent[text] {
				t.Fatalf("%s: 题面应原样发出，不加任何前缀: %q", format, text)
			}
			prompts[text] = true
			switch format {
			case model.IQProviderResponses:
				if req.body["prompt_cache_key"] != id {
					t.Fatalf("Responses 请求体的 prompt_cache_key 应等于会话 ID: %v", req.body["prompt_cache_key"])
				}
			default:
				if _, ok := req.body["prompt_cache_key"]; ok {
					t.Fatalf("Chat Completions 请求体不应带非标准字段 prompt_cache_key")
				}
			}
		}
		if len(ids) != 5 || len(prompts) != 5 {
			t.Fatalf("%s: 5 次检测应是 5 个不同的会话 ID、5 个不同的首条消息，got %d / %d", format, len(ids), len(prompts))
		}
	}
}

func TestOpenAIClientErrorsRedactKeyAndRefuseRedirect(t *testing.T) {
	fake := &fakeOpenAI{mode: "error"}
	server := httptest.NewServer(fake)
	defer server.Close()
	client := NewOpenAIClient(10*time.Second, true)
	target := ProviderTarget{BaseURL: server.URL + "/v1", APIFormat: model.IQProviderChat, APIKey: "sk-secret-123456789"}

	result := client.Ask(context.Background(), target, ModelSpec{Model: "m"}, "q")
	if result.Err == nil || !strings.Contains(result.Err.Error(), "401") || strings.Contains(result.Err.Error(), "sk-secret-123456789") {
		t.Fatalf("错误应带状态码且抹掉 Key: %v", result.Err)
	}
	if _, err := client.ListModels(context.Background(), target); err == nil || strings.Contains(err.Error(), "sk-secret") {
		t.Fatalf("ListModels 错误应抹掉 Key: %v", err)
	}

	fake.mode = "redirect"
	result = client.Ask(context.Background(), target, ModelSpec{Model: "m"}, "q")
	if result.Err == nil || !strings.Contains(result.Err.Error(), "重定向") {
		t.Fatalf("不应跟随重定向（Key 会被带走）: %v", result.Err)
	}
}

func TestOpenAIClientBlocksPrivateTargetsByDefault(t *testing.T) {
	server := httptest.NewServer(&fakeOpenAI{})
	defer server.Close()
	client := NewOpenAIClient(5*time.Second, false)
	result := client.Ask(context.Background(), ProviderTarget{BaseURL: server.URL + "/v1", APIKey: "k"}, ModelSpec{Model: "m"}, "q")
	if result.Err == nil || !strings.Contains(result.Err.Error(), "内网") {
		t.Fatalf("默认应拒绝连 127.0.0.1: %v", result.Err)
	}
}

func TestListModels(t *testing.T) {
	server := httptest.NewServer(&fakeOpenAI{})
	defer server.Close()
	ids, err := NewOpenAIClient(5*time.Second, true).ListModels(context.Background(), ProviderTarget{BaseURL: server.URL + "/v1", APIKey: "k"})
	if err != nil || len(ids) != 2 || ids[0] != "gpt-6-astra" {
		t.Fatalf("ListModels: %v %v", ids, err)
	}
}

func TestSealer(t *testing.T) {
	sealer, err := NewSealer("0123456789abcdef-session-secret")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := sealer.Seal("sk-live-abcdef")
	b, _ := sealer.Seal("sk-live-abcdef")
	if a == b || strings.Contains(a, "sk-live") {
		t.Fatalf("密文应随机且不含明文: %s %s", a, b)
	}
	if plain, err := sealer.Open(a); err != nil || plain != "sk-live-abcdef" {
		t.Fatalf("解密失败: %q %v", plain, err)
	}
	other, _ := NewSealer("another-secret-0123456789")
	if _, err := other.Open(a); err == nil {
		t.Fatal("换了密钥不应能解开")
	}
	if _, err := NewSealer("short"); err == nil {
		t.Fatal("太短的密钥应拒绝")
	}
	if keyHint("sk-proj-1234567890abcd") != "••••abcd" || keyHint("short") != "••••" {
		t.Fatalf("keyHint 错: %s", keyHint("sk-proj-1234567890abcd"))
	}
}
