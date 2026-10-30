package iq

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"gpt-pro-monitor/internal/model"
)

// ---------- 供应商列表 ----------

func (s *Service) Providers(ctx context.Context) (model.IQProvidersView, error) {
	if err := s.requireProviders(); err != nil {
		return model.IQProvidersView{}, err
	}
	records, err := s.store.ListProviders(ctx)
	if err != nil {
		return model.IQProvidersView{}, err
	}
	views := make([]model.IQProviderView, 0, len(records))
	for _, record := range records {
		view, err := s.buildProviderView(ctx, record)
		if err != nil {
			return model.IQProvidersView{}, err
		}
		views = append(views, view)
	}
	specs, _, _ := s.models(ctx)
	return model.IQProvidersView{
		GeneratedAt:   s.options.Now().UTC(),
		Providers:     views,
		Limits:        model.IQProviderLimits{MaxProviders: maxProviders, MaxModels: maxProviderModels, MaxSamples: maxSamplesPerModel},
		DefaultModels: FormatModelSpecs(specs),
	}, nil
}

func (s *Service) providerView(ctx context.Context, id int64) (model.IQProviderView, error) {
	record, err := s.store.GetProvider(ctx, id)
	if err != nil {
		return model.IQProviderView{}, &model.IQError{Status: http.StatusNotFound, Message: "供应商不存在"}
	}
	return s.buildProviderView(ctx, record)
}

func (s *Service) buildProviderView(ctx context.Context, record ProviderRecord) (model.IQProviderView, error) {
	specs, _ := ParseModelSpecs(record.Models)
	view := model.IQProviderView{
		ID: record.ID, Name: record.Name, BaseURL: record.BaseURL, Endpoint: EndpointOf(record.BaseURL, record.APIFormat),
		APIFormat: record.APIFormat, APIKeyHint: record.APIKeyHint, Models: specViews(specs),
		Schedule: model.IQProviderSchedule{
			Enabled: record.Enabled, IntervalSeconds: record.IntervalSeconds,
			JitterSeconds: record.JitterSeconds, SamplesPerModel: record.SamplesPerModel,
		},
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
	if parsed, err := url.Parse(record.BaseURL); err == nil {
		view.Host = parsed.Host
		view.Insecure = parsed.Scheme == "http"
	}
	view.Cards = make([]model.IQModelCard, 0, len(specs))
	levels := make([]string, 0, len(specs))
	for _, spec := range specs {
		card, _, err := s.modelCardFor(ctx, ProviderSource(record.ID), spec, QuestionScopeAll, s.options.HistoryPoints)
		if err != nil {
			return model.IQProviderView{}, err
		}
		view.Cards = append(view.Cards, card)
		levels = append(levels, card.Level)
	}
	view.Level = worstOf(levels)
	if !record.Enabled {
		view.Level = LevelPaused
	}
	view.LevelLabel = levelLabel(view.Level)
	view.Status = s.providerStatus(record)
	return view, nil
}

func (s *Service) providerStatus(record ProviderRecord) model.IQProviderStatus {
	runtime := s.providerRuntime(record.ID)
	if runtime == nil {
		return model.IQProviderStatus{}
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	status := model.IQProviderStatus{
		Running: runtime.running, RunningKind: runtime.runningKind, RunningTargets: runtime.runningTargets,
		LastRunAt: timePtr(runtime.lastRunAt), LastFinishedAt: timePtr(runtime.lastFinishedAt),
		SkippedSlots: runtime.skippedSlots, LastError: runtime.lastError,
	}
	if runtime.lastRoundTime > 0 {
		seconds := int(runtime.lastRoundTime.Round(time.Second).Seconds())
		status.LastRoundSeconds = &seconds
	}
	if record.Enabled && !runtime.slot.Anchor.IsZero() && runtime.scheduleKey == providerKey(record) {
		status.NextRunAt = timePtr(runtime.slot.FireAt)
		status.NextAnchorAt = timePtr(runtime.slot.Anchor)
	}
	return status
}
