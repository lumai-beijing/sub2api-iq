package monitor

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"gpt-pro-monitor/internal/db"
	"gpt-pro-monitor/internal/model"
)

// sub2api 的代码内置默认值（openAICodexTicketConfigContext）。settings 表只存「显式配过」的键。
var codexDefaults = map[string]string{
	"openai_codex_ticket_enabled":                         "false",
	"openai_codex_ticket_fail_closed":                     "false",
	"openai_codex_ticket_fail_closed_min_holders":         "2",
	"openai_codex_ticket_fresh_min_seconds":               "300",
	"openai_codex_ticket_target_length":                   "292",
	"openai_codex_ticket_ttl_seconds":                     "3600",
	"openai_codex_ticket_refresh_before_seconds":          "1800",
	"openai_codex_ticket_harvest_probe_interval_seconds":  "6",
	"openai_codex_ticket_harvest_attempt_timeout_seconds": "25",
	"openai_codex_ticket_harvest_concurrency":             "4",
	"openai_codex_ticket_harvest_max_probes_per_cycle":    "16",
	"openai_codex_ticket_harvest_backoff_max_seconds":     "3600",
	"openai_codex_ticket_prefer_holders":                  "true",
	"openai_codex_ticket_harvest_proxy_url":               "",
	"openai_codex_ticket_models":                          "",
}

var codexHotReloadable = map[string]bool{
	"openai_codex_ticket_enabled":                      true,
	"openai_codex_ticket_fail_closed":                  true,
	"openai_codex_ticket_fail_closed_min_holders":      true,
	"openai_codex_ticket_fresh_min_seconds":            true,
	"openai_codex_ticket_refresh_before_seconds":       true,
	"openai_codex_ticket_harvest_backoff_max_seconds":  true,
	"openai_codex_ticket_harvest_concurrency":          true,
	"openai_codex_ticket_harvest_max_probes_per_cycle": true,
	"openai_codex_ticket_prefer_holders":               true,
	"openai_codex_ticket_harvest_proxy_url":            true,
	"openai_codex_ticket_models":                       true,
}

var codexSettingOrder = []string{
	"openai_codex_ticket_enabled",
	"openai_codex_ticket_models",
	"openai_codex_ticket_fail_closed",
	"openai_codex_ticket_fail_closed_min_holders",
	"openai_codex_ticket_fresh_min_seconds",
	"openai_codex_ticket_prefer_holders",
	"openai_codex_ticket_target_length",
	"openai_codex_ticket_ttl_seconds",
	"openai_codex_ticket_refresh_before_seconds",
	"openai_codex_ticket_harvest_probe_interval_seconds",
	"openai_codex_ticket_harvest_attempt_timeout_seconds",
	"openai_codex_ticket_harvest_concurrency",
	"openai_codex_ticket_harvest_max_probes_per_cycle",
	"openai_codex_ticket_harvest_backoff_max_seconds",
	"openai_codex_ticket_harvest_proxy_url",
}

// gatedModels 解析门控清单：settings 里 openai_codex_ticket_models（逗号分隔）非空则用它，
// 否则回落到配置的兜底清单。与 sub2api 的 NormalizeOpenAICodexTicketModelsSetting 同语义。
func (s *CodexService) gatedModels(settings map[string]string) []string {
	if settings != nil {
		if raw, ok := settings["openai_codex_ticket_models"]; ok {
			parsed := parseModelList(raw)
			if len(parsed) > 0 {
				return parsed
			}
		}
	}
	return append([]string(nil), s.options.DefaultModels...)
}

func parseModelList(raw string) []string {
	out := make([]string, 0, 4)
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func (s *CodexService) buildConfig(settings map[string]string) model.CodexConfigView {
	resolve := func(key string) (string, string) {
		if settings != nil {
			if value, ok := settings[key]; ok && strings.TrimSpace(value) != "" {
				return value, "db"
			}
		}
		return codexDefaults[key], "default"
	}

	view := model.CodexConfigView{GatedModels: s.gatedModels(settings), GatedModelsSource: "default"}
	if settings != nil {
		if raw, ok := settings["openai_codex_ticket_models"]; ok && len(parseModelList(raw)) > 0 {
			view.GatedModelsSource = "db"
		}
	}
	view.Values = make([]model.CodexSettingValue, 0, len(codexSettingOrder))
	for _, key := range codexSettingOrder {
		value, source := resolve(key)
		if key == "openai_codex_ticket_models" && source == "default" {
			value = strings.Join(view.GatedModels, ",")
		}
		view.Values = append(view.Values, model.CodexSettingValue{
			Key: key, Value: value, Source: source, HotReload: codexHotReloadable[key],
		})
	}

	boolean := func(key string) bool {
		value, _ := resolve(key)
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		return err == nil && parsed
	}
	number := func(key string) int {
		value, _ := resolve(key)
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			parsed, _ = strconv.Atoi(strings.TrimSpace(codexDefaults[key]))
		}
		return parsed
	}

	view.Enabled = boolean("openai_codex_ticket_enabled")
	view.FailClosed = boolean("openai_codex_ticket_fail_closed")
	view.PreferHolders = boolean("openai_codex_ticket_prefer_holders")
	view.FloorMinHolders = number("openai_codex_ticket_fail_closed_min_holders")
	view.FreshMinSeconds = number("openai_codex_ticket_fresh_min_seconds")
	view.TargetLength = number("openai_codex_ticket_target_length")
	view.TTLSeconds = number("openai_codex_ticket_ttl_seconds")
	view.ProbeInterval = number("openai_codex_ticket_harvest_probe_interval_seconds")
	view.BackoffMaxSeconds = number("openai_codex_ticket_harvest_backoff_max_seconds")
	view.Concurrency = number("openai_codex_ticket_harvest_concurrency")
	view.MaxProbesPerCycle = number("openai_codex_ticket_harvest_max_probes_per_cycle")
	return view
}

// codexTicketBlob 是 accounts.extra 里那张票。state 是路由凭证，解析后立刻丢弃。
type codexTicketBlob struct {
	Model      string    `json:"model"`
	Length     int       `json:"length"`
	Attempts   int       `json:"attempts"`
	CapturedAt time.Time `json:"captured_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

const ticketExtraPrefix = "codex_turn_ticket:"

func parseTicketBlobs(raw string) map[string]codexTicketBlob {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" || raw == "null" {
		return nil
	}
	var byKey map[string]codexTicketBlob
	if err := json.Unmarshal([]byte(raw), &byKey); err != nil {
		return nil
	}
	out := make(map[string]codexTicketBlob, len(byKey))
	for key, blob := range byKey {
		m := strings.TrimPrefix(key, ticketExtraPrefix)
		if m == "" || m == key {
			continue
		}
		out[m] = blob
	}
	return out
}

func (s *CodexService) classify(rows []model.CodexTicketAccountRow, config model.CodexConfigView, now time.Time) []model.CodexTicketAccount {
	accounts := make([]model.CodexTicketAccount, 0, len(rows))
	for _, row := range rows {
		account := model.CodexTicketAccount{
			ID: row.ID, Name: row.Name, Email: row.Email,
			Status: row.Status, Schedulable: row.Schedulable, OptIn: row.OptIn,
			Eligible:              row.OptIn && row.Schedulable && row.Status == "active",
			Groups:                row.Groups,
			Quota5HUsedPercent:    row.Quota5HUsedPercent,
			Quota7DUsedPercent:    row.Quota7DUsedPercent,
			UsageSnapshotAt:       row.UsageSnapshotAt,
			SubscriptionExpiresAt: row.SubscriptionExpiresAt,
			LastUsedAt:            row.LastUsedAt,
		}
		if row.Quota7DUsedPercent != nil {
			remaining := 100 - *row.Quota7DUsedPercent
			account.Quota7DRemaining = &remaining
		}

		blobs := parseTicketBlobs(row.TicketsJSON)
		account.Tickets = make([]model.CodexAccountTicket, 0, len(config.GatedModels))
		for _, m := range config.GatedModels {
			ticket := model.CodexAccountTicket{Model: m, State: model.TicketOff}
			if row.OptIn {
				ticket.State = model.TicketNone
			}
			if blob, ok := blobs[m]; ok {
				ticket.Length = blob.Length
				ticket.Attempts = blob.Attempts
				if !blob.CapturedAt.IsZero() {
					captured := blob.CapturedAt.UTC()
					ticket.CapturedAt = &captured
					ticket.AgeSeconds = int64(now.Sub(captured).Seconds())
				}
				if !blob.ExpiresAt.IsZero() {
					expires := blob.ExpiresAt.UTC()
					ticket.ExpiresAt = &expires
					ticket.RemainingSeconds = int64(expires.Sub(now).Seconds())
				}
				switch {
				case ticket.RemainingSeconds <= 0:
					ticket.State = model.TicketExpired
				case config.TargetLength > 0 && blob.Length != config.TargetLength:
					ticket.State = model.TicketExpired
				case ticket.RemainingSeconds > int64(config.FreshMinSeconds):
					ticket.State = model.TicketFresh
				default:
					ticket.State = model.TicketStale
				}
			}
			account.Tickets = append(account.Tickets, ticket)
		}
		accounts = append(accounts, account)
	}
	return accounts
}

func ticketFor(account model.CodexTicketAccount, m string) *model.CodexAccountTicket {
	for i := range account.Tickets {
		if account.Tickets[i].Model == m {
			return &account.Tickets[i]
		}
	}
	return nil
}

func isHolderState(state string) bool {
	return state == model.TicketFresh || state == model.TicketStale
}

// Overview 组装整块看板。
func (s *CodexService) Overview(ctx context.Context) (model.CodexOverview, error) {
	data, poolStale, err := s.poolData(ctx)
	if err != nil {
		return model.CodexOverview{}, err
	}
	now := s.options.Now().UTC()
	config := s.buildConfig(data.Settings)
	accounts := s.classify(data.Accounts, config, now)

	usage, usageAt, usageStale, usageErr := s.usageData(ctx, config.GatedModels)
	notes := []string{}
	if poolStale {
		notes = append(notes, "票池数据刷新失败，展示的是上一份成功结果")
	}
	if usageStale {
		notes = append(notes, "用量数据刷新失败，展示的是上一份成功结果")
	}
	if usageErr != nil {
		notes = append(notes, "用量数据不可用："+usageErr.Error())
	}
	if data.Settings == nil {
		notes = append(notes, "打票配置读取失败，配置面板显示的是代码内置默认值")
	}

	windowHours := s.options.UsageWindowHours
	windowMinutes := float64(windowHours) * 60
	perModelUsage := splitUsageByModel(usage, config.GatedModels)

	// 逐账号用量并入明细
	for index := range accounts {
		account := &accounts[index]
		for _, m := range config.GatedModels {
			mu := perModelUsage[m]
			counters, ok := mu.byAccount[account.ID]
			if !ok || counters.requests == 0 {
				continue
			}
			item := model.CodexAccountUsage{
				Model: m, Requests: counters.requests, Degraded: counters.degraded, Upgraded: counters.upgraded,
				RPM:         round2(float64(counters.requests) / windowMinutes),
				Concurrency: round2(float64(counters.busyMs) / (windowMinutes * 60000)),
			}
			percent := round2(100 * float64(counters.degraded) / float64(counters.requests))
			item.DegradedPercent = &percent
			account.Usage = append(account.Usage, item)
			account.Requests += counters.requests
			account.Degraded += counters.degraded
		}
	}

	overview := model.CodexOverview{
		GeneratedAt: now,
		Config:      config,
		Population:  buildPopulation(accounts),
		Load:        buildLoad(usage, accounts, windowHours),
		Quota:       s.buildQuota(accounts, now),
		Trend:       s.trendSamples(),
		Accounts:    accounts,
		Freshness: model.CodexFreshness{
			PoolAt: data.At, UsageAt: usageAt,
			PoolTTL:  int(s.options.PoolCacheTTL.Seconds()),
			UsageTTL: int(s.options.UsageCacheTTL.Seconds()),
			Degraded: poolStale || usageStale || usageErr != nil,
			Notes:    notes,
		},
	}
	for _, m := range config.GatedModels {
		overview.Models = append(overview.Models, model.CodexModelView{
			Model:       m,
			Pool:        buildPoolSummary(accounts, m),
			Pools:       s.buildPoolBreakdown(accounts, m, perModelUsage[m], config),
			Harvest:     buildHarvest(accounts, m, config),
			Degradation: buildDegradation(perModelUsage[m], windowHours),
		})
	}
	sortCodexAccounts(overview.Accounts, config.GatedModels)
	overview.Advisories = buildAdvisories(overview)
	return overview, nil
}

func buildPopulation(accounts []model.CodexTicketAccount) model.CodexPopulation {
	pop := model.CodexPopulation{OAuthTotal: len(accounts)}
	for _, account := range accounts {
		if account.OptIn {
			pop.OptIn++
		} else {
			pop.NotOptIn++
		}
		if account.Schedulable && account.Status == "active" {
			pop.Schedulable++
		}
		if account.Eligible {
			pop.Eligible++
		}
	}
	return pop
}

func buildPoolSummary(accounts []model.CodexTicketAccount, m string) model.CodexPoolSummary {
	summary := model.CodexPoolSummary{}
	attemptsSum, attemptsCount := 0, 0
	remainingSum, remainingCount := int64(0), 0
	for _, account := range accounts {
		if !account.Eligible {
			continue
		}
		summary.Eligible++
		ticket := ticketFor(account, m)
		if ticket == nil {
			continue
		}
		switch ticket.State {
		case model.TicketFresh:
			summary.Fresh++
			summary.Holders++
		case model.TicketStale:
			summary.Stale++
			summary.Holders++
		case model.TicketExpired:
			summary.Expired++
		case model.TicketNone:
			summary.Never++
		}
		if isHolderState(ticket.State) {
			if ticket.Attempts > 0 {
				attemptsSum += ticket.Attempts
				attemptsCount++
			}
			if ticket.RemainingSeconds > 0 {
				remainingSum += ticket.RemainingSeconds
				remainingCount++
			}
		}
	}
	if summary.Eligible > 0 {
		summary.CoveragePercent = round2(100 * float64(summary.Holders) / float64(summary.Eligible))
		summary.FreshPercent = round2(100 * float64(summary.Fresh) / float64(summary.Eligible))
	}
	if attemptsCount > 0 {
		summary.AvgAttempts = round2(float64(attemptsSum) / float64(attemptsCount))
	}
	if remainingCount > 0 {
		summary.AvgRemainingSecs = remainingSum / int64(remainingCount)
	}
	return summary
}

func (s *CodexService) buildPoolBreakdown(accounts []model.CodexTicketAccount, m string, usage modelUsage, config model.CodexConfigView) []model.CodexPoolBreakdown {
	wanted := map[int64]bool{}
	for _, id := range s.options.PoolGroupIDs {
		wanted[id] = true
	}
	index := map[int64]*model.CodexPoolBreakdown{}
	order := []int64{}
	for _, account := range accounts {
		for _, group := range account.Groups {
			if len(wanted) > 0 && !wanted[group.ID] {
				continue
			}
			entry, ok := index[group.ID]
			if !ok {
				entry = &model.CodexPoolBreakdown{GroupID: group.ID, Name: group.Name, Floor: config.FloorMinHolders}
				index[group.ID] = entry
				order = append(order, group.ID)
			}
			entry.Members++
			if account.Schedulable && account.Status == "active" {
				entry.Schedulable++
			}
			if account.Eligible {
				entry.Eligible++
				if ticket := ticketFor(account, m); ticket != nil {
					switch ticket.State {
					case model.TicketFresh:
						entry.Fresh++
						entry.Holders++
					case model.TicketStale:
						entry.Stale++
						entry.Holders++
					case model.TicketExpired:
						entry.Expired++
					case model.TicketNone:
						entry.Never++
					}
				}
			}
			if counters, ok := usage.byAccount[account.ID]; ok {
				entry.Requests += counters.requests
				entry.Degraded += counters.degraded
			}
		}
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	result := make([]model.CodexPoolBreakdown, 0, len(order))
	for _, id := range order {
		entry := index[id]
		entry.FloorSatisfied = entry.Floor <= 0 || entry.Holders >= entry.Floor
		if entry.Floor > 0 {
			entry.FloorMargin = round2(float64(entry.Holders) / float64(entry.Floor))
		}
		if entry.Eligible > 0 {
			entry.CoveragePercent = round2(100 * float64(entry.Holders) / float64(entry.Eligible))
		}
		if entry.Requests > 0 {
			entry.DegradedPercent = round2(100 * float64(entry.Degraded) / float64(entry.Requests))
		}
		result = append(result, *entry)
	}
	return result
}

func buildHarvest(accounts []model.CodexTicketAccount, m string, config model.CodexConfigView) model.CodexHarvestSummary {
	summary := model.CodexHarvestSummary{
		WindowMinutes: config.TTLSeconds / 60,
		Note:          "票在账号 extra 里是覆盖写的，采集分布只能看到当前在手票的 captured_at，最多覆盖一个 TTL 窗口。",
	}
	if summary.WindowMinutes <= 0 {
		summary.WindowMinutes = 60
	}
	buckets := map[time.Time]int{}
	attemptsSum, attemptsCount := 0, 0
	for _, account := range accounts {
		ticket := ticketFor(account, m)
		if ticket == nil || ticket.CapturedAt == nil || !isHolderState(ticket.State) {
			continue
		}
		summary.Captured++
		buckets[ticket.CapturedAt.Truncate(time.Minute)]++
		if summary.OldestCapturedAt == nil || ticket.CapturedAt.Before(*summary.OldestCapturedAt) {
			v := *ticket.CapturedAt
			summary.OldestCapturedAt = &v
		}
		if summary.NewestCapturedAt == nil || ticket.CapturedAt.After(*summary.NewestCapturedAt) {
			v := *ticket.CapturedAt
			summary.NewestCapturedAt = &v
		}
		if ticket.Attempts > 0 {
			attemptsSum += ticket.Attempts
			attemptsCount++
		}
	}
	if attemptsCount > 0 {
		summary.AvgAttempts = round2(float64(attemptsSum) / float64(attemptsCount))
	}
	if summary.Captured > 0 {
		summary.CapturePerMinute = round2(float64(summary.Captured) / float64(summary.WindowMinutes))
	}
	if config.ProbeInterval > 0 {
		// 预算是所有门控模型共用的一个计数器，这里给的是整体上限。
		summary.CycleCeiling = round2(60 / float64(config.ProbeInterval) * float64(config.MaxProbesPerCycle))
	}
	keys := make([]time.Time, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })
	for _, key := range keys {
		summary.Buckets = append(summary.Buckets, model.CodexHarvestBucket{Minute: key, Captured: buckets[key]})
	}
	return summary
}

func round2(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return math.Round(value*100) / 100
}

// sortCodexAccounts：最需要关注的排前面——在任一门控模型上「从未拿到 / 已过期」的可调度号，
// 然后按请求量降序。
func sortCodexAccounts(accounts []model.CodexTicketAccount, models []string) {
	rank := func(account model.CodexTicketAccount) int {
		if !account.Eligible {
			return 4
		}
		worst := 3
		for _, ticket := range account.Tickets {
			r := map[string]int{model.TicketNone: 0, model.TicketExpired: 1, model.TicketStale: 2, model.TicketFresh: 3}[ticket.State]
			if r < worst {
				worst = r
			}
		}
		return worst
	}
	sort.SliceStable(accounts, func(i, j int) bool {
		left, right := accounts[i], accounts[j]
		if rank(left) != rank(right) {
			return rank(left) < rank(right)
		}
		if left.Requests != right.Requests {
			return left.Requests > right.Requests
		}
		return left.ID < right.ID
	})
}

// ---------- 用量按模型拆分 + 谱系裁决 ----------

type usageCounters struct {
	requests int
	degraded int
	upgraded int
	unknown  int
	blank    int
	busyMs   int64
}

type modelUsage struct {
	total     usageCounters
	byAccount map[int64]usageCounters
	byHour    map[time.Time]usageCounters
	pairs     map[string]model.CodexResponsePair // response → pair
}

func (c *usageCounters) add(verdict string, counters db.UsageCounters) {
	c.requests += counters.Requests
	c.busyMs += counters.BusyMs
	switch verdict {
	case model.ResponseDegraded:
		c.degraded += counters.Requests
	case model.ResponseUpgraded:
		c.upgraded += counters.Requests
	case model.ResponseUnknown:
		c.unknown += counters.Requests
	case model.ResponseBlank:
		c.blank += counters.Requests
	}
}

func splitUsageByModel(usage db.UsageAggregate, models []string) map[string]modelUsage {
	out := make(map[string]modelUsage, len(models))
	for _, m := range models {
		out[m] = modelUsage{byAccount: map[int64]usageCounters{}, byHour: map[time.Time]usageCounters{}, pairs: map[string]model.CodexResponsePair{}}
	}
	for pair, counters := range usage.Total {
		mu, ok := out[pair.Requested]
		if !ok {
			continue
		}
		verdict := classifyResponseModel(pair.Requested, pair.Response)
		mu.total.add(verdict, counters)
		p := mu.pairs[pair.Response]
		p.Response, p.Verdict = pair.Response, verdict
		p.Requests += counters.Requests
		mu.pairs[pair.Response] = p
		out[pair.Requested] = mu
	}
	for pair, perAccount := range usage.ByAccount {
		mu, ok := out[pair.Requested]
		if !ok {
			continue
		}
		verdict := classifyResponseModel(pair.Requested, pair.Response)
		for id, counters := range perAccount {
			c := mu.byAccount[id]
			c.add(verdict, counters)
			mu.byAccount[id] = c
		}
	}
	for pair, perHour := range usage.ByHour {
		mu, ok := out[pair.Requested]
		if !ok {
			continue
		}
		verdict := classifyResponseModel(pair.Requested, pair.Response)
		for hour, counters := range perHour {
			c := mu.byHour[hour]
			c.add(verdict, counters)
			mu.byHour[hour] = c
		}
	}
	return out
}
