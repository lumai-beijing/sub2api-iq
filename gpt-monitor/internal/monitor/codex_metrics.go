package monitor

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"gpt-pro-monitor/internal/db"
	"gpt-pro-monitor/internal/model"
)

// recentDegradationHours 是「近端」窗口长度：结论区用它判断当前状态，整段窗口只用来看趋势。
const recentDegradationHours = 3

func buildDegradation(usage modelUsage, windowHours int) model.CodexDegradationSummary {
	summary := model.CodexDegradationSummary{
		WindowHours: windowHours,
		RecentHours: recentDegradationHours,
		Requests:    usage.total.requests,
		Degraded:    usage.total.degraded,
		Upgraded:    usage.total.upgraded,
		Unknown:     usage.total.unknown,
		Blank:       usage.total.blank,
	}
	if summary.Requests > 0 {
		summary.DegradedPercent = round2(100 * float64(summary.Degraded) / float64(summary.Requests))
		summary.BlankPercent = round2(100 * float64(summary.Blank) / float64(summary.Requests))
	}
	for _, pair := range usage.pairs {
		summary.Pairs = append(summary.Pairs, pair)
	}
	sort.Slice(summary.Pairs, func(i, j int) bool { return summary.Pairs[i].Requests > summary.Pairs[j].Requests })

	hours := make([]time.Time, 0, len(usage.byHour))
	for hour := range usage.byHour {
		hours = append(hours, hour)
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i].Before(hours[j]) })
	for _, hour := range hours {
		counters := usage.byHour[hour]
		bucket := model.CodexUsageBucket{
			Hour: hour, Requests: counters.requests, Degraded: counters.degraded,
			Upgraded: counters.upgraded, Blank: counters.blank,
			RPM:         round2(float64(counters.requests) / 60),
			Concurrency: round2(float64(counters.busyMs) / 3600000),
		}
		if counters.requests > 0 {
			bucket.DegradedPercent = round2(100 * float64(counters.degraded) / float64(counters.requests))
		}
		summary.Buckets = append(summary.Buckets, bucket)
	}
	for index := len(summary.Buckets) - 1; index >= 0 && len(summary.Buckets)-index <= recentDegradationHours; index-- {
		summary.RecentRequests += summary.Buckets[index].Requests
		summary.RecentDegraded += summary.Buckets[index].Degraded
	}
	if summary.RecentRequests > 0 {
		summary.RecentDegradedPercent = round2(100 * float64(summary.RecentDegraded) / float64(summary.RecentRequests))
	}
	return summary
}

// buildLoad 是与模型无关的总负载：所有门控模型的请求合计。
func buildLoad(usage db.UsageAggregate, accounts []model.CodexTicketAccount, windowHours int) model.CodexLoadSummary {
	windowMinutes := float64(windowHours) * 60
	summary := model.CodexLoadSummary{WindowHours: windowHours}
	var totalBusy int64
	activeAccounts := map[int64]bool{}
	perHour := map[time.Time]db.UsageCounters{}
	for _, counters := range usage.Total {
		summary.Requests += counters.Requests
		totalBusy += counters.BusyMs
	}
	for _, perAccount := range usage.ByAccount {
		for id := range perAccount {
			activeAccounts[id] = true
		}
	}
	for _, hours := range usage.ByHour {
		for hour, counters := range hours {
			c := perHour[hour]
			c.Requests += counters.Requests
			c.BusyMs += counters.BusyMs
			perHour[hour] = c
		}
	}
	summary.ActiveAccounts = len(activeAccounts)
	if windowMinutes > 0 {
		summary.RPM = round2(float64(summary.Requests) / windowMinutes)
		summary.AvgConcurrency = round2(float64(totalBusy) / (windowMinutes * 60000))
	}
	if summary.Requests > 0 {
		summary.AvgDurationMs = totalBusy / int64(summary.Requests)
	}
	for _, counters := range perHour {
		if rpm := round2(float64(counters.Requests) / 60); rpm > summary.PeakRPM {
			summary.PeakRPM = rpm
		}
		if concurrency := round2(float64(counters.BusyMs) / 3600000); concurrency > summary.PeakConcurrency {
			summary.PeakConcurrency = concurrency
		}
	}
	eligible := 0
	for _, account := range accounts {
		if account.Eligible {
			eligible++
		}
	}
	if eligible > 0 {
		summary.PerEligibleRPM = round2(summary.RPM / float64(eligible))
	}
	return summary
}

func (s *CodexService) buildQuota(accounts []model.CodexTicketAccount, now time.Time) model.CodexQuotaSummary {
	summary := model.CodexQuotaSummary{}
	buckets := []struct {
		label string
		lo    float64
		hi    float64
	}{
		{"0-20%", 0, 20}, {"20-40%", 20, 40}, {"40-60%", 40, 60}, {"60-80%", 60, 80}, {"80-100%", 80, 100.0001},
	}
	counts := make([]int, len(buckets))
	sum5H, count5H := 0.0, 0
	sum7D, count7D := 0.0, 0
	for _, account := range accounts {
		if !account.Eligible {
			continue
		}
		stale := account.UsageSnapshotAt == nil ||
			(s.options.UsageStaleAfter > 0 && now.Sub(*account.UsageSnapshotAt) > s.options.UsageStaleAfter)
		if stale {
			summary.SnapshotStale++
		}
		if account.Quota5HUsedPercent != nil {
			sum5H += *account.Quota5HUsedPercent
			count5H++
			if *account.Quota5HUsedPercent >= 80 {
				summary.Accounts5HAbove80++
			}
		}
		if account.Quota7DRemaining != nil {
			sum7D += *account.Quota7DRemaining
			count7D++
			if account.Quota7DUsedPercent != nil && *account.Quota7DUsedPercent >= 80 {
				summary.Accounts7DAbove80++
			}
			for index, bucket := range buckets {
				if *account.Quota7DRemaining >= bucket.lo && *account.Quota7DRemaining < bucket.hi {
					counts[index]++
					break
				}
			}
		}
		switch {
		case account.SubscriptionExpiresAt == nil:
		case !account.SubscriptionExpiresAt.After(now):
			summary.Expired++
		case account.SubscriptionExpiresAt.Before(now.Add(7 * 24 * time.Hour)):
			summary.ExpiringIn7Days++
			summary.ExpiringIn30Days++
		case account.SubscriptionExpiresAt.Before(now.Add(30 * 24 * time.Hour)):
			summary.ExpiringIn30Days++
		}
	}
	if count5H > 0 {
		v := round2(sum5H / float64(count5H))
		summary.Avg5HUsedPercent = &v
	}
	if count7D > 0 {
		remaining := round2(sum7D / float64(count7D))
		summary.Avg7DRemaining = &remaining
		used := round2(100 - remaining)
		summary.Avg7DUsedPercent = &used
	}
	for index, bucket := range buckets {
		summary.Histogram7D = append(summary.Histogram7D, model.CodexQuotaBucket{Label: bucket.label, Count: counts[index]})
	}
	return summary
}

// buildAdvisories 把各维度合成可执行结论：模型相关的按模型各出一份，容量/配置类只出一份。
func buildAdvisories(overview model.CodexOverview) []model.CodexAdvisory {
	advisories := []model.CodexAdvisory{}
	add := func(level, category, m, title, detail, evidence string) {
		advisories = append(advisories, model.CodexAdvisory{Level: level, Category: category, Model: m, Title: title, Detail: detail, Evidence: evidence})
	}
	config := overview.Config

	if !config.Enabled {
		add("critical", "配置", "", "292 打票总开关是关的",
			"账号仍会被正常调度，但不会带 turn-state 头，降智不受任何保护。", "openai_codex_ticket_enabled = false")
		return advisories
	}
	if !config.FailClosed {
		add("warning", "配置", "", "fail_closed 没开，无票号照常接流量",
			"打票仍在跑、持票号也会被优先，但无票号不会被冷却，降智率通常会显著高于开启时。",
			"openai_codex_ticket_fail_closed = false")
	}

	for _, mv := range overview.Models {
		m := mv.Model
		pool := mv.Pool

		if config.FailClosed {
			breached, thin := []string{}, []string{}
			for _, item := range mv.Pools {
				if !item.FloorSatisfied {
					breached = append(breached, fmt.Sprintf("%s %d/%d", item.Name, item.Holders, item.Floor))
				} else if item.FloorMargin > 0 && item.FloorMargin < 1.5 {
					thin = append(thin, fmt.Sprintf("%s %d/%d", item.Name, item.Holders, item.Floor))
				}
			}
			switch {
			case len(breached) > 0 && pool.Holders == 0:
				// 一张票都没有：拦截根本没启动过，是「未起步」不是「跌破」，别喊 critical。
				add("info", "票池", m, "还没有任何持票号，fail_closed 对该模型尚未启动",
					"组内持票数为 0 时地板放行一切请求。刚加进门控清单的模型会经历这个阶段，等采集器采到第一批票后自动进入拦截。",
					"跌破："+strings.Join(breached, "、"))
			case len(breached) > 0:
				add("critical", "票池", m, "持票账号已跌破地板，无票号正在照常接流量",
					"组内持票号少于地板时整组不拦截，降智会立刻回升。请补号或放宽地板。",
					"跌破："+strings.Join(breached, "、"))
			case len(thin) > 0:
				add("warning", "票池", m, "持票账号余量不足地板的 1.5 倍",
					"再掉几个号就会触发放行，建议盯住或补号。", "偏薄："+strings.Join(thin, "、"))
			default:
				add("ok", "票池", m, "持票账号余量充足", "各池持票数都在地板之上。", "")
			}
		}

		if pool.Eligible > 0 {
			switch {
			case pool.CoveragePercent < 20:
				add("critical", "打票", m, "持票覆盖率过低",
					"多数可调度号都没拿到 292 票，打票侧可能被上游限流、代理不通，或该模型的好票本来就少。",
					fmt.Sprintf("覆盖率 %.1f%%（%d/%d）", pool.CoveragePercent, pool.Holders, pool.Eligible))
			case pool.CoveragePercent < 40:
				add("warning", "打票", m, "持票覆盖率偏低",
					"先看 skipped_budget 是否持续 >0：是则调大 harvest_max_probes_per_cycle；否则瓶颈在退避，缩小 harvest_backoff_max_seconds 让失败号更快重试。",
					fmt.Sprintf("覆盖率 %.1f%%（%d/%d）", pool.CoveragePercent, pool.Holders, pool.Eligible))
			}
		}

		d := mv.Degradation
		if d.Requests > 0 {
			current, hours, deg, req := d.RecentDegradedPercent, d.RecentHours, d.RecentDegraded, d.RecentRequests
			if d.RecentRequests < 50 {
				current, hours, deg, req = d.DegradedPercent, d.WindowHours, d.Degraded, d.Requests
			}
			evidence := fmt.Sprintf("近 %dh %.2f%%（%d/%d）", hours, current, deg, req)
			switch {
			case current >= 10:
				add("critical", "降智", m, "降智率偏高",
					"门控模型的请求里有相当比例被上游换成了更差的模型。先看票池余量与地板，再看 fail_closed 是否真的在拦。", evidence)
			case current >= 2:
				add("warning", "降智", m, "降智率高于预期", "机制生效时这个数通常在 1% 以内。", evidence)
			default:
				add("ok", "降智", m, "降智率在预期内", fmt.Sprintf("近 %dh 门控模型请求 %d 笔。", hours, req), evidence)
			}
			if d.RecentRequests >= 50 && d.DegradedPercent-d.RecentDegradedPercent >= 5 {
				add("info", "降智", m, "整段窗口仍混着旧数据",
					fmt.Sprintf("近 %dh 已经降到 %.2f%%，但 %dh 均值还是 %.2f%% —— 旧数据要再过一段时间才滚出窗口，别按均值下结论。",
						d.RecentHours, d.RecentDegradedPercent, d.WindowHours, d.DegradedPercent),
					fmt.Sprintf("%dh 均值 %.2f%% vs 近 %dh %.2f%%", d.WindowHours, d.DegradedPercent, d.RecentHours, d.RecentDegradedPercent))
			}
			if d.Upgraded > 0 {
				add("info", "降智", m, "部分请求被上游升到同系列更高版本，不计入降智",
					"看门狗按谱系放过这类改名（同系列且版本不低于请求模型）。",
					fmt.Sprintf("%d 笔（%.2f%%）", d.Upgraded, 100*float64(d.Upgraded)/float64(d.Requests)))
			}
			if d.BlankPercent >= 5 {
				add("info", "可观测性", m, "部分请求没记到上游返回的模型名",
					"这些请求既不算降智也不算正常，会让降智率被低估。", fmt.Sprintf("%.1f%% 无 upstream_response_model", d.BlankPercent))
			}
		}

		if pool.Eligible > 0 && mv.Harvest.Captured == 0 {
			add("warning", "打票", m, "当前一张在手的票都没有",
				"刚加进门控清单会有这个阶段；持续超过 10 分钟就要查打票代理、账号 token 与上游限流。",
				fmt.Sprintf("可调度 opt-in 号 %d 个", pool.Eligible))
		}
	}

	quota := overview.Quota
	if quota.Avg7DRemaining != nil {
		switch {
		case *quota.Avg7DRemaining < 20:
			add("critical", "容量", "", "7 天额度平均剩余不足两成",
				"这是补号信号：继续跑下去会出现大面积限流，票池也会跟着塌。",
				fmt.Sprintf("平均剩余 %.1f%%，≥80%% 已用的号 %d 个", *quota.Avg7DRemaining, quota.Accounts7DAbove80))
		case *quota.Avg7DRemaining < 40:
			add("warning", "容量", "", "7 天额度平均剩余偏低",
				"按当前流量推进，建议开始准备新号或把流量往剩余多的号上挪。",
				fmt.Sprintf("平均剩余 %.1f%%，≥80%% 已用的号 %d 个", *quota.Avg7DRemaining, quota.Accounts7DAbove80))
		}
	}
	if quota.Expired > 0 || quota.ExpiringIn7Days > 0 {
		add("warning", "续费", "", "有号即将或已经到期", "到期号会退出可调度集合，直接缩小票池分母。",
			fmt.Sprintf("已过期 %d 个，7 天内到期 %d 个", quota.Expired, quota.ExpiringIn7Days))
	}
	if quota.SnapshotStale > 0 {
		add("info", "可观测性", "", "部分账号的额度快照已过期", "额度维度的平均值只覆盖有新鲜快照的号。",
			fmt.Sprintf("%d 个可调度号快照过期", quota.SnapshotStale))
	}

	rank := map[string]int{"critical": 0, "warning": 1, "info": 2, "ok": 3}
	sort.SliceStable(advisories, func(i, j int) bool { return rank[advisories[i].Level] < rank[advisories[j].Level] })
	return advisories
}
