package iq

import (
	"context"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"gpt-pro-monitor/internal/model"
)

// ---------- 判级 ----------

const (
	LevelHealthy = "healthy"
	LevelWarning = "warning"
	LevelDanger  = "danger"
	LevelUnknown = "unknown"
	LevelBlocked = "blocked"
	LevelPaused  = "paused"
)

func levelLabel(level string) string {
	return map[string]string{
		LevelHealthy: "智商正常",
		LevelWarning: "疑似降智",
		LevelDanger:  "降智",
		LevelUnknown: "样本不足",
		LevelBlocked: "等待 sub2api 支持",
		LevelPaused:  "已暂停",
	}[level]
}

// classify 用最近 RecentWindow 次可判分的结果判级；样本不足 MinSamples 时不下结论。
func (s *Service) classify(stats model.IQStats) string {
	if stats.Graded < s.options.MinSamples || stats.AccuracyPercent == nil {
		return LevelUnknown
	}
	switch accuracy := *stats.AccuracyPercent; {
	case accuracy >= s.options.HealthyPercent:
		return LevelHealthy
	case accuracy >= s.options.WarningPercent:
		return LevelWarning
	default:
		return LevelDanger
	}
}

var levelRank = map[string]int{LevelDanger: 0, LevelWarning: 1, LevelUnknown: 2, LevelHealthy: 3}

// ---------- 统计 ----------

func statsFromTally(t Tally) model.IQStats {
	stats := model.IQStats{
		Graded: t.Graded(), Correct: t.Correct, Wrong: t.Wrong, NoAnswer: t.NoAnswer,
		Errors: t.Errors, Swapped: t.Swapped, JSONFormat: t.JSONFormat,
	}
	if stats.Graded > 0 {
		stats.AccuracyPercent = percent(t.Correct, stats.Graded)
		stats.JSONPercent = percent(t.JSONFormat, stats.Graded)
	}
	if t.DurationN > 0 {
		avg := t.DurationSum / int64(t.DurationN)
		stats.AvgDurationMs = &avg
	}
	return stats
}

func statsFromProbes(probes []Probe) model.IQStats {
	var t Tally
	for _, p := range probes {
		addToTally(&t, p)
	}
	return statsFromTally(t)
}

func addToTally(t *Tally, p Probe) {
	switch p.Outcome {
	case OutcomeCorrect:
		t.Correct++
	case OutcomeWrong:
		t.Wrong++
	case OutcomeNoAnswer:
		t.NoAnswer++
	default:
		t.Errors++
	}
	if p.ModelVerdict == "degraded" {
		t.Swapped++
	}
	if p.AnswerFormat == FormatJSON {
		t.JSONFormat++
	}
	if p.Outcome != OutcomeError {
		t.DurationSum += p.DurationMs
		t.DurationN++
	}
	if p.CreatedAt.After(t.LastAt) {
		t.LastAt = p.CreatedAt
	}
}

func percent(n, d int) *float64 {
	value := math.Round(10000*float64(n)/float64(d)) / 100
	return &value
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	utc := t.UTC()
	return &utc
}

// ---------- 总览 ----------

// Overview 是看板首屏：版本闸、定时状态、题目说明，以及默认口径（最近 N 次、全部题面）的总览矩阵和结论。
func (s *Service) Overview(ctx context.Context) (model.IQOverview, error) {
	specs, source, err := s.models(ctx)
	if err != nil {
		return model.IQOverview{}, err
	}
	gate := s.gate(ctx, false)
	config, configSource := s.scheduleConfig(ctx)
	board, err := s.Board(ctx, WindowRecent, QuestionScopeAll)
	if err != nil {
		return model.IQOverview{}, err
	}

	// 总体判级只看自己的号（号池整体 + 选中的分组），第三方的好坏在结论和矩阵里单独看。
	levels := []string{}
	for _, row := range board.Rows {
		if row.Kind != model.IQSourceProvider {
			levels = append(levels, row.Level)
		}
	}
	overall := worstOf(levels)
	switch {
	case !gate.Open:
		overall = LevelBlocked
	case !config.Enabled:
		overall = LevelPaused
	}

	s.mu.Lock()
	samples := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		samples = append(samples, CandyQuestion.Render(s.rng).Prompt)
	}
	s.mu.Unlock()

	return model.IQOverview{
		GeneratedAt: s.options.Now().UTC(),
		Level:       overall,
		LevelLabel:  levelLabel(overall),
		Gate:        gate,
		Schedule:    s.schedule(ctx, config, configSource),
		Question: model.IQQuestionView{
			ID: CandyQuestion.ID, Prompt: CandyQuestion.Prompt, Answer: CandyQuestion.Answer,
			Randomized: CandyQuestion.Render != nil, Space: CandyVariantSpace(), Samples: samples,
		},
		Models:       specViews(specs),
		ModelsSource: source,
		Groups:       s.groupConfig(ctx),
		Thresholds:   s.thresholds(),
		Board:        board,
	}, nil
}

func (s *Service) thresholds() model.IQThresholds {
	return model.IQThresholds{
		RecentWindow: s.options.RecentWindow, MinSamples: s.options.MinSamples,
		HealthyPercent: s.options.HealthyPercent, WarningPercent: s.options.WarningPercent,
		HistoryPoints: s.options.HistoryPoints,
	}
}

// worstOf 取几个判级里最差的；全都样本不足时为 unknown。
func worstOf(levels []string) string {
	worst, known := LevelHealthy, false
	for _, level := range levels {
		rank, ok := levelRank[level]
		if !ok || level == LevelUnknown {
			continue
		}
		known = true
		if rank < levelRank[worst] {
			worst = level
		}
	}
	if !known {
		return LevelUnknown
	}
	return worst
}

// modelCardFor 给一个来源的一个测试模型出卡片（最近 limit 次）。号池、分组、供应商的卡片结构完全一样。
func (s *Service) modelCardFor(ctx context.Context, source Source, spec ModelSpec, scope string, limit int) (model.IQModelCard, []Probe, error) {
	effort := spec.ReasoningEffort
	probes, err := s.store.RecentProbes(ctx, ProbeFilter{Source: source, Model: spec.Model, Effort: &effort, QuestionIDs: questionIDs(scope), Limit: limit})
	if err != nil {
		return model.IQModelCard{}, nil, err
	}
	card := model.IQModelCard{Spec: specViews([]ModelSpec{spec})[0], Points: make([]model.IQHistoryPoint, 0, len(probes))}
	// probes 是新 → 旧；历史条要旧 → 新。
	for i := len(probes) - 1; i >= 0; i-- {
		p := probes[i]
		card.Points = append(card.Points, model.IQHistoryPoint{
			At: p.CreatedAt, Outcome: p.Outcome, AccountID: p.AccountID, AccountName: p.AccountName,
			Answer: p.AnswerValue, UpstreamModel: p.UpstreamModel, Swapped: p.ModelVerdict == "degraded", DurationMs: p.DurationMs,
			Variant: questionVariantKind(p.QuestionID),
		})
	}
	card.History = statsFromProbes(probes)

	// 判级只看最近 RecentWindow 次「可判分」的结果：出错的不算智商，不该把窗口挤掉。
	// 同一段时间里出错了几次单独数出来，卡片上和正确率并排展示。
	recent := make([]Probe, 0, s.options.RecentWindow)
	recentErrors := 0
	for _, p := range probes {
		if p.Outcome == OutcomeError {
			recentErrors++
			continue
		}
		recent = append(recent, p)
		if len(recent) == s.options.RecentWindow {
			break
		}
	}
	card.Recent = statsFromProbes(recent)
	card.Recent.Errors = recentErrors
	card.Level = s.classify(card.Recent)
	card.LevelLabel = levelLabel(card.Level)

	if len(probes) > 0 {
		card.LastAt = timePtr(probes[0].CreatedAt)
		lastRun := probes[0].RunID
		round := []Probe{}
		for _, p := range probes {
			if p.RunID != lastRun {
				break
			}
			round = append(round, p)
		}
		card.LastRound = statsFromProbes(round)
	}
	return card, probes, nil
}

func (s *Service) schedule(ctx context.Context, config model.IQScheduleConfig, source string) model.IQSchedule {
	groups := s.groupConfig(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := model.IQSchedule{
		IQScheduleConfig: config,
		ConfigSource:     source,
		Paused:           !config.Enabled,
		Concurrency:      s.options.Concurrency,
		Running:          s.running,
		RunningKind:      s.runningKind,
		RunningTargets:   s.runningTargets,
		LastRunAt:        timePtr(s.lastRunAt),
		LastFinishedAt:   timePtr(s.lastFinishedAt),
		SkippedSlots:     s.skippedSlots,
		LastError:        s.lastError,
		Candidates:       s.lastCandidates,
		SampleSize:       SampleSizeFor(config, s.lastCandidates),
		Groups:           len(groups.IDs),
		GroupPerRound:    groups.PerRound,
		LastRoundPool:    s.lastPool,
		LastRoundTopUps:  s.lastTopUps,
	}
	if s.lastRoundTime > 0 {
		seconds := int(s.lastRoundTime.Round(time.Second).Seconds())
		out.LastRoundSeconds = &seconds
	}
	// 只有调度循环按当前配置排好的计划才展示；刚改完配置、循环还没重排的那一瞬间不展示旧计划。
	if config.Enabled && !s.slot.Anchor.IsZero() && s.scheduleKey == scheduleKeyOf(config) {
		out.NextRunAt = timePtr(s.slot.FireAt)
		out.NextAnchorAt = timePtr(s.slot.Anchor)
	}
	return out
}

// ---------- 账号排行 ----------

var windows = map[string]time.Duration{"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour, "all": 0}

// WindowRecent 是「最近 N 次」口径：每个来源、每个模型各取最近 RecentWindow 次可判分的结果。
const WindowRecent = "recent"

// windowSince 把时间窗口换成起点；空串按 7d，all 为零值（全部历史）。
func (s *Service) windowSince(window string) (time.Time, string, error) {
	window = strings.TrimSpace(window)
	if window == "" {
		window = "7d"
	}
	duration, ok := windows[window]
	if !ok {
		return time.Time{}, "", &model.IQError{Status: http.StatusBadRequest, Message: "window 只能是 24h / 7d / 30d / all"}
	}
	if duration == 0 {
		return time.Time{}, window, nil
	}
	return s.options.Now().UTC().Add(-duration), window, nil
}

// Accounts 是账号排行。groupID > 0 时只看检测时属于这个分组的记录。
func (s *Service) Accounts(ctx context.Context, window string, groupID int64) (model.IQAccountsView, error) {
	since, window, err := s.windowSince(window)
	if err != nil {
		return model.IQAccountsView{}, err
	}
	now := s.options.Now().UTC()

	tallies, err := s.store.TallyByAccount(ctx, since, questionIDs(QuestionScopeAll), groupID)
	if err != nil {
		return model.IQAccountsView{}, err
	}

	available := map[int64]bool{}
	availableInGroup := 0
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	if rows, err := s.candidates.ListIQCandidates(listCtx); err == nil {
		for _, row := range rows {
			available[row.ID] = true
			if groupID == 0 || containsID(row.GroupIDs, groupID) {
				availableInGroup++
			}
		}
	}
	cancel()

	type accountAcc struct {
		row   model.IQAccountRow
		tally Tally
	}
	byAccount := map[int64]*accountAcc{}
	var overall Tally
	for _, t := range tallies {
		acc, ok := byAccount[t.AccountID]
		if !ok {
			acc = &accountAcc{row: model.IQAccountRow{AccountID: t.AccountID, AccountName: t.AccountName,
				AccountType: t.AccountType, Available: available[t.AccountID], Groups: []model.IQGroupRef{}}}
			byAccount[t.AccountID] = acc
		}
		mergeTally(&acc.tally, t.Tally)
		mergeTally(&overall, t.Tally)
		acc.row.Models = append(acc.row.Models, model.IQAccountModelStat{
			Model: t.Model, ReasoningEffort: t.ReasoningEffort, LastAt: timePtr(t.LastAt), IQStats: statsFromTally(t.Tally),
		})
	}

	// 最近 10 次的红绿条与最近一次答案：一条查询取窗口内最近的若干条，按号分桶。
	recentSource := AccountsSource
	if groupID > 0 {
		recentSource = GroupSource(groupID)
	}
	recent, err := s.store.RecentProbes(ctx, ProbeFilter{Source: recentSource, QuestionIDs: questionIDs(QuestionScopeAll), Since: since, Limit: 1000})
	if err != nil {
		return model.IQAccountsView{}, err
	}
	for _, p := range recent { // 新 → 旧
		acc, ok := byAccount[p.AccountID]
		if !ok || len(acc.row.Recent) >= 10 {
			continue
		}
		if len(acc.row.Recent) == 0 {
			acc.row.LastAnswer = p.AnswerValue
			acc.row.LastUpstreamModel = p.UpstreamModel
		}
		acc.row.Recent = append(acc.row.Recent, p.Outcome)
	}

	// 每个号当前在哪些分组：方便看出降智的号是哪一批的。读失败就不展示，不影响排行。
	ids := make([]int64, 0, len(byAccount))
	for id := range byAccount {
		ids = append(ids, id)
	}
	if len(ids) > 0 {
		groupsCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		memberships, err := s.candidates.AccountGroups(groupsCtx, ids)
		cancel()
		if err == nil {
			infos, _ := s.groupInfos(ctx)
			for id, groupIDs := range memberships {
				acc := byAccount[id]
				for _, gid := range groupIDs {
					if _, ok := infos[gid]; ok || len(infos) == 0 {
						acc.row.Groups = append(acc.row.Groups, model.IQGroupRef{ID: gid, Name: groupName(infos, gid)})
					}
				}
			}
		}
	}

	rows := make([]model.IQAccountRow, 0, len(byAccount))
	for _, acc := range byAccount {
		acc.row.IQStats = statsFromTally(acc.tally)
		acc.row.LastAt = timePtr(acc.tally.LastAt)
		acc.row.Insufficient = acc.row.Graded < s.options.MinSamples
		reverseStrings(acc.row.Recent) // 旧 → 新，和卡片上的历史条同方向
		sort.Slice(acc.row.Models, func(i, j int) bool {
			a, b := acc.row.Models[i], acc.row.Models[j]
			if a.Model != b.Model {
				return a.Model < b.Model
			}
			return a.ReasoningEffort < b.ReasoningEffort
		})
		rows = append(rows, acc.row)
	}
	// 默认：样本足够的在前，按准确率从低到高（先看最可能降智的号），再按样本数从多到少。
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Insufficient != b.Insufficient {
			return !a.Insufficient
		}
		av, bv := accuracyOf(a.IQStats), accuracyOf(b.IQStats)
		if av != bv {
			return av < bv
		}
		if a.Graded != b.Graded {
			return a.Graded > b.Graded
		}
		return a.AccountID < b.AccountID
	})

	return model.IQAccountsView{
		GeneratedAt: now,
		Window:      window,
		GroupID:     groupID,
		Since:       timePtr(since),
		MinSamples:  s.options.MinSamples,
		Tested:      len(rows),
		Available:   availableInGroup,
		Overall:     statsFromTally(overall),
		Accounts:    rows,
	}, nil
}

func mergeTally(dst *Tally, src Tally) {
	dst.Correct += src.Correct
	dst.Wrong += src.Wrong
	dst.NoAnswer += src.NoAnswer
	dst.Errors += src.Errors
	dst.Swapped += src.Swapped
	dst.JSONFormat += src.JSONFormat
	dst.DurationSum += src.DurationSum
	dst.DurationN += src.DurationN
	if src.LastAt.After(dst.LastAt) {
		dst.LastAt = src.LastAt
	}
}

func accuracyOf(stats model.IQStats) float64 {
	if stats.AccuracyPercent == nil {
		return 101
	}
	return *stats.AccuracyPercent
}

func reverseStrings(values []string) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}

// ---------- 采样详情 ----------

func (s *Service) Probes(ctx context.Context, query model.IQProbeQuery) (model.IQProbesView, error) {
	source, err := ParseSource(query.Source)
	if err != nil {
		return model.IQProbesView{}, &model.IQError{Status: http.StatusBadRequest, Message: err.Error()}
	}
	filter := ProbeFilter{Source: source, AccountID: query.AccountID, QuestionIDs: questionIDs(query.Scope), Limit: query.Limit}
	if filter.Limit <= 0 || filter.Limit > 500 {
		filter.Limit = 180
	}
	switch query.Outcome {
	case "", OutcomeCorrect, OutcomeWrong, OutcomeNoAnswer, OutcomeError:
		filter.Outcome = query.Outcome
	default:
		return model.IQProbesView{}, &model.IQError{Status: http.StatusBadRequest, Message: "outcome 只能是 correct / wrong / no_answer / error"}
	}
	if query.Model != "" {
		modelName, effort, hasEffort := strings.Cut(query.Model, "@")
		filter.Model = strings.TrimSpace(modelName)
		if hasEffort {
			value := strings.ToLower(strings.TrimSpace(effort))
			filter.Effort = &value
		}
	}
	probes, err := s.store.RecentProbes(ctx, filter)
	if err != nil {
		return model.IQProbesView{}, err
	}
	if err := s.store.LoadGroups(ctx, probes); err != nil {
		return model.IQProbesView{}, err
	}
	return model.IQProbesView{GeneratedAt: s.options.Now().UTC(), Probes: probeViews(probes)}, nil
}

func probeViews(probes []Probe) []model.IQProbeView {
	out := make([]model.IQProbeView, 0, len(probes))
	for _, p := range probes {
		groups := p.GroupIDs
		if groups == nil {
			groups = []int64{}
		}
		out = append(out, model.IQProbeView{
			ID: p.ID, ProviderID: p.ProviderID, RunID: p.RunID, AccountID: p.AccountID, AccountName: p.AccountName,
			AccountType: p.AccountType, SampleGroupID: p.SampleGroupID, GroupIDs: groups,
			Variant: questionVariantKind(p.QuestionID), Prompt: p.Prompt,
			Model: p.Model, ReasoningEffort: p.ReasoningEffort, Outcome: p.Outcome,
			AnswerValue: p.AnswerValue, AnswerFormat: p.AnswerFormat, AnswerReasoning: p.AnswerReasoning,
			AnswerExcerpt: p.AnswerExcerpt, UpstreamModel: p.UpstreamModel, ModelVerdict: p.ModelVerdict,
			TicketInjected: p.TicketInjected, ErrorMessage: p.ErrorMessage, DurationMs: p.DurationMs,
			Trigger: p.Trigger, CreatedAt: p.CreatedAt,
		})
	}
	return out
}
