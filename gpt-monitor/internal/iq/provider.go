package iq

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"gpt-pro-monitor/internal/model"
)

// 第三方供应商：任意 OpenAI 兼容接口，用和号池同一道题、同一套判分测智商。
// 每个供应商一个独立的调度循环，周期、随机范围、测试模型各自配置，互不影响，也不影响号池。

const (
	maxProviders           = 20
	maxProviderModels      = 8
	maxSamplesPerModel     = 5
	providerConcurrency    = 2
	providerManualCooldown = 20 * time.Second
	defaultProviderJitter  = 60
)

// providerRuntime 是一个供应商在内存里的调度状态。
type providerRuntime struct {
	cancel context.CancelFunc
	wake   chan struct{}
	done   chan struct{}

	mu             sync.Mutex
	slot           slotPlan
	scheduleKey    [2]int
	skippedSlots   int
	running        bool
	runningKind    string
	runningTargets int
	lastRunAt      time.Time
	lastFinishedAt time.Time
	lastRoundTime  time.Duration
	lastError      string
	lastManualAt   time.Time
}

// ---------- 生命周期 ----------

// startProviders 在 Run 开始时为库里每个供应商起一个调度循环。
func (s *Service) startProviders(ctx context.Context) {
	if s.providers == nil {
		return
	}
	records, err := s.store.ListProviders(ctx)
	if err != nil {
		s.options.Logger.Warn("iq providers not started", "error", err)
		return
	}
	s.mu.Lock()
	s.runCtx = ctx
	s.mu.Unlock()
	for _, record := range records {
		s.ensureProviderLoop(record.ID)
	}
}

// ensureProviderLoop 确保这个供应商有一个在跑的调度循环；已有就唤醒它重读配置。
func (s *Service) ensureProviderLoop(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runCtx == nil || s.runCtx.Err() != nil {
		return // Run 还没开始或已经退出：启动时会统一拉起
	}
	if runtime, ok := s.providerRuntimes[id]; ok {
		select {
		case runtime.wake <- struct{}{}:
		default:
		}
		return
	}
	ctx, cancel := context.WithCancel(s.runCtx)
	runtime := &providerRuntime{cancel: cancel, wake: make(chan struct{}, 1), done: make(chan struct{})}
	s.providerRuntimes[id] = runtime
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer close(runtime.done)
		s.providerLoop(ctx, id, runtime)
	}()
}

// stopProviderLoop 停掉并等这个供应商的循环退出（删除时用，在途的那一题会被丢弃不入库）。
func (s *Service) stopProviderLoop(id int64) {
	s.mu.Lock()
	runtime, ok := s.providerRuntimes[id]
	delete(s.providerRuntimes, id)
	s.mu.Unlock()
	if !ok {
		return
	}
	runtime.cancel()
	select {
	case <-runtime.done:
	case <-time.After(10 * time.Second):
		s.options.Logger.Warn("iq provider loop slow to stop", "provider_id", id)
	}
}

func (s *Service) providerRuntime(id int64) *providerRuntime {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.providerRuntimes[id]
}

// providerLoop 和号池的 Run 同一套锚定周期：锚点对齐到周期整数倍，每轮在锚点 ± 随机范围内开跑。
func (s *Service) providerLoop(ctx context.Context, id int64, runtime *providerRuntime) {
	for {
		record, err := s.store.GetProvider(ctx, id)
		if errors.Is(err, ErrProviderNotFound) || ctx.Err() != nil {
			return
		}
		wait := idleRecheck
		if err == nil && record.Enabled {
			wait = s.planProvider(runtime, record).FireAt.Sub(s.options.Now())
		} else {
			runtime.mu.Lock()
			runtime.slot = slotPlan{}
			runtime.mu.Unlock()
		}
		if wait < 0 {
			wait = 0
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-runtime.wake:
			timer.Stop()
			continue
		case <-timer.C:
		}
		if err != nil || !record.Enabled {
			continue
		}
		current, err := s.store.GetProvider(ctx, id)
		if err != nil || !current.Enabled || providerKey(current) != providerKey(record) {
			continue
		}
		s.runProviderRound(ctx, current, runtime, "schedule", nil)
		s.advanceProvider(runtime, current)
	}
}

func providerKey(r ProviderRecord) [2]int { return [2]int{r.IntervalSeconds, r.JitterSeconds} }

func (s *Service) planProvider(runtime *providerRuntime, record ProviderRecord) slotPlan {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	key := providerKey(record)
	if runtime.slot.Anchor.IsZero() || runtime.scheduleKey != key {
		interval := time.Duration(record.IntervalSeconds) * time.Second
		jitter := time.Duration(record.JitterSeconds) * time.Second
		s.mu.Lock()
		runtime.slot = planSlot(firstAnchor(s.options.Now(), anchorOrigin(s.options.Location), interval, jitter), jitter, s.rng)
		s.mu.Unlock()
		runtime.scheduleKey = key
		s.options.Logger.Info("iq provider planned", "provider_id", record.ID, "name", record.Name,
			"anchor", runtime.slot.Anchor.UTC().Format(time.RFC3339), "fire_at", runtime.slot.FireAt.UTC().Format(time.RFC3339))
	}
	return runtime.slot
}

func (s *Service) advanceProvider(runtime *providerRuntime, record ProviderRecord) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.slot.Anchor.IsZero() || runtime.scheduleKey != providerKey(record) {
		return
	}
	interval := time.Duration(record.IntervalSeconds) * time.Second
	jitter := time.Duration(record.JitterSeconds) * time.Second
	anchor, skipped := nextAnchorAfter(runtime.slot.Anchor, s.options.Now(), interval, jitter)
	runtime.skippedSlots += skipped
	s.mu.Lock()
	runtime.slot = planSlot(anchor, jitter, s.rng)
	s.mu.Unlock()
}

// ---------- 一轮 ----------

type providerJob struct {
	spec ModelSpec
}

// runProviderRound 把每个测试模型各测 SamplesPerModel 次。only 非空时只测这一个模型（手动「立即检测」某张卡）。
func (s *Service) runProviderRound(ctx context.Context, record ProviderRecord, runtime *providerRuntime, trigger string, only *ModelSpec) {
	specs, err := ParseModelSpecs(record.Models)
	if err != nil || len(specs) == 0 {
		s.setProviderError(runtime, "测试模型配置无效")
		return
	}
	jobs := []providerJob{}
	for _, spec := range specs {
		if only != nil && spec != *only {
			continue
		}
		for i := 0; i < max(record.SamplesPerModel, 1); i++ {
			jobs = append(jobs, providerJob{spec: spec})
		}
	}
	key, err := s.sealer.Open(record.APIKeySealed)
	if err != nil {
		s.setProviderError(runtime, err.Error())
		return
	}
	if !beginProvider(runtime, trigger, len(jobs), s.options.Now()) {
		s.setProviderError(runtime, "上一轮检测还没结束，跳过了这一轮")
		return
	}
	defer finishProvider(runtime, s.options.Now)

	target := ProviderTarget{BaseURL: record.BaseURL, APIFormat: record.APIFormat, APIKey: key}
	runID := newRunID()
	semaphore := make(chan struct{}, providerConcurrency)
	var wg sync.WaitGroup
	for _, job := range jobs {
		wg.Add(1)
		semaphore <- struct{}{}
		go func(job providerJob) {
			defer wg.Done()
			defer func() { <-semaphore }()
			s.probeProvider(ctx, record, target, runID, trigger, job.spec)
		}(job)
	}
	wg.Wait()
}

func (s *Service) probeProvider(ctx context.Context, record ProviderRecord, target ProviderTarget, runID, trigger string, spec ModelSpec) {
	variant := s.renderQuestion()
	requestCtx, cancel := context.WithTimeout(ctx, s.options.RequestTimeout)
	defer cancel()
	result := s.providers.Ask(requestCtx, target, spec, variant.Prompt)
	// 删除供应商或进程退出打断的不入库，同号池的处理。
	if result.Err != nil && ctx.Err() != nil {
		return
	}
	probe := Probe{
		ProviderID:      record.ID,
		RunID:           runID,
		AccountName:     record.Name,
		Model:           spec.Model,
		ReasoningEffort: spec.ReasoningEffort,
		QuestionID:      CandyQuestion.ID,
		QuestionVariant: variant.Signature,
		Prompt:          variant.Prompt,
		AnswerExcerpt:   truncateRunes(strings.TrimSpace(result.Text), maxExcerptRunes),
		UpstreamModel:   result.UpstreamModel,
		DurationMs:      result.Duration.Milliseconds(),
		Trigger:         trigger,
		CreatedAt:       s.options.Now().UTC(),
	}
	if result.UpstreamModel != "" {
		probe.ModelVerdict = s.options.ClassifyModel(spec.Model, result.UpstreamModel)
	}
	if result.Err != nil {
		probe.Outcome = OutcomeError
		probe.ErrorMessage = truncateRunes(redactKey(result.Err.Error(), target.APIKey), 500)
	} else {
		graded := GradeAnswer(result.Text, CandyQuestion.Answer)
		probe.Outcome, probe.AnswerValue = graded.Outcome, graded.Value
		probe.AnswerFormat, probe.AnswerReasoning = graded.Format, graded.Reasoning
	}
	writeCtx, writeCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer writeCancel()
	if _, err := s.store.InsertProbe(writeCtx, probe); err != nil {
		s.options.Logger.Error("iq provider probe not saved", "provider_id", record.ID, "error", err)
		return
	}
	s.options.Logger.Info("iq provider probe",
		"run_id", runID, "provider_id", record.ID, "provider", record.Name, "model", spec.Model, "effort", spec.ReasoningEffort,
		"outcome", probe.Outcome, "answer", probe.AnswerValue, "format", probe.AnswerFormat,
		"upstream_model", probe.UpstreamModel, "duration_ms", probe.DurationMs)
}

func beginProvider(runtime *providerRuntime, kind string, targets int, now time.Time) bool {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.running {
		return false
	}
	runtime.running, runtime.runningKind, runtime.runningTargets = true, kind, targets
	runtime.lastRunAt, runtime.lastError = now, ""
	return true
}

func finishProvider(runtime *providerRuntime, now func() time.Time) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.running, runtime.runningKind, runtime.runningTargets = false, "", 0
	runtime.lastFinishedAt = now()
	runtime.lastRoundTime = runtime.lastFinishedAt.Sub(runtime.lastRunAt)
}

func (s *Service) setProviderError(runtime *providerRuntime, message string) {
	runtime.mu.Lock()
	runtime.lastError = message
	runtime.mu.Unlock()
	s.options.Logger.Warn("iq provider round skipped", "error", message)
}

// ---------- 增删改查 ----------

func (s *Service) requireProviders() error {
	if s.providers == nil || s.sealer == nil {
		return &model.IQError{Status: http.StatusNotFound, Message: "第三方供应商未启用"}
	}
	return nil
}

// validateProvider 校验输入并组装成库里的记录。existing 非空表示编辑（API Key 留空 = 不改）。
func (s *Service) validateProvider(input model.IQProviderInput, existing *ProviderRecord) (ProviderRecord, error) {
	bad := func(message string) (ProviderRecord, error) {
		return ProviderRecord{}, &model.IQError{Status: http.StatusBadRequest, Message: message}
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len([]rune(name)) > 40 {
		return bad("名称需在 1 到 40 个字之间")
	}
	baseURL, err := NormalizeBaseURL(input.BaseURL)
	if err != nil {
		return bad(err.Error())
	}
	format := strings.TrimSpace(input.APIFormat)
	if format == "" {
		format = model.IQProviderChat
	}
	if format != model.IQProviderChat && format != model.IQProviderResponses {
		return bad("接口格式只能是 Chat Completions 或 Responses")
	}
	specs, err := ParseModelSpecs(input.Models)
	if err != nil {
		return bad(err.Error())
	}
	if len(specs) == 0 {
		return bad("至少配置一个测试模型")
	}
	if len(specs) > maxProviderModels {
		return bad(fmt.Sprintf("测试模型最多 %d 个", maxProviderModels))
	}
	schedule := model.IQScheduleConfig{
		Enabled: input.Enabled, IntervalSeconds: input.IntervalSeconds, JitterSeconds: input.JitterSeconds,
		SampleMode: model.IQSampleCount, SampleCount: 1,
	}
	if err := ValidateSchedule(schedule); err != nil {
		return bad(err.Error())
	}
	if input.SamplesPerModel < 1 || input.SamplesPerModel > maxSamplesPerModel {
		return bad(fmt.Sprintf("每个模型每轮测 1 到 %d 次", maxSamplesPerModel))
	}

	record := ProviderRecord{
		Name: name, BaseURL: baseURL, APIFormat: format, Models: FormatModelSpecs(specs), Enabled: input.Enabled,
		IntervalSeconds: input.IntervalSeconds, JitterSeconds: input.JitterSeconds, SamplesPerModel: input.SamplesPerModel,
	}
	key := strings.TrimSpace(input.APIKey)
	switch {
	case key != "":
		if len(key) > 512 || strings.ContainsAny(key, " \t\r\n") {
			return bad("API Key 格式不对（不能有空格或换行）")
		}
		sealed, err := s.sealer.Seal(key)
		if err != nil {
			return ProviderRecord{}, err
		}
		record.APIKeySealed, record.APIKeyHint = sealed, keyHint(key)
	case existing != nil:
		// 不重填 Key 时，只允许发往原来的主机：防止有人改了 Base URL，把存着的 Key 发到别的服务器上。
		if hostOf(existing.BaseURL) != hostOf(baseURL) {
			return bad("改了 Base URL 的主机，需要重新填写 API Key")
		}
		record.APIKeySealed, record.APIKeyHint = existing.APIKeySealed, existing.APIKeyHint
	default:
		return bad("请填写 API Key")
	}
	return record, nil
}

func hostOf(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Scheme + "://" + parsed.Host)
}

func (s *Service) CreateProvider(ctx context.Context, input model.IQProviderInput) (model.IQProviderView, error) {
	if err := s.requireProviders(); err != nil {
		return model.IQProviderView{}, err
	}
	count, err := s.store.CountProviders(ctx)
	if err != nil {
		return model.IQProviderView{}, err
	}
	if count >= maxProviders {
		return model.IQProviderView{}, &model.IQError{Status: http.StatusConflict, Message: fmt.Sprintf("最多添加 %d 个供应商", maxProviders)}
	}
	record, err := s.validateProvider(input, nil)
	if err != nil {
		return model.IQProviderView{}, err
	}
	now := s.options.Now().UTC()
	record.CreatedAt, record.UpdatedAt = now, now
	id, err := s.store.InsertProvider(ctx, record)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return model.IQProviderView{}, &model.IQError{Status: http.StatusConflict, Message: "已经有同名的供应商"}
		}
		return model.IQProviderView{}, err
	}
	s.options.Logger.Info("iq provider created", "provider_id", id, "name", record.Name, "host", hostOf(record.BaseURL),
		"models", record.Models, "interval_seconds", record.IntervalSeconds, "enabled", record.Enabled)
	s.ensureProviderLoop(id)
	return s.providerView(ctx, id)
}

func (s *Service) UpdateProvider(ctx context.Context, id int64, input model.IQProviderInput) (model.IQProviderView, error) {
	if err := s.requireProviders(); err != nil {
		return model.IQProviderView{}, err
	}
	existing, err := s.store.GetProvider(ctx, id)
	if errors.Is(err, ErrProviderNotFound) {
		return model.IQProviderView{}, &model.IQError{Status: http.StatusNotFound, Message: "供应商不存在"}
	}
	if err != nil {
		return model.IQProviderView{}, err
	}
	record, err := s.validateProvider(input, &existing)
	if err != nil {
		return model.IQProviderView{}, err
	}
	record.ID, record.CreatedAt, record.UpdatedAt = id, existing.CreatedAt, s.options.Now().UTC()
	if err := s.store.UpdateProvider(ctx, record); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return model.IQProviderView{}, &model.IQError{Status: http.StatusConflict, Message: "已经有同名的供应商"}
		}
		return model.IQProviderView{}, err
	}
	s.options.Logger.Info("iq provider updated", "provider_id", id, "name", record.Name, "host", hostOf(record.BaseURL),
		"models", record.Models, "interval_seconds", record.IntervalSeconds, "enabled", record.Enabled,
		"key_changed", strings.TrimSpace(input.APIKey) != "")
	s.ensureProviderLoop(id) // 唤醒，按新配置重排
	return s.providerView(ctx, id)
}

func (s *Service) DeleteProvider(ctx context.Context, id int64, keepProbes bool) error {
	if err := s.requireProviders(); err != nil {
		return err
	}
	s.stopProviderLoop(id)
	if err := s.store.DeleteProvider(ctx, id, keepProbes); err != nil {
		if errors.Is(err, ErrProviderNotFound) {
			return &model.IQError{Status: http.StatusNotFound, Message: "供应商不存在"}
		}
		return err
	}
	s.options.Logger.Info("iq provider deleted", "provider_id", id, "keep_probes", keepProbes)
	return nil
}

// TestProvider 调一次 GET /models 验证地址和 Key，并检查测试模型在不在对方的模型列表里。
func (s *Service) TestProvider(ctx context.Context, request model.IQProviderTestRequest) (model.IQProviderTestResult, error) {
	if err := s.requireProviders(); err != nil {
		return model.IQProviderTestResult{}, err
	}
	baseURL, err := NormalizeBaseURL(request.BaseURL)
	if err != nil {
		return model.IQProviderTestResult{}, &model.IQError{Status: http.StatusBadRequest, Message: err.Error()}
	}
	key := strings.TrimSpace(request.APIKey)
	if key == "" && request.ProviderID > 0 {
		existing, err := s.store.GetProvider(ctx, request.ProviderID)
		if err != nil {
			return model.IQProviderTestResult{}, &model.IQError{Status: http.StatusNotFound, Message: "供应商不存在"}
		}
		if hostOf(existing.BaseURL) != hostOf(baseURL) {
			return model.IQProviderTestResult{}, &model.IQError{Status: http.StatusBadRequest, Message: "改了 Base URL 的主机，需要重新填写 API Key"}
		}
		if key, err = s.sealer.Open(existing.APIKeySealed); err != nil {
			return model.IQProviderTestResult{}, &model.IQError{Status: http.StatusConflict, Message: err.Error()}
		}
	}
	if key == "" {
		return model.IQProviderTestResult{}, &model.IQError{Status: http.StatusBadRequest, Message: "请填写 API Key"}
	}
	format := request.APIFormat
	if format != model.IQProviderResponses {
		format = model.IQProviderChat
	}

	testCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	started := time.Now()
	ids, err := s.providers.ListModels(testCtx, ProviderTarget{BaseURL: baseURL, APIFormat: format, APIKey: key})
	result := model.IQProviderTestResult{Endpoint: EndpointOf(baseURL, format), LatencyMs: time.Since(started).Milliseconds()}
	if err != nil {
		result.Message = redactKey(err.Error(), key)
		return result, nil
	}
	result.OK = true
	result.ModelCount = len(ids)
	available := map[string]bool{}
	for _, id := range ids {
		available[strings.ToLower(id)] = true
	}
	if specs, err := ParseModelSpecs(request.Models); err == nil {
		for _, spec := range specs {
			if !available[strings.ToLower(spec.Model)] {
				result.Missing = append(result.Missing, spec.Model)
			}
		}
	}
	result.Sample = ids[:min(len(ids), 12)]
	switch {
	case len(ids) == 0:
		result.Message = "连接成功，但 /models 没有返回模型列表（有的中转不实现这个接口，不影响发题）"
	case len(result.Missing) > 0:
		result.Message = fmt.Sprintf("连接成功，共 %d 个模型；%s 不在对方的模型列表里", len(ids), strings.Join(result.Missing, "、"))
	default:
		result.Message = fmt.Sprintf("连接成功，共 %d 个模型，测试模型都在列表里", len(ids))
	}
	return result, nil
}

// RunProvider 立即跑一轮。model 非空时只测这一个模型（卡片上的「立即检测」）。
func (s *Service) RunProvider(ctx context.Context, id int64, modelLabel string) (model.IQRunResponse, error) {
	if err := s.requireProviders(); err != nil {
		return model.IQRunResponse{}, err
	}
	record, err := s.store.GetProvider(ctx, id)
	if err != nil {
		return model.IQRunResponse{}, &model.IQError{Status: http.StatusNotFound, Message: "供应商不存在"}
	}
	var only *ModelSpec
	if strings.TrimSpace(modelLabel) != "" {
		specs, _ := ParseModelSpecs(record.Models)
		for i := range specs {
			if specs[i].String() == modelLabel || specs[i].Model == modelLabel {
				only = &specs[i]
				break
			}
		}
		if only == nil {
			return model.IQRunResponse{}, &model.IQError{Status: http.StatusBadRequest, Message: "模型不在这个供应商的测试清单里：" + modelLabel}
		}
	}
	runtime := s.providerRuntime(id)
	if runtime == nil {
		return model.IQRunResponse{}, &model.IQError{Status: http.StatusServiceUnavailable, Message: "检测服务还没就绪，稍后再试"}
	}
	runtime.mu.Lock()
	if runtime.running {
		runtime.mu.Unlock()
		return model.IQRunResponse{}, &model.IQError{Status: http.StatusConflict, Message: "这个供应商上一轮检测还没结束"}
	}
	if since := s.options.Now().Sub(runtime.lastManualAt); since < providerManualCooldown {
		runtime.mu.Unlock()
		return model.IQRunResponse{}, &model.IQError{Status: http.StatusTooManyRequests,
			Message: fmt.Sprintf("操作太频繁，%d 秒后再试", int((providerManualCooldown-since).Seconds())+1)}
	}
	runtime.lastManualAt = s.options.Now()
	runtime.mu.Unlock()

	specs, _ := ParseModelSpecs(record.Models)
	targets := len(specs) * max(record.SamplesPerModel, 1)
	if only != nil {
		targets = max(record.SamplesPerModel, 1)
	}
	s.mu.Lock()
	runCtx := s.runCtx
	s.mu.Unlock()
	if runCtx == nil {
		runCtx = context.WithoutCancel(ctx)
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.runProviderRound(runCtx, record, runtime, "manual", only)
	}()
	return model.IQRunResponse{Kind: "provider", Targets: targets}, nil
}
