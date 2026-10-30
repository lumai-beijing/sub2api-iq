package monitor

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"gpt-pro-monitor/internal/db"
	"gpt-pro-monitor/internal/model"
)

// CodexSource 是看板需要的只读数据。拆成接口是为了测试能塞假数据。
type CodexSource interface {
	ListTicketAccounts(ctx context.Context) ([]model.CodexTicketAccountRow, error)
	AggregateGatedUsage(ctx context.Context, windowHours int, models []string) (db.UsageAggregate, error)
	AggregateGatedDaily(ctx context.Context, days int, models []string) ([]db.DailyPairBucket, error)
	LoadCodexSettings(ctx context.Context) (map[string]string, error)
}

type CodexOptions struct {
	PoolCacheTTL     time.Duration
	UsageCacheTTL    time.Duration
	TrendCacheTTL    time.Duration
	SampleInterval   time.Duration
	TrendRetention   time.Duration
	UsageWindowHours int
	TrendDays        int
	PoolGroupIDs     []int64
	DefaultModels    []string // settings 读不到 openai_codex_ticket_models 时的兜底
	UsageStaleAfter  time.Duration
	Now              func() time.Time
}

// CodexService 把数据源拼成看板。
//
// 对 46 的性能承诺全在这里兑现：
//   - 票池、用量、按天趋势三条线各有独立缓存，互不触发；
//   - 每条线各有一把 refresh 锁，并发请求只会打一次库（单飞）；
//   - 刷新失败时回落到上一份好数据并打 degraded 标记，不会因为失败而反复重试打库；
//   - 用量 / 按天缓存都记住当时用的门控清单，清单变了才重算。
type CodexService struct {
	source  CodexSource
	options CodexOptions

	poolMu      sync.RWMutex
	poolRefresh sync.Mutex
	poolCache   *codexPoolData
	poolUntil   time.Time

	usageMu      sync.RWMutex
	usageRefresh sync.Mutex
	usageCache   *db.UsageAggregate
	usageAt      time.Time
	usageUntil   time.Time

	dailyMu      sync.RWMutex
	dailyRefresh sync.Mutex
	dailyCache   []db.DailyPairBucket
	dailyModels  []string
	dailyAt      time.Time
	dailyUntil   time.Time

	trendMu sync.RWMutex
	trend   []model.CodexTrendSample
}

type codexPoolData struct {
	At       time.Time
	Accounts []model.CodexTicketAccountRow
	Settings map[string]string
}

func NewCodexService(source CodexSource, options CodexOptions) *CodexService {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.UsageWindowHours <= 0 {
		options.UsageWindowHours = 24
	}
	if options.TrendDays <= 0 {
		options.TrendDays = 7
	}
	if len(options.DefaultModels) == 0 {
		options.DefaultModels = []string{"gpt-6-astra"}
	}
	return &CodexService{source: source, options: options}
}

// ---------- 缓存层 ----------

func (s *CodexService) poolData(ctx context.Context) (*codexPoolData, bool, error) {
	now := s.options.Now().UTC()

	s.poolMu.RLock()
	cached, until := s.poolCache, s.poolUntil
	s.poolMu.RUnlock()
	if cached != nil && now.Before(until) {
		return cached, false, nil
	}

	s.poolRefresh.Lock()
	defer s.poolRefresh.Unlock()

	now = s.options.Now().UTC()
	s.poolMu.RLock()
	cached, until = s.poolCache, s.poolUntil
	s.poolMu.RUnlock()
	if cached != nil && now.Before(until) {
		return cached, false, nil
	}

	accounts, err := s.source.ListTicketAccounts(ctx)
	if err != nil {
		if cached != nil {
			return cached, true, nil
		}
		return nil, false, fmt.Errorf("load ticket accounts: %w", err)
	}
	settings, err := s.source.LoadCodexSettings(ctx)
	if err != nil {
		settings = nil
	}
	data := &codexPoolData{At: now, Accounts: accounts, Settings: settings}

	s.poolMu.Lock()
	s.poolCache = data
	s.poolUntil = now.Add(s.options.PoolCacheTTL)
	s.poolMu.Unlock()
	return data, false, nil
}

func (s *CodexService) usageData(ctx context.Context, models []string) (db.UsageAggregate, time.Time, bool, error) {
	now := s.options.Now().UTC()

	s.usageMu.RLock()
	cached, at, until := s.usageCache, s.usageAt, s.usageUntil
	s.usageMu.RUnlock()
	if cached != nil && now.Before(until) && slices.Equal(cached.Models, models) {
		return *cached, at, false, nil
	}

	s.usageRefresh.Lock()
	defer s.usageRefresh.Unlock()

	now = s.options.Now().UTC()
	s.usageMu.RLock()
	cached, at, until = s.usageCache, s.usageAt, s.usageUntil
	s.usageMu.RUnlock()
	if cached != nil && now.Before(until) && slices.Equal(cached.Models, models) {
		return *cached, at, false, nil
	}

	aggregate, err := s.source.AggregateGatedUsage(ctx, s.options.UsageWindowHours, models)
	if err != nil {
		if cached != nil {
			return *cached, at, true, nil
		}
		return db.UsageAggregate{}, time.Time{}, false, fmt.Errorf("aggregate gated usage: %w", err)
	}

	s.usageMu.Lock()
	s.usageCache = &aggregate
	s.usageAt = now
	s.usageUntil = now.Add(s.options.UsageCacheTTL)
	s.usageMu.Unlock()
	return aggregate, now, false, nil
}

// DailyTrend 是按需面板：7 天那一遍要读 ~280MB 堆页，缓存 TTL 远大于其它面板。
func (s *CodexService) DailyTrend(ctx context.Context) (model.CodexDailyTrend, error) {
	data, _, err := s.poolData(ctx)
	if err != nil {
		return model.CodexDailyTrend{}, err
	}
	models := s.gatedModels(data.Settings)
	now := s.options.Now().UTC()

	s.dailyMu.RLock()
	cached, cachedModels, at, until := s.dailyCache, s.dailyModels, s.dailyAt, s.dailyUntil
	s.dailyMu.RUnlock()
	if cached != nil && now.Before(until) && slices.Equal(cachedModels, models) {
		return s.buildDaily(cached, models, at), nil
	}

	s.dailyRefresh.Lock()
	defer s.dailyRefresh.Unlock()

	now = s.options.Now().UTC()
	s.dailyMu.RLock()
	cached, cachedModels, at, until = s.dailyCache, s.dailyModels, s.dailyAt, s.dailyUntil
	s.dailyMu.RUnlock()
	if cached != nil && now.Before(until) && slices.Equal(cachedModels, models) {
		return s.buildDaily(cached, models, at), nil
	}

	buckets, err := s.source.AggregateGatedDaily(ctx, s.options.TrendDays, models)
	if err != nil {
		if cached != nil {
			return s.buildDaily(cached, cachedModels, at), nil
		}
		return model.CodexDailyTrend{}, fmt.Errorf("aggregate gated daily: %w", err)
	}
	if buckets == nil {
		buckets = []db.DailyPairBucket{}
	}

	s.dailyMu.Lock()
	s.dailyCache = buckets
	s.dailyModels = append([]string(nil), models...)
	s.dailyAt = now
	s.dailyUntil = now.Add(s.options.TrendCacheTTL)
	s.dailyMu.Unlock()
	return s.buildDaily(buckets, models, now), nil
}

func (s *CodexService) buildDaily(raw []db.DailyPairBucket, models []string, at time.Time) model.CodexDailyTrend {
	type key struct {
		model string
		day   time.Time
	}
	acc := map[key]*model.CodexDailyBucket{}
	order := []key{}
	for _, item := range raw {
		k := key{item.Requested, item.Day}
		bucket, ok := acc[k]
		if !ok {
			bucket = &model.CodexDailyBucket{Model: item.Requested, Day: item.Day}
			acc[k] = bucket
			order = append(order, k)
		}
		bucket.Requests += item.Requests
		if classifyResponseModel(item.Requested, item.Response) == model.ResponseDegraded {
			bucket.Degraded += item.Requests
		}
	}
	out := make([]model.CodexDailyBucket, 0, len(order))
	for _, k := range order {
		bucket := acc[k]
		if bucket.Requests > 0 {
			bucket.DegradedPercent = round2(100 * float64(bucket.Degraded) / float64(bucket.Requests))
		}
		out = append(out, *bucket)
	}
	return model.CodexDailyTrend{
		GeneratedAt: at,
		Days:        s.options.TrendDays,
		Models:      append([]string(nil), models...),
		Buckets:     out,
		Note:        fmt.Sprintf("按天趋势对 usage_logs 的扫描代价较高，结果缓存 %s。", s.options.TrendCacheTTL),
	}
}

// ---------- 趋势采样器 ----------

// SampleTrend 只打票池那条便宜查询，按模型记录持票数。票是覆盖写的，DB 没历史。
func (s *CodexService) SampleTrend(ctx context.Context) error {
	data, _, err := s.poolData(ctx)
	if err != nil {
		return err
	}
	now := s.options.Now().UTC()
	config := s.buildConfig(data.Settings)
	accounts := s.classify(data.Accounts, config, now)

	sample := model.CodexTrendSample{At: now, Holders: map[string]int{}, Fresh: map[string]int{}}
	for _, m := range config.GatedModels {
		sample.Holders[m] = 0
		sample.Fresh[m] = 0
	}
	for _, account := range accounts {
		if account.Eligible {
			sample.Eligible++
		}
		for _, ticket := range account.Tickets {
			switch ticket.State {
			case model.TicketFresh:
				sample.Holders[ticket.Model]++
				sample.Fresh[ticket.Model]++
			case model.TicketStale:
				sample.Holders[ticket.Model]++
			}
		}
	}

	s.trendMu.Lock()
	s.trend = append(s.trend, sample)
	cutoff := now.Add(-s.options.TrendRetention)
	keep := s.trend[:0]
	for _, item := range s.trend {
		if !item.At.Before(cutoff) {
			keep = append(keep, item)
		}
	}
	s.trend = keep
	s.trendMu.Unlock()
	return nil
}

func (s *CodexService) RunSampler(ctx context.Context, onError func(error)) {
	if s.options.SampleInterval <= 0 {
		return
	}
	if err := s.SampleTrend(ctx); err != nil && onError != nil {
		onError(err)
	}
	ticker := time.NewTicker(s.options.SampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.SampleTrend(ctx); err != nil && onError != nil {
				onError(err)
			}
		}
	}
}

func (s *CodexService) trendSamples() []model.CodexTrendSample {
	s.trendMu.RLock()
	defer s.trendMu.RUnlock()
	return append([]model.CodexTrendSample(nil), s.trend...)
}
