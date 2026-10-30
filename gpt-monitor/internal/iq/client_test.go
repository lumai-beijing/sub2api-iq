package iq

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func sse(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		b.WriteString("data: " + e + "\n\n")
	}
	return b.String()
}

func TestParseTestStream(t *testing.T) {
	t.Run("拼接答案并取上游模型与注入标记", func(t *testing.T) {
		result := parseTestStream(strings.NewReader(sse(
			`{"type":"test_start","model":"gpt-6-astra"}`,
			`{"type":"content","text":"最少需要 "}`,
			`{"type":"content","text":"21 个"}`,
			`{"type":"content","data":{"codex_ticket_injected":true}}`,
			`{"type":"test_complete","success":true,"model":"gpt-5.6-luna"}`,
		)))
		if result.Err != nil || result.Text != "最少需要 21 个" || result.UpstreamModel != "gpt-5.6-luna" {
			t.Fatalf("解析错: %+v", result)
		}
		if result.TicketInjected == nil || !*result.TicketInjected {
			t.Fatalf("注入标记没读到")
		}
	})
	t.Run("老版本没有上游模型和注入标记", func(t *testing.T) {
		result := parseTestStream(strings.NewReader(sse(`{"type":"content","text":"hi"}`, `{"type":"test_complete","success":true}`)))
		if result.Err != nil || result.UpstreamModel != "" || result.TicketInjected != nil {
			t.Fatalf("老版本应解析成功且两个字段为空: %+v", result)
		}
	})
	t.Run("error 事件", func(t *testing.T) {
		result := parseTestStream(strings.NewReader(sse(`{"type":"test_start"}`, `{"type":"error","error":"API returned 429: rate limited"}`)))
		if result.Err == nil || !strings.Contains(result.Err.Error(), "429") {
			t.Fatalf("应带上游错误: %+v", result)
		}
	})
	t.Run("流在完成前结束", func(t *testing.T) {
		result := parseTestStream(strings.NewReader(sse(`{"type":"content","text":"21"}`)))
		if result.Err == nil {
			t.Fatal("没有 test_complete 应报错")
		}
	})
}

func TestClientSendsAdminKeyAndBody(t *testing.T) {
	var got TestRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/accounts/42/test" || r.Header.Get("x-api-key") != "admin-secret" {
			http.Error(w, "bad", http.StatusUnauthorized)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse(`{"type":"content","text":"21"}`, `{"type":"test_complete","success":true}`))
	}))
	defer server.Close()

	client := NewClient(server.URL+"/", "admin-secret", 5*time.Second)
	result := client.TestAccount(context.Background(), 42, TestRequest{ModelID: "gpt-6-astra", Prompt: "q", ReasoningEffort: "xhigh", ApplyCodexTicket: true, Probe: true})
	if result.Err != nil || result.Text != "21" {
		t.Fatalf("调用失败: %+v", result)
	}
	if got.ModelID != "gpt-6-astra" || got.Prompt != "q" || got.ReasoningEffort != "xhigh" || !got.ApplyCodexTicket || !got.Probe {
		t.Fatalf("请求体错: %+v", got)
	}
}

func TestClientErrorsNeverLeakAdminKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"code":"INVALID_ADMIN_KEY"}`, http.StatusUnauthorized)
	}))
	defer server.Close()
	client := NewClient(server.URL, "super-secret-admin-key", 5*time.Second)
	result := client.TestAccount(context.Background(), 1, TestRequest{})
	if result.Err == nil || strings.Contains(result.Err.Error(), "super-secret-admin-key") {
		t.Fatalf("错误信息不应带 key: %v", result.Err)
	}

	unreachable := NewClient("http://127.0.0.1:1", "super-secret-admin-key", time.Second)
	result = unreachable.TestAccount(context.Background(), 1, TestRequest{})
	if result.Err == nil || strings.Contains(result.Err.Error(), "super-secret-admin-key") || strings.Contains(result.Err.Error(), "127.0.0.1:1/api") {
		t.Fatalf("连接错误不应带 key 或完整 URL: %v", result.Err)
	}
}

func TestVersionAndCompare(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"code":0,"data":{"version":"lmuai-0.2.10.3","site_name":"x"}}`)
	}))
	defer server.Close()
	version, err := NewClient(server.URL, "", time.Second).Version(context.Background())
	if err != nil || version != "lmuai-0.2.10.3" {
		t.Fatalf("版本读取错: %q %v", version, err)
	}
	cases := []struct {
		a, b string
		want int
	}{
		{"lmuai-0.2.10.3", "lmuai-0.2.10.3", 0},
		{"lmuai-0.2.10.3", "lmuai-0.2.14.0", -1},
		{"lmuai-0.2.14.0", "lmuai-0.2.10.3", 1},
		{"lmuai-0.2.10", "lmuai-0.2.10.0", 0},
		{"lmuai-0.2.10.3-rc1", "lmuai-0.2.10.3", 0},
		{"v0.3.0", "lmuai-0.2.99.9", 1},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%s, %s) = %d want %d", c.a, c.b, got, c.want)
		}
	}
}
