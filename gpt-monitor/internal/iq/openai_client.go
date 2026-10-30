package iq

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	"gpt-pro-monitor/internal/model"
)

// ProviderTarget 是一次发题的目的地：一个 OpenAI 兼容接口。
type ProviderTarget struct {
	BaseURL   string
	APIFormat string // chat_completions | responses
	APIKey    string
}

// ProviderTester 是第三方供应商的发题接口（拆出来是为了测试里换成假的）。
type ProviderTester interface {
	Ask(ctx context.Context, target ProviderTarget, spec ModelSpec, prompt string) TestResult
	ListModels(ctx context.Context, target ProviderTarget) ([]string, error)
}

// OpenAIClient 调第三方 OpenAI 兼容接口。安全上和调 sub2api 不同：地址是用户在界面上填的，
// 所以连接时拒绝回环 / 内网 / 链路本地地址（拨号那一刻按实际解析出的 IP 检查，DNS 重绑定也绕不过去），
// 也不跟随重定向（否则 Key 可能被 302 带到别的主机）。
type OpenAIClient struct {
	http *http.Client
}

// 回答文本最多读这么多：题目只需要结尾的 JSON，防异常长输出撑爆内存。
const maxProviderBody = 2 << 20

func NewOpenAIClient(timeout time.Duration, allowPrivate bool) *OpenAIClient {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if !allowPrivate {
		dialer.Control = rejectPrivateAddress
	}
	transport := &http.Transport{
		Proxy:                 nil, // 不走环境变量里的代理：Key 只发给填写的那个地址
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
	}
	return &OpenAIClient{http: &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

// rejectPrivateAddress 在 TCP 连接前检查目标 IP。
func rejectPrivateAddress(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("无法识别的地址 %q", host)
	}
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() || isSharedOrReserved(ip) {
		return fmt.Errorf("目标地址 %s 是内网或保留地址，不允许访问", ip)
	}
	return nil
}

var reservedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"), // 运营商级 NAT
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), // 基准测试（也是 Clash fake-ip 段）
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("fc00::/7"),
}

func isSharedOrReserved(ip netip.Addr) bool {
	for _, prefix := range reservedPrefixes {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// NormalizeBaseURL 校验并规范化 Base URL：去掉末尾的 /，去掉误填的接口路径（/chat/completions、/responses、/models）。
// 不补 /v1：有的供应商就是要求 /openai/v1 之类的前缀，填什么用什么。
func NormalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("Base URL 不能为空")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", errors.New("Base URL 格式不对，例如 https://api.example.com/v1")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", errors.New("Base URL 只支持 http:// 或 https://")
	}
	if parsed.User != nil {
		return "", errors.New("Base URL 里不要带用户名密码，API Key 单独填")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("Base URL 里不要带 ? 或 # 参数")
	}
	path := strings.TrimRight(parsed.Path, "/")
	for _, suffix := range []string{"/chat/completions", "/responses", "/models", "/completions"} {
		path = strings.TrimSuffix(path, suffix)
	}
	parsed.Path = strings.TrimRight(path, "/")
	parsed.RawPath = ""
	return parsed.String(), nil
}

func endpointFor(target ProviderTarget, path string) string {
	return strings.TrimRight(target.BaseURL, "/") + path
}

// EndpointOf 是实际发题的完整地址，展示给用户核对。
func EndpointOf(baseURL, format string) string {
	if format == model.IQProviderResponses {
		return strings.TrimRight(baseURL, "/") + "/responses"
	}
	return strings.TrimRight(baseURL, "/") + "/chat/completions"
}

// Ask 发一道题。用流式请求：推理模型可能想很久，流式能让「连上了但还在想」和「卡死」区分开，
// 也是第三方中转支持最普遍的形态；对方不支持流式、直接回完整 JSON 时也能解析。
func (c *OpenAIClient) Ask(ctx context.Context, target ProviderTarget, spec ModelSpec, prompt string) TestResult {
	started := time.Now()
	result := c.ask(ctx, target, spec, prompt)
	result.Duration = time.Since(started)
	return result
}

func (c *OpenAIClient) ask(ctx context.Context, target ProviderTarget, spec ModelSpec, prompt string) TestResult {
	// 每次检测都是一个全新的会话，让对方号池按「新会话」重新挑号（见 session.go）。
	// 题面由调用方每次随机改写，这里原样发出。
	session := newProbeSession()

	var (
		body []byte
		path string
	)
	if target.APIFormat == model.IQProviderResponses {
		payload := map[string]any{
			"model":            spec.Model,
			"input":            []map[string]any{{"role": "user", "content": []map[string]any{{"type": "input_text", "text": prompt}}}},
			"stream":           true,
			"store":            false,
			"prompt_cache_key": session.ID,
		}
		if spec.ReasoningEffort != "" {
			payload["reasoning"] = map[string]any{"effort": spec.ReasoningEffort}
		}
		body, _ = json.Marshal(payload)
		path = "/responses"
	} else {
		// Chat Completions 不放 prompt_cache_key：它不是这个接口的标准字段，个别严格校验的供应商会报 400。
		// 会话 ID 走请求头；中转自己从 Chat 转成 Responses 时，也是从请求头取会话 ID。
		payload := map[string]any{
			"model":    spec.Model,
			"messages": []map[string]any{{"role": "user", "content": prompt}},
			"stream":   true,
		}
		if spec.ReasoningEffort != "" {
			payload["reasoning_effort"] = spec.ReasoningEffort
		}
		body, _ = json.Marshal(payload)
		path = "/chat/completions"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointFor(target, path), bytes.NewReader(body))
	if err != nil {
		return TestResult{Err: fmt.Errorf("构造请求失败: %w", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream, application/json")
	req.Header.Set("Authorization", "Bearer "+target.APIKey)
	for _, name := range sessionHeaders {
		// 直接写 map 保留大小写和下划线：Header.Set 会把 session_id 规范化成 Session_id，
		// 大多数服务端按不区分大小写读，但有的按原样匹配。
		req.Header[name] = []string{session.ID}
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return TestResult{Err: fmt.Errorf("请求失败: %w", scrubURL(err))}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return TestResult{Err: upstreamError(resp, target.APIKey)}
	}
	reader := io.LimitReader(resp.Body, maxProviderBody)
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		if target.APIFormat == model.IQProviderResponses {
			return parseResponsesStream(reader)
		}
		return parseChatStream(reader)
	}
	return parseNonStream(reader, target.APIFormat)
}

// upstreamError 把对方的报错整理成一句话，并把 Key 抹掉（有的中转会把收到的 Key 回显在错误里）。
func upstreamError(resp *http.Response, key string) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	message := strings.TrimSpace(string(raw))
	var payload struct {
		Error any    `json:"error"`
		Msg   string `json:"message"`
	}
	if json.Unmarshal(raw, &payload) == nil {
		switch e := payload.Error.(type) {
		case map[string]any:
			if m, ok := e["message"].(string); ok && m != "" {
				message = m
			}
		case string:
			if e != "" {
				message = e
			}
		}
		if payload.Msg != "" && message == strings.TrimSpace(string(raw)) {
			message = payload.Msg
		}
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		message = "接口返回了重定向（" + resp.Header.Get("Location") + "），检查 Base URL 是否填对"
	}
	message = redactKey(truncateRunes(message, 300), key)
	return fmt.Errorf("API returned %d: %s", resp.StatusCode, message)
}

func redactKey(text, key string) string {
	key = strings.TrimSpace(key)
	if len(key) >= 8 {
		text = strings.ReplaceAll(text, key, "[api-key]")
	}
	return text
}

// ---------- 解析 ----------

// sseEvents 逐个产出 SSE 的 data 负载。
func sseEvents(body io.Reader, handle func(payload []byte) (stop bool)) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			return nil
		}
		if handle([]byte(data)) {
			return nil
		}
	}
	return scanner.Err()
}

type streamError struct {
	Error any `json:"error"`
}

func (e streamError) message() string {
	switch v := e.Error.(type) {
	case map[string]any:
		if m, ok := v["message"].(string); ok && m != "" {
			return m
		}
		return "上游返回错误"
	case string:
		return v
	}
	return ""
}

func parseChatStream(body io.Reader) TestResult {
	var (
		result   TestResult
		text     strings.Builder
		finished bool
		failure  string
	)
	err := sseEvents(body, func(payload []byte) bool {
		var event struct {
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			streamError
		}
		if json.Unmarshal(payload, &event) != nil {
			return false
		}
		if msg := event.message(); msg != "" {
			failure = msg
			return true
		}
		if event.Model != "" {
			result.UpstreamModel = event.Model
		}
		for _, choice := range event.Choices {
			if text.Len() < maxAnswerBytes {
				text.WriteString(choice.Delta.Content)
			}
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				finished = true
			}
		}
		return false
	})
	result.Text = text.String()
	switch {
	case failure != "":
		result.Err = errors.New(failure)
	case err != nil:
		result.Err = fmt.Errorf("读取回答流失败: %w", scrubURL(err))
	case !finished && strings.TrimSpace(result.Text) == "":
		result.Err = errors.New("回答流在给出内容前就结束了")
	}
	return result
}

func parseResponsesStream(body io.Reader) TestResult {
	var (
		result    TestResult
		text      strings.Builder
		completed bool
		failure   string
	)
	err := sseEvents(body, func(payload []byte) bool {
		var event struct {
			Type     string `json:"type"`
			Delta    string `json:"delta"`
			Response struct {
				Model string `json:"model"`
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"response"`
			Message string `json:"message"`
			streamError
		}
		if json.Unmarshal(payload, &event) != nil {
			return false
		}
		if event.Response.Model != "" {
			result.UpstreamModel = event.Response.Model
		}
		switch event.Type {
		case "response.output_text.delta":
			if text.Len() < maxAnswerBytes {
				text.WriteString(event.Delta)
			}
		case "response.completed", "response.done":
			completed = true
			return true
		case "response.failed", "response.incomplete":
			failure = "上游回答失败"
			if event.Response.Error != nil && event.Response.Error.Message != "" {
				failure = event.Response.Error.Message
			}
			return true
		case "error":
			failure = firstNonEmpty(event.Message, event.message(), "上游返回错误")
			return true
		}
		return false
	})
	result.Text = text.String()
	switch {
	case failure != "":
		result.Err = errors.New(failure)
	case err != nil:
		result.Err = fmt.Errorf("读取回答流失败: %w", scrubURL(err))
	case !completed && strings.TrimSpace(result.Text) == "":
		result.Err = errors.New("回答流在给出内容前就结束了")
	}
	return result
}

// parseNonStream 兜底：对方忽略了 stream=true、直接回了完整 JSON。
func parseNonStream(body io.Reader, format string) TestResult {
	raw, err := io.ReadAll(body)
	if err != nil {
		return TestResult{Err: fmt.Errorf("读取回答失败: %w", scrubURL(err))}
	}
	if format == model.IQProviderResponses {
		var payload struct {
			Model      string `json:"model"`
			OutputText string `json:"output_text"`
			Output     []struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"output"`
			streamError
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return TestResult{Err: errors.New("回答不是合法的 JSON：" + truncateRunes(strings.TrimSpace(string(raw)), 120))}
		}
		if msg := payload.message(); msg != "" {
			return TestResult{Err: errors.New(msg)}
		}
		text := payload.OutputText
		if text == "" {
			var b strings.Builder
			for _, item := range payload.Output {
				for _, content := range item.Content {
					if content.Type == "output_text" || content.Type == "text" {
						b.WriteString(content.Text)
					}
				}
			}
			text = b.String()
		}
		return TestResult{Text: text, UpstreamModel: payload.Model}
	}
	var payload struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content any `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		streamError
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return TestResult{Err: errors.New("回答不是合法的 JSON：" + truncateRunes(strings.TrimSpace(string(raw)), 120))}
	}
	if msg := payload.message(); msg != "" {
		return TestResult{Err: errors.New(msg)}
	}
	if len(payload.Choices) == 0 {
		return TestResult{Err: errors.New("回答里没有 choices"), UpstreamModel: payload.Model}
	}
	return TestResult{Text: contentText(payload.Choices[0].Message.Content), UpstreamModel: payload.Model}
}

// contentText 兼容 content 是字符串，或是 [{type:text,text:...}] 数组的两种写法。
func contentText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, part := range v {
			if m, ok := part.(map[string]any); ok {
				if t, ok := m["text"].(string); ok {
					b.WriteString(t)
				}
			}
		}
		return b.String()
	}
	return ""
}

// ListModels 调 GET {base}/models，用于「测试连接」。
func (c *OpenAIClient) ListModels(ctx context.Context, target ProviderTarget) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointFor(target, "/models"), nil)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+target.APIKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", scrubURL(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, upstreamError(resp, target.APIKey)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxProviderBody)).Decode(&payload); err != nil {
		return nil, errors.New("/models 返回的不是 OpenAI 格式的 JSON")
	}
	ids := make([]string, 0, len(payload.Data)+len(payload.Models))
	for _, item := range payload.Data {
		if item.ID != "" {
			ids = append(ids, item.ID)
		}
	}
	for _, item := range payload.Models {
		if id := firstNonEmpty(item.ID, item.Name); id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}
