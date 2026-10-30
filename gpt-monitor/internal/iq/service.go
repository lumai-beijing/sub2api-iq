package iq

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	mrand "math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"gpt-pro-monitor/internal/model"
)

// CandidateSource 读 sub2api（只读）：此刻可被抽中的号、OpenAI 分组、号所属的分组。
type CandidateSource interface {
	ListIQCandidates(ctx context.Context) ([]model.IQCandidate, error)
	ListIQGroups(ctx context.Context) ([]model.IQGroupInfo, error)
	AccountGroups(ctx context.Context, accountIDs []int64) (map[int64][]int64, error)
}

// Tester 调 sub2api 的账号测试接口；Version 读 sub2api 版本，用于版本闸。
type Tester interface {
	TestAccount(ctx context.Context, accountID int64, request TestRequest) TestResult
	Version(ctx context.Context) (string, error)
}

type Options struct {
	Interval       time.Duration
	Jitter         time.Duration
	Concurrency    int
	RequestTimeout time.Duration
	Retention      time.Duration
	MinVersion     string
	DefaultModels  []ModelSpec
	ApplyTicket    bool
	HistoryPoints  int
	RecentWindow   int
	MinSamples     int
	HealthyPercent float64
	WarningPercent float64
	// ClassifyModel 判断上游返回的模型相对请求模型是否「降智」，与打票看板同一套谱系规则。
	ClassifyModel func(requested, observed string) string
	// Location 是锚点对齐用的时区（看板的显示时区），nil 按 UTC。
	Location *time.Location
	Logger   *slog.Logger
	Now      func() time.Time
	Rand     *mrand.Rand
}

const (
	settingModels   = "models"
	settingSchedule = "schedule"
	settingGroups   = "groups"
	// settingPaused 是旧版的暂停开关，只读不写：没存过 schedule 时用来决定默认是否开启。
	settingPaused = "paused"
	// 版本号缓存：sub2api 升级后最多 10 分钟内跟上，期间不重复请求。
	versionTTL = 10 * time.Minute
	// 「立即检测」的冷却：防止手抖连点把一批号打满。
	manualCooldown  = 20 * time.Second
	maxExcerptRunes = 2000
)

// Service 是智商检测：定时抽号、调测试接口、判分入库，并给看板出数据。
type Service struct {
	store      *Store
	candidates CandidateSource
	tester     Tester
	options    Options

	mu             sync.Mutex
	rng            *mrand.Rand
	running        bool
	runningKind    string
	runningTargets int
	lastRunAt      time.Time
	lastFinishedAt time.Time
	lastRoundTime  time.Duration
	lastError      string
	lastCandidates int
	lastPool       int // 上一轮号池抽样的号数
	lastTopUps     int // 上一轮为分组补测的号数
	lastManualAt   time.Time
	version        string
	versionAt      time.Time
	versionErr     string
	// 锚定周期的当前计划。scheduleKey 记住生成它的周期/随机范围，配置一改就作废重排。
	slot         slotPlan
	scheduleKey  [2]int
	skippedSlots int

	// 第三方供应商：每个一个调度循环。providers / sealer 为 nil 时整个功能关闭。
	providers        ProviderTester
	sealer           *Sealer
	providerRuntimes map[int64]*providerRuntime
	runCtx           context.Context

	wake chan struct{}
	wg   sync.WaitGroup
}

// EnableProviders 打开第三方供应商功能。要在 Run 之前调用。
func (s *Service) EnableProviders(tester ProviderTester, sealer *Sealer) {
	s.providers, s.sealer = tester, sealer
}

func NewService(store *Store, candidates CandidateSource, tester Tester, options Options) *Service {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.Concurrency <= 0 {
		options.Concurrency = 2
	}
	if options.HistoryPoints <= 0 {
		options.HistoryPoints = 180
	}
	if options.RecentWindow <= 0 {
		options.RecentWindow = 30
	}
	if options.MinSamples <= 0 {
		options.MinSamples = 5
	}
	if options.ClassifyModel == nil {
		options.ClassifyModel = func(requested, observed string) string {
			if strings.TrimSpace(observed) == "" {
				return "blank"
			}
			if strings.EqualFold(strings.TrimSpace(requested), strings.TrimSpace(observed)) {
				return "same"
			}
			return "unknown"
		}
	}
	rng := options.Rand
	if rng == nil {
		rng = mrand.New(mrand.NewPCG(uint64(time.Now().UnixNano()), 0x9e3779b97f4a7c15))
	}
	return &Service{store: store, candidates: candidates, tester: tester, options: options, rng: rng,
		wake: make(chan struct{}, 1), providerRuntimes: map[int64]*providerRuntime{}}
}

// ---------- 调度 ----------

// 定时关闭时多久醒一次：只为了跟进配置变化（正常情况下改配置会直接唤醒，这里是兜底）。
const idleRecheck = time.Minute

// Run 是调度循环。周期是锚定的：锚点按周期固定推进（对齐到周期的整数倍），
// 每轮在锚点 ± 随机范围内开跑，不受上一轮跑了多久影响；上一轮没跑完不会开新一轮。
// 配置每次循环现读，界面上改完立刻唤醒循环、按新配置重排。
func (s *Service) Run(ctx context.Context) {
	pruneTicker := time.NewTicker(6 * time.Hour)
	defer pruneTicker.Stop()
	s.prune(ctx)
	s.startProviders(ctx)
	for {
		config, _ := s.scheduleConfig(ctx)
		wait := idleRecheck
		if config.Enabled {
			// 用服务自己的时钟算等待时长：计划时刻也是按它排的，混用 time.Now 会在测试时钟下等错。
			wait = s.plan(config).FireAt.Sub(s.options.Now())
		} else {
			s.clearPlan()
		}
		if wait < 0 {
			wait = 0
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.wg.Wait()
			return
		case <-pruneTicker.C:
			timer.Stop()
			s.prune(ctx)
			continue
		case <-s.wake:
			timer.Stop()
			continue
		case <-timer.C:
		}
		if !config.Enabled {
			continue
		}
		// 醒来后再读一次：等待期间配置可能被改了（唤醒信号偶尔会和定时器同时到）。
		if current, _ := s.scheduleConfig(ctx); !current.Enabled || scheduleKeyOf(current) != scheduleKeyOf(config) {
			continue
		}
		s.runScheduledRound(ctx, config)
		s.advancePlan(config)
	}
}

func scheduleKeyOf(c model.IQScheduleConfig) [2]int {
	return [2]int{c.IntervalSeconds, c.JitterSeconds}
}

// plan 返回当前周期的计划；第一次运行、或周期/随机范围改过之后，按新配置重新对齐锚点。
func (s *Service) plan(config model.IQScheduleConfig) slotPlan {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := scheduleKeyOf(config)
	if s.slot.Anchor.IsZero() || s.scheduleKey != key {
		interval := time.Duration(config.IntervalSeconds) * time.Second
		jitter := time.Duration(config.JitterSeconds) * time.Second
		s.slot = planSlot(firstAnchor(s.options.Now(), anchorOrigin(s.options.Location), interval, jitter), jitter, s.rng)
		s.scheduleKey = key
		s.logSlot("iq schedule planned", 0)
	}
	return s.slot
}

// logSlot 记下排好的下一轮，方便从容器日志核对节奏。调用方持有 s.mu。
func (s *Service) logSlot(message string, skipped int) {
	s.options.Logger.Info(message,
		"anchor", s.slot.Anchor.UTC().Format(time.RFC3339), "fire_at", s.slot.FireAt.UTC().Format(time.RFC3339),
		"interval_seconds", s.scheduleKey[0], "jitter_seconds", s.scheduleKey[1], "skipped", skipped)
}

// advancePlan 在一轮跑完后推进到下一个锚点。这一轮跑得太久、错过了下一个窗口时直接跳过，不补跑。
func (s *Service) advancePlan(config model.IQScheduleConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.slot.Anchor.IsZero() || s.scheduleKey != scheduleKeyOf(config) {
		return // 跑的过程中配置被改了：下次循环由 plan 重新对齐
	}
	interval := time.Duration(config.IntervalSeconds) * time.Second
	jitter := time.Duration(config.JitterSeconds) * time.Second
	anchor, skipped := nextAnchorAfter(s.slot.Anchor, s.options.Now(), interval, jitter)
	s.skippedSlots += skipped
	// 跳过之后，新锚点的窗口可能已经开始了：抽到的时刻落在过去就立刻跑。
	s.slot = planSlot(anchor, jitter, s.rng)
	s.logSlot("iq schedule next", skipped)
}

func (s *Service) clearPlan() {
	s.mu.Lock()
	s.slot = slotPlan{}
	s.mu.Unlock()
}

// notify 唤醒调度循环，让它按最新配置重排。非阻塞：已有一个待处理的唤醒就够了。
func (s *Service) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) prune(ctx context.Context) {
	if s.options.Retention <= 0 {
		return
	}
	pruneCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	removed, err := s.store.Prune(pruneCtx, s.options.Now().Add(-s.options.Retention))
	if err != nil {
		s.options.Logger.Warn("iq prune failed", "error", err)
		return
	}
	if removed > 0 {
		s.options.Logger.Info("iq probes pruned", "rows", removed)
	}
}

// runScheduledRound 跑一轮：版本闸 → 抽号 → 测试 → 入库。是否开启由调用方（Run）判断。
func (s *Service) runScheduledRound(ctx context.Context, config model.IQScheduleConfig) {
	if gate := s.gate(ctx, false); !gate.Open {
		s.setLastError(gateMessage(gate))
		return
	}
	targets, round, err := s.pickTargets(ctx, config)
	if err != nil {
		s.setLastError(err.Error())
		s.options.Logger.Warn("iq round skipped", "error", err)
		return
	}
	s.recordRound(round)
	if len(targets) == 0 {
		s.setLastError("当前没有可用的号")
		return
	}
	if !s.begin("schedule", len(targets)) {
		// 只可能是手动「立即检测」还在跑：定时轮本身是串行的。
		s.setLastError("手动检测还没结束，跳过了这一轮定时检测")
		return
	}
	s.execute(ctx, newRunID(), "schedule", targets)
}

type target struct {
	candidate model.IQCandidate
	spec      ModelSpec
	// sampleGroup > 0：号池抽完后为这个分组补测的。号池整体的统计不算它，免得偏向被选中的分组。
	sampleGroup int64
}

// roundInfo 记一轮抽样的构成，给看板展示。
type roundInfo struct {
	available int // 可用的 OAuth 号（号池整体的抽样基数）
	pool      int // 号池抽样抽了几个
	topUps    int // 为分组补测了几个
}

func (s *Service) recordRound(round roundInfo) {
	s.mu.Lock()
	s.lastCandidates, s.lastPool, s.lastTopUps = round.available, round.pool, round.topUps
	s.mu.Unlock()
}

// pickTargets 先按配置的比例或个数从可用的 OAuth 号里抽号（号池整体），再给每个选中的分组补测：
// 这一轮里属于该分组的号不到 GroupPerRound 个时，从组内可用的号（OAuth 或 API Key）里补齐。
// 每个号随机分配一个已配置的测试模型。
func (s *Service) pickTargets(ctx context.Context, config model.IQScheduleConfig) ([]target, roundInfo, error) {
	specs, _, err := s.models(ctx)
	if err != nil {
		return nil, roundInfo{}, err
	}
	if len(specs) == 0 {
		return nil, roundInfo{}, errors.New("没有配置测试模型")
	}
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows, err := s.candidates.ListIQCandidates(listCtx)
	if err != nil {
		return nil, roundInfo{}, fmt.Errorf("读取可用账号失败: %w", err)
	}
	last, err := s.store.LastProbedAt(ctx)
	if err != nil {
		return nil, roundInfo{}, err
	}
	groups := s.groupConfig(ctx)
	now := s.options.Now()
	cooldown := time.Duration(config.IntervalSeconds) * time.Second

	oauth := make([]model.IQCandidate, 0, len(rows))
	for _, row := range rows {
		if isOAuth(row.Type) {
			oauth = append(oauth, row)
		}
	}
	round := roundInfo{available: len(oauth)}

	s.mu.Lock()
	defer s.mu.Unlock()
	picked := map[int64]bool{}
	targets := []target{}
	add := func(rows []model.IQCandidate, sampleGroup int64) {
		for _, row := range rows {
			picked[row.ID] = true
			targets = append(targets, target{candidate: row, spec: specs[s.rng.IntN(len(specs))], sampleGroup: sampleGroup})
		}
	}
	add(s.sampleFresh(oauth, SampleSizeFor(config, len(oauth)), last, now, cooldown), 0)
	round.pool = len(targets)

	for _, groupID := range groups.IDs {
		members, have := []model.IQCandidate{}, 0
		for _, row := range rows {
			if !containsID(row.GroupIDs, groupID) {
				continue
			}
			if picked[row.ID] {
				have++
			} else {
				members = append(members, row)
			}
		}
		if need := groups.PerRound - have; need > 0 {
			add(s.sampleFresh(members, need, last, now, cooldown), groupID)
		}
	}
	round.topUps = len(targets) - round.pool
	return targets, round, nil
}

// sampleFresh 抽 n 个。刚测过的号（一个周期内）这轮不再抽：定时轮和手动「立即检测」挨得近时，
// 同一个号可能 20 秒内被问两次。可用号太少、排除后凑不够时放宽，宁可重复也要凑满样本数。调用方持有 s.mu。
func (s *Service) sampleFresh(rows []model.IQCandidate, n int, last map[int64]time.Time, now time.Time, cooldown time.Duration) []model.IQCandidate {
	if n <= 0 || len(rows) == 0 {
		return nil
	}
	byID := make(map[int64]model.IQCandidate, len(rows))
	fresh := make([]Candidate, 0, len(rows))
	all := make([]Candidate, 0, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
		candidate := Candidate{ID: row.ID, Name: row.Name}
		all = append(all, candidate)
		if at, ok := last[row.ID]; !ok || now.Sub(at) >= cooldown {
			fresh = append(fresh, candidate)
		}
	}
	pool := fresh
	if len(pool) < n {
		pool = all
	}
	out := []model.IQCandidate{}
	for _, c := range Sample(pool, n, last, now, s.rng) {
		out = append(out, byID[c.ID])
	}
	return out
}

func isOAuth(accountType string) bool {
	return accountType == "" || accountType == model.IQAccountOAuth
}

func containsID(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func (s *Service) begin(kind string, targets int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return false
	}
	s.running, s.runningKind, s.runningTargets = true, kind, targets
	s.lastRunAt = s.options.Now()
	s.lastError = ""
	return true
}

func (s *Service) finish() {
	s.mu.Lock()
	s.running, s.runningKind, s.runningTargets = false, "", 0
	s.lastFinishedAt = s.options.Now()
	s.lastRoundTime = s.lastFinishedAt.Sub(s.lastRunAt)
	s.mu.Unlock()
}

// execute 并发跑一批测试并入库。并发上限 Concurrency：xhigh/max 推理单次要几十秒到几分钟，
// 并发太高会让同一时刻多个号同时占着上游。
func (s *Service) execute(ctx context.Context, runID, trigger string, targets []target) {
	defer s.finish()
	semaphore := make(chan struct{}, s.options.Concurrency)
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		semaphore <- struct{}{}
		go func(t target) {
			defer wg.Done()
			defer func() { <-semaphore }()
			s.probeOne(ctx, runID, trigger, t)
		}(t)
	}
	wg.Wait()
}

func (s *Service) probeOne(ctx context.Context, runID, trigger string, t target) {
	variant := s.renderQuestion()
	requestCtx, cancel := context.WithTimeout(ctx, s.options.RequestTimeout)
	defer cancel()
	result := s.tester.TestAccount(requestCtx, t.candidate.ID, TestRequest{
		ModelID:          t.spec.Model,
		Prompt:           variant.Prompt,
		ReasoningEffort:  t.spec.ReasoningEffort,
		ApplyCodexTicket: s.options.ApplyTicket,
		Probe:            true,
	})

	accountType := t.candidate.Type
	if accountType == "" {
		accountType = model.IQAccountOAuth
	}
	probe := Probe{
		RunID:           runID,
		AccountID:       t.candidate.ID,
		AccountName:     t.candidate.Name,
		AccountType:     accountType,
		SampleGroupID:   t.sampleGroup,
		GroupIDs:        t.candidate.GroupIDs,
		Model:           t.spec.Model,
		ReasoningEffort: t.spec.ReasoningEffort,
		QuestionID:      CandyQuestion.ID,
		QuestionVariant: variant.Signature,
		Prompt:          variant.Prompt,
		AnswerExcerpt:   truncateRunes(strings.TrimSpace(result.Text), maxExcerptRunes),
		UpstreamModel:   result.UpstreamModel,
		TicketInjected:  result.TicketInjected,
		DurationMs:      result.Duration.Milliseconds(),
		Trigger:         trigger,
		CreatedAt:       s.options.Now().UTC(),
	}
	if result.UpstreamModel != "" {
		probe.ModelVerdict = s.options.ClassifyModel(t.spec.Model, result.UpstreamModel)
	}
	// 进程退出（重启、升级）打断的测试不入库：那是监控自己停了，不是号出了问题。
	// 单个请求超时（requestCtx 到期、父 ctx 还在）照常记成出错。
	if result.Err != nil && ctx.Err() != nil {
		s.options.Logger.Info("iq probe interrupted by shutdown", "run_id", runID, "account_id", t.candidate.ID)
		return
	}
	if result.Err != nil {
		probe.Outcome = OutcomeError
		probe.ErrorMessage = truncateRunes(result.Err.Error(), 500)
	} else {
		graded := GradeAnswer(result.Text, CandyQuestion.Answer)
		probe.Outcome, probe.AnswerValue = graded.Outcome, graded.Value
		probe.AnswerFormat, probe.AnswerReasoning = graded.Format, graded.Reasoning
	}

	// 入库用独立的 ctx：进程收到停止信号时，已经拿到的答案也要落盘。
	writeCtx, writeCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer writeCancel()
	if _, err := s.store.InsertProbe(writeCtx, probe); err != nil {
		s.options.Logger.Error("iq probe not saved", "account_id", probe.AccountID, "error", err)
		return
	}
	s.options.Logger.Info("iq probe",
		"run_id", runID, "account_id", probe.AccountID, "type", probe.AccountType, "sample_group", probe.SampleGroupID,
		"model", probe.Model, "effort", probe.ReasoningEffort, "variant", probe.QuestionVariant,
		"outcome", probe.Outcome, "answer", probe.AnswerValue, "format", probe.AnswerFormat,
		"upstream_model", probe.UpstreamModel, "duration_ms", probe.DurationMs)
}

// renderQuestion 给这次检测随机生成一个题面（共用 s.rng，所以要加锁）。
func (s *Service) renderQuestion() Variant {
	s.mu.Lock()
	defer s.mu.Unlock()
	return CandyQuestion.Render(s.rng)
}

// ---------- 版本闸 ----------

// gate 判断 sub2api 是否已支持 #816。force=true 时绕过缓存（「立即检测」用，免得刚升级完还要等 10 分钟）。
func (s *Service) gate(ctx context.Context, force bool) model.IQGate {
	s.mu.Lock()
	cached, at, cachedErr := s.version, s.versionAt, s.versionErr
	s.mu.Unlock()
	now := s.options.Now()
	if force || at.IsZero() || now.Sub(at) > versionTTL {
		versionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		version, err := s.tester.Version(versionCtx)
		cancel()
		s.mu.Lock()
		s.versionAt = now
		if err != nil {
			s.versionErr = err.Error()
			// 读失败时保留上一次读到的版本：短暂的网络抖动不应关闸。
		} else {
			s.version, s.versionErr = version, ""
		}
		cached, at, cachedErr = s.version, s.versionAt, s.versionErr
		s.mu.Unlock()
	}
	gate := model.IQGate{Required: s.options.MinVersion, Current: cached, Error: cachedErr}
	if !at.IsZero() {
		checked := at.UTC()
		gate.CheckedAt = &checked
	}
	gate.Open = cached != "" && (s.options.MinVersion == "" || CompareVersions(cached, s.options.MinVersion) >= 0)
	return gate
}

func gateMessage(gate model.IQGate) string {
	if gate.Current == "" {
		if gate.Error != "" {
			return "读取 sub2api 版本失败：" + gate.Error
		}
		return "尚未读取到 sub2api 版本"
	}
	return fmt.Sprintf("sub2api %s 低于 %s，测试接口还不支持自定义题目", gate.Current, gate.Required)
}

// ---------- 设置 ----------

func (s *Service) models(ctx context.Context) ([]ModelSpec, string, error) {
	raw, ok, err := s.store.GetSetting(ctx, settingModels)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return append([]ModelSpec(nil), s.options.DefaultModels...), "default", nil
	}
	specs, err := ParseModelSpecs(raw)
	if err != nil || len(specs) == 0 {
		// 库里的值坏了也不该让检测停摆：退回默认清单，看板上标「默认」。
		return append([]ModelSpec(nil), s.options.DefaultModels...), "default", nil
	}
	return specs, "settings", nil
}

// scheduleConfig 读定时检测配置。没在界面上改过时用默认值（周期取 .env），
// 并沿用旧版的「暂停」开关：升级前暂停着的，升级后仍是关闭。
func (s *Service) scheduleConfig(ctx context.Context) (model.IQScheduleConfig, string) {
	defaults := DefaultSchedule(s.options.Interval, s.options.Jitter)
	if raw, ok, err := s.store.GetSetting(ctx, settingSchedule); err == nil && ok {
		if config, valid := decodeSchedule(raw, defaults); valid {
			return config, "settings"
		}
	}
	if raw, ok, err := s.store.GetSetting(ctx, settingPaused); err == nil && ok && raw == "true" {
		defaults.Enabled = false
	}
	return defaults, "default"
}

func (s *Service) Settings(ctx context.Context) (model.IQSettingsView, error) {
	specs, source, err := s.models(ctx)
	if err != nil {
		return model.IQSettingsView{}, err
	}
	config, scheduleSource := s.scheduleConfig(ctx)
	return model.IQSettingsView{
		Models: specViews(specs), ModelsSource: source,
		Schedule: config, ScheduleSource: scheduleSource, Paused: !config.Enabled,
		Groups: s.groupConfig(ctx),
	}, nil
}

func (s *Service) UpdateSettings(ctx context.Context, request model.IQSettingsRequest) (model.IQSettingsView, error) {
	// 先校验全部字段、再写库：一次请求里任何一项不合法，整个请求都不生效。
	update := model.IQScheduleUpdate{}
	if request.Schedule != nil {
		update = *request.Schedule
	}
	if request.Paused != nil && update.Enabled == nil {
		enabled := !*request.Paused
		update.Enabled = &enabled
	}
	scheduleChanged := update != (model.IQScheduleUpdate{})
	var nextSchedule model.IQScheduleConfig
	if scheduleChanged {
		current, _ := s.scheduleConfig(ctx)
		next, err := ApplyScheduleUpdate(current, update)
		if err != nil {
			return model.IQSettingsView{}, &model.IQError{Status: http.StatusBadRequest, Message: err.Error()}
		}
		nextSchedule = next
	}
	var nextGroups *model.IQGroupConfig
	if request.Groups != nil {
		next, err := s.validateGroupUpdate(ctx, *request.Groups)
		if err != nil {
			return model.IQSettingsView{}, err
		}
		nextGroups = &next
	}
	if request.Models != nil {
		specs, err := ParseModelSpecs(*request.Models)
		if err != nil {
			return model.IQSettingsView{}, &model.IQError{Status: http.StatusBadRequest, Message: err.Error()}
		}
		if len(specs) == 0 {
			return model.IQSettingsView{}, &model.IQError{Status: http.StatusBadRequest, Message: "至少配置一个测试模型"}
		}
		if len(specs) > 8 {
			return model.IQSettingsView{}, &model.IQError{Status: http.StatusBadRequest, Message: "测试模型最多 8 个"}
		}
		if err := s.store.SetSetting(ctx, settingModels, FormatModelSpecs(specs)); err != nil {
			return model.IQSettingsView{}, err
		}
	}
	if nextGroups != nil {
		if err := s.store.SetSetting(ctx, settingGroups, encodeGroups(*nextGroups)); err != nil {
			return model.IQSettingsView{}, err
		}
		s.options.Logger.Info("iq groups updated", "ids", nextGroups.IDs, "per_round", nextGroups.PerRound)
	}
	if scheduleChanged {
		if err := s.store.SetSetting(ctx, settingSchedule, encodeSchedule(nextSchedule)); err != nil {
			return model.IQSettingsView{}, err
		}
		s.options.Logger.Info("iq schedule updated", "enabled", nextSchedule.Enabled,
			"interval_seconds", nextSchedule.IntervalSeconds, "jitter_seconds", nextSchedule.JitterSeconds,
			"sample_mode", nextSchedule.SampleMode, "sample_percent", nextSchedule.SamplePercent,
			"sample_min", nextSchedule.SampleMin, "sample_count", nextSchedule.SampleCount)
		s.notify()
	}
	return s.Settings(ctx)
}

// ---------- 立即检测 ----------

// RunNow 立刻跑一次。不带 account_id = 立即跑一轮常规抽样；带了 = 只测这个号。
// 测试本身在后台跑（xhigh 推理可能要几分钟），接口立刻返回，结果从看板上看。
func (s *Service) RunNow(ctx context.Context, request model.IQRunRequest) (model.IQRunResponse, error) {
	if gate := s.gate(ctx, true); !gate.Open {
		return model.IQRunResponse{}, &model.IQError{Status: http.StatusConflict, Message: gateMessage(gate)}
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return model.IQRunResponse{}, &model.IQError{Status: http.StatusConflict, Message: "上一轮检测还没结束"}
	}
	if since := s.options.Now().Sub(s.lastManualAt); since < manualCooldown {
		s.mu.Unlock()
		return model.IQRunResponse{}, &model.IQError{Status: http.StatusTooManyRequests,
			Message: fmt.Sprintf("操作太频繁，%d 秒后再试", int((manualCooldown-since).Seconds())+1)}
	}
	s.mu.Unlock()

	var (
		targets []target
		kind    = "round"
	)
	switch {
	case request.AccountID > 0:
		kind = "account"
		single, err := s.singleTarget(ctx, request)
		if err != nil {
			return model.IQRunResponse{}, err
		}
		targets = []target{single}
	case request.GroupID > 0:
		kind = "group"
		picked, err := s.groupTargets(ctx, request.GroupID, request.Model)
		if err != nil {
			return model.IQRunResponse{}, err
		}
		targets = picked
	default:
		// 手动跑一轮不看定时开关：定时关了也能按当前的抽样配置临时跑一轮（含分组补测）。
		config, _ := s.scheduleConfig(ctx)
		picked, round, err := s.pickTargets(ctx, config)
		if err != nil {
			return model.IQRunResponse{}, &model.IQError{Status: http.StatusServiceUnavailable, Message: err.Error()}
		}
		if len(picked) == 0 {
			return model.IQRunResponse{}, &model.IQError{Status: http.StatusConflict, Message: "当前没有可用的号"}
		}
		s.recordRound(round)
		targets = picked
	}
	if !s.begin("manual", len(targets)) {
		return model.IQRunResponse{}, &model.IQError{Status: http.StatusConflict, Message: "上一轮检测还没结束"}
	}
	s.mu.Lock()
	s.lastManualAt = s.options.Now()
	s.mu.Unlock()

	runID := newRunID()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		// 不跟请求的 ctx 走：页面关了检测也要跑完。
		s.execute(context.WithoutCancel(ctx), runID, "manual", targets)
	}()
	return model.IQRunResponse{RunID: runID, Kind: kind, Targets: len(targets)}, nil
}

func (s *Service) singleTarget(ctx context.Context, request model.IQRunRequest) (target, error) {
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows, err := s.candidates.ListIQCandidates(listCtx)
	if err != nil {
		return target{}, &model.IQError{Status: http.StatusServiceUnavailable, Message: "读取可用账号失败"}
	}
	var candidate *model.IQCandidate
	for i := range rows {
		if rows[i].ID == request.AccountID {
			candidate = &rows[i]
			break
		}
	}
	if candidate == nil {
		// 只测此刻可用的号：冷却中的号测出来多半是上游报错，还会占它的额度。
		return target{}, &model.IQError{Status: http.StatusConflict, Message: fmt.Sprintf("#%d 当前不可调度（停用、限流或冷却中）", request.AccountID)}
	}
	pick, err := s.specPicker(ctx, request.Model)
	if err != nil {
		return target{}, err
	}
	return target{candidate: *candidate, spec: pick()}, nil
}

// specPicker 返回给每个号分配测试模型的函数：指定了模型就固定用它，否则每次随机挑一个。
func (s *Service) specPicker(ctx context.Context, label string) (func() ModelSpec, error) {
	specs, _, err := s.models(ctx)
	if err != nil || len(specs) == 0 {
		return nil, &model.IQError{Status: http.StatusConflict, Message: "没有配置测试模型"}
	}
	if label != "" {
		for _, item := range specs {
			if item.String() == label || item.Model == label {
				spec := item
				return func() ModelSpec { return spec }, nil
			}
		}
		return nil, &model.IQError{Status: http.StatusBadRequest, Message: "模型不在测试清单里：" + label}
	}
	return func() ModelSpec {
		s.mu.Lock()
		defer s.mu.Unlock()
		return specs[s.rng.IntN(len(specs))]
	}, nil
}

// groupTargets 是分组的「立即检测」：从组内此刻可用的号里抽 GroupPerRound 个（刚测过的优先往后排）。
func (s *Service) groupTargets(ctx context.Context, groupID int64, label string) ([]target, error) {
	pick, err := s.specPicker(ctx, label)
	if err != nil {
		return nil, err
	}
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows, err := s.candidates.ListIQCandidates(listCtx)
	if err != nil {
		return nil, &model.IQError{Status: http.StatusServiceUnavailable, Message: "读取可用账号失败"}
	}
	members := []model.IQCandidate{}
	for _, row := range rows {
		if containsID(row.GroupIDs, groupID) {
			members = append(members, row)
		}
	}
	if len(members) == 0 {
		return nil, &model.IQError{Status: http.StatusConflict, Message: fmt.Sprintf("分组 #%d 当前没有可调度的号", groupID)}
	}
	last, err := s.store.LastProbedAt(ctx)
	if err != nil {
		return nil, err
	}
	config, _ := s.scheduleConfig(ctx)
	groups := s.groupConfig(ctx)
	s.mu.Lock()
	picked := s.sampleFresh(members, groups.PerRound, last, s.options.Now(), time.Duration(config.IntervalSeconds)*time.Second)
	s.mu.Unlock()
	targets := make([]target, 0, len(picked))
	for _, row := range picked {
		targets = append(targets, target{candidate: row, spec: pick(), sampleGroup: groupID})
	}
	return targets, nil
}

func (s *Service) setLastError(message string) {
	s.mu.Lock()
	s.lastError = message
	s.mu.Unlock()
}

// Wait 等后台的「立即检测」跑完（测试和优雅退出用）。
func (s *Service) Wait() { s.wg.Wait() }

// Close 关闭 SQLite。调用前应先停掉 Run 的 ctx 并等它返回。
func (s *Service) Close() error { return s.store.Close() }

func newRunID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b[:])
}

func specViews(specs []ModelSpec) []model.IQModelSpec {
	out := make([]model.IQModelSpec, 0, len(specs))
	for _, s := range specs {
		out = append(out, model.IQModelSpec{Model: s.Model, ReasoningEffort: s.ReasoningEffort, Label: s.String()})
	}
	return out
}
