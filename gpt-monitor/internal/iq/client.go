package iq

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// TestRequest 是 sub2api 账号测试接口的请求体。后三个字段由 lumai-beijing/sub2api#816 新增；
// 旧版本会忽略它们（并把 prompt 悄悄换成 "hi"），所以调度器在调用前先做版本闸。
type TestRequest struct {
	ModelID          string `json:"model_id"`
	Prompt           string `json:"prompt"`
	ReasoningEffort  string `json:"reasoning_effort,omitempty"`
	ApplyCodexTicket bool   `json:"apply_codex_ticket"`
	Probe            bool   `json:"probe"`
}

// TestResult 是一次测试解析后的结果。Err 非空表示没拿到可判分的答案。
type TestResult struct {
	Text           string
	UpstreamModel  string
	TicketInjected *bool
	Duration       time.Duration
	Err            error
}

// Client 调 sub2api 的管理员接口。adminKey 是全权凭证：只用于请求头，绝不进日志、错误信息和响应。
type Client struct {
	baseURL  string
	adminKey string
	http     *http.Client
}

func NewClient(baseURL, adminKey string, timeout time.Duration) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		adminKey: adminKey,
		http:     &http.Client{Timeout: timeout},
	}
}

// 答案原文最多读这么多：题目只需要结尾的数字，限制住防止异常长的输出撑爆内存。
const maxAnswerBytes = 64 << 10

// TestAccount 调 POST /api/v1/admin/accounts/{id}/test 并解析 SSE。
func (c *Client) TestAccount(ctx context.Context, accountID int64, request TestRequest) TestResult {
	started := time.Now()
	result := c.testAccount(ctx, accountID, request)
	result.Duration = time.Since(started)
	return result
}

func (c *Client) testAccount(ctx context.Context, accountID int64, request TestRequest) TestResult {
	body, err := json.Marshal(request)
	if err != nil {
		return TestResult{Err: fmt.Errorf("encode test request: %w", err)}
	}
	url := c.baseURL + "/api/v1/admin/accounts/" + strconv.FormatInt(accountID, 10) + "/test"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return TestResult{Err: fmt.Errorf("build test request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("x-api-key", c.adminKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return TestResult{Err: fmt.Errorf("test request: %w", scrubURL(err))}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return TestResult{Err: fmt.Errorf("sub2api 返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))}
	}
	return parseTestStream(resp.Body)
}

// testEvent 对应 sub2api 的 TestEvent。#816 之后 test_complete 的 model 是上游实际模型，
// codex_ticket_injected 可能出现在任一事件里。
type testEvent struct {
	Type                string `json:"type"`
	Text                string `json:"text"`
	Model               string `json:"model"`
	Success             bool   `json:"success"`
	Error               string `json:"error"`
	CodexTicketInjected *bool  `json:"codex_ticket_injected"`
	Data                struct {
		CodexTicketInjected *bool `json:"codex_ticket_injected"`
	} `json:"data"`
}

func parseTestStream(body io.Reader) TestResult {
	var (
		result   TestResult
		text     strings.Builder
		complete bool
	)
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		payload, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		payload = strings.TrimSpace(payload)
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var event testEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}
		if flag := firstBool(event.CodexTicketInjected, event.Data.CodexTicketInjected); flag != nil {
			result.TicketInjected = flag
		}
		switch event.Type {
		case "content":
			if text.Len() < maxAnswerBytes {
				text.WriteString(event.Text)
			}
		case "test_complete":
			complete = true
			if strings.TrimSpace(event.Model) != "" {
				result.UpstreamModel = strings.TrimSpace(event.Model)
			}
		case "error":
			result.Text = text.String()
			result.Err = errors.New(firstNonEmpty(event.Error, "sub2api 报告测试失败"))
			return result
		}
	}
	result.Text = text.String()
	if err := scanner.Err(); err != nil {
		result.Err = fmt.Errorf("读取测试流: %w", scrubURL(err))
		return result
	}
	if !complete {
		result.Err = errors.New("测试流在完成前结束")
	}
	return result
}

// Version 读 sub2api 的公开版本号（GET /api/v1/settings/public 的 data.version）。
func (c *Client) Version(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/settings/public", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("read sub2api version: %w", scrubURL(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("read sub2api version: status %d", resp.StatusCode)
	}
	var payload struct {
		Version string `json:"version"`
		Data    struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode sub2api version: %w", err)
	}
	return firstNonEmpty(payload.Data.Version, payload.Version), nil
}

// scrubURL 去掉 *url.Error 里的完整 URL，只留原因，免得内部地址进日志。
func scrubURL(err error) error {
	var urlErr interface{ Unwrap() error }
	if errors.As(err, &urlErr) {
		if inner := urlErr.Unwrap(); inner != nil {
			return inner
		}
	}
	return err
}

func firstBool(values ...*bool) *bool {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// CompareVersions 比较 lmuai-0.2.10.3 这样的版本号。前缀不同或解析不了的段按 0 处理。
func CompareVersions(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func versionParts(v string) []int {
	v = strings.TrimSpace(v)
	if i := strings.IndexFunc(v, func(r rune) bool { return r >= '0' && r <= '9' }); i >= 0 {
		v = v[i:]
	} else {
		return nil
	}
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		n, _ := strconv.Atoi(part)
		out = append(out, n)
	}
	return out
}
