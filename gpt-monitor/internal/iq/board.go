package iq

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"gpt-pro-monitor/internal/model"
)

// 总览矩阵：行 = 来源（号池整体、选中的分组、第三方供应商），列 = 测试模型。
// 一眼看出「谁、在哪个模型上、现在答得怎么样」，同一列横向比较。

const (
	// recent 口径下每格取多少条：要凑够 RecentWindow 次可判分，出错多的时候得多取一些。
	boardProbeLimit = 120
	// 每格的迷你历史条画多少次。
	boardStripPoints = 30
	// 随机化前后对比：两边各至少这么多次可判分才比较。
	variantCompareMin = 20
	// 两个来源 / 改版前后的差，至少这么多个百分点、且统计上显著，才写进结论。
	notableGap = 15.0
)

type boardSource struct {
	source  Source
	name    string
	detail  string
	specs   []ModelSpec
	enabled bool
	status  model.IQProviderStatus // 供应商的运行状态；号池和分组共用号池的
	missing bool                   // 分组在 sub2api 里已经没了
	avail   int                    // 分组此刻可用的号；-1 = 不知道
}

// cellNotes 是生成结论时要用、但不进矩阵的信息。
type cellNotes struct {
	topError    string
	topErrorN   int
	wrongAnswer string
	wrongN      int
}

// Board 出总览矩阵。window = recent 时每格是最近 RecentWindow 次可判分的结果（判级口径），并附结论；
// 24h / 7d / 30d / all 时是窗口内的合计，适合长期横向比较。scope 选题面口径（全部 / 固定 / 随机）。
func (s *Service) Board(ctx context.Context, window, scope string) (model.IQBoard, error) {
	window = strings.TrimSpace(window)
	if window == "" {
		window = WindowRecent
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = QuestionScopeAll
	}
	if scope != QuestionScopeAll && scope != QuestionScopeFixed && scope != QuestionScopeRandom {
		return model.IQBoard{}, &model.IQError{Status: http.StatusBadRequest, Message: "scope 只能是 all / fixed / random"}
	}
	var since time.Time
	if window != WindowRecent {
		var err error
		if since, window, err = s.windowSince(window); err != nil {
			return model.IQBoard{}, &model.IQError{Status: http.StatusBadRequest, Message: "window 只能是 recent / 24h / 7d / 30d / all"}
		}
	}
	qids := questionIDs(scope)

	sources, infos, groupsErr, err := s.boardSources(ctx)
	if err != nil {
		return model.IQBoard{}, err
	}
	columns := []ModelSpec{}
	seen := map[string]bool{}
	for _, src := range sources {
		for _, spec := range src.specs {
			if !seen[spec.String()] {
				seen[spec.String()] = true
				columns = append(columns, spec)
			}
		}
	}

	// 时间窗口口径：一次把所有来源聚合出来，按（来源, 模型）合并题目。
	windowTallies := map[string]map[string]Tally{}
	if window != WindowRecent {
		tallies, err := s.store.TallySources(ctx, since, qids)
		if err != nil {
			return model.IQBoard{}, err
		}
		for _, t := range tallies {
			key := t.Source.Key()
			if windowTallies[key] == nil {
				windowTallies[key] = map[string]Tally{}
			}
			label := ModelSpec{Model: t.Model, ReasoningEffort: t.ReasoningEffort}.String()
			merged := windowTallies[key][label]
			mergeTally(&merged, t.Tally)
			windowTallies[key][label] = merged
		}
	}

	board := model.IQBoard{
		GeneratedAt: s.options.Now().UTC(), Window: window, Scope: scope, Since: timePtr(since),
		MinSamples: s.options.MinSamples, Columns: make([]model.IQBoardColumn, 0, len(columns)), Rows: []model.IQBoardRow{},
	}
	for _, spec := range columns {
		board.Columns = append(board.Columns, model.IQBoardColumn{Label: spec.String(), Model: spec.Model, ReasoningEffort: spec.ReasoningEffort})
	}

	notes := map[string]map[int]cellNotes{}
	poolStatus := s.poolRunStatus(ctx)
	for _, src := range sources {
		row := model.IQBoardRow{
			Key: src.source.Key(), Kind: src.source.Kind, ID: src.source.ID, Name: src.name, Detail: src.detail,
			Enabled: src.enabled, Cells: make([]model.IQBoardCell, len(columns)),
		}
		status := poolStatus
		if src.source.Kind == model.IQSourceProvider {
			status = src.status
		}
		row.Running, row.NextRunAt, row.LastRunAt, row.LastError = status.Running, status.NextRunAt, status.LastRunAt, status.LastError
		if !src.enabled {
			row.NextRunAt = nil
		}
		notes[row.Key] = map[int]cellNotes{}

		var total Tally
		configured := specSet(src.specs)
		for i, spec := range columns {
			cell := model.IQBoardCell{Configured: configured[spec.String()], Strip: []model.IQStripPoint{}}
			var tally Tally
			has := false
			if cell.Configured {
				limit := boardStripPoints
				if window == WindowRecent {
					limit = boardProbeLimit
				}
				card, probes, err := s.modelCardFor(ctx, src.source, spec, scope, limit)
				if err != nil {
					return model.IQBoard{}, err
				}
				cell.Strip = stripOf(card.Points, src.source.Kind)
				cell.LastAt = card.LastAt
				if window == WindowRecent {
					recent, errs := s.splitRecent(probes)
					for _, p := range recent {
						addToTally(&tally, p)
					}
					tally.Errors += errs
					has = len(probes) > 0
					notes[row.Key][i] = notesOf(probes)
				}
			}
			if window != WindowRecent {
				if t, ok := windowTallies[row.Key][spec.String()]; ok {
					tally, has = t, true
					cell.LastAt = timePtr(t.LastAt)
				}
			}
			if has {
				cell.Stats = statsFromTally(tally)
				mergeTally(&total, tally)
			}
			cell.Insufficient = cell.Stats.Graded < s.options.MinSamples
			cell.Level = s.classify(cell.Stats)
			cell.LevelLabel = levelLabel(cell.Level)
			row.Cells[i] = cell
		}
		row.Total = model.IQBoardCell{Configured: true, Stats: statsFromTally(total), Strip: []model.IQStripPoint{}, LastAt: timePtr(total.LastAt)}
		row.Total.Insufficient = row.Total.Stats.Graded < s.options.MinSamples
		row.Total.Level = s.classify(row.Total.Stats)
		row.Total.LevelLabel = levelLabel(row.Total.Level)
		levels := []string{}
		for _, cell := range row.Cells {
			if cell.Configured {
				levels = append(levels, cell.Level)
			}
		}
		row.Level = worstOf(levels)
		row.LevelLabel = levelLabel(row.Level)
		board.Rows = append(board.Rows, row)
	}

	for i := range board.Columns {
		cells := make([]*model.IQBoardCell, 0, len(board.Rows))
		for r := range board.Rows {
			cells = append(cells, &board.Rows[r].Cells[i])
		}
		markBoardBest(cells)
		markGapVsPool(cells)
	}
	totals := make([]*model.IQBoardCell, 0, len(board.Rows))
	for r := range board.Rows {
		totals = append(totals, &board.Rows[r].Total)
	}
	markBoardBest(totals)
	markGapVsPool(totals)

	if window == WindowRecent {
		advisories, err := s.boardAdvisories(ctx, board, sources, notes, infos, groupsErr)
		if err != nil {
			return model.IQBoard{}, err
		}
		board.Advisories = advisories
	}
	return board, nil
}

// boardSources 列出矩阵的行：号池整体、选中的分组（按选择顺序）、第三方供应商。
func (s *Service) boardSources(ctx context.Context) ([]boardSource, map[int64]model.IQGroupInfo, error, error) {
	poolSpecs, _, err := s.models(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	config, _ := s.scheduleConfig(ctx)
	groups := s.groupConfig(ctx)

	s.mu.Lock()
	candidates := s.lastCandidates
	s.mu.Unlock()
	if candidates == 0 {
		// 刚启动、还没跑过一轮：现查一次此刻可用的 OAuth 号，免得首屏显示空的。
		candidates = s.countOAuthCandidates(ctx)
	}
	poolDetail := "此刻没有可用的 OAuth 号"
	if candidates > 0 {
		poolDetail = fmt.Sprintf("%d 个可用 OAuth 号 · 每轮抽 %d 个", candidates, SampleSizeFor(config, candidates))
	}
	sources := []boardSource{{source: PoolSource, name: "号池整体", detail: poolDetail, specs: poolSpecs, enabled: config.Enabled, avail: -1}}

	var (
		infos     map[int64]model.IQGroupInfo
		groupsErr error
	)
	if len(groups.IDs) > 0 {
		infos, groupsErr = s.groupInfos(ctx)
	}
	for _, id := range groups.IDs {
		info, known := infos[id]
		src := boardSource{source: GroupSource(id), name: groupName(infos, id), specs: poolSpecs, enabled: config.Enabled, avail: -1}
		switch {
		case known:
			src.avail = info.Available
			src.detail = fmt.Sprintf("可用 %d / %d 个号", info.Available, info.Members)
			if info.APIKey > 0 {
				src.detail += fmt.Sprintf("（含 API Key 号 %d 个）", info.APIKey)
			}
		case groupsErr == nil:
			src.missing = true
			src.detail = "sub2api 里已没有这个分组"
		default:
			src.detail = "读取分组信息失败"
		}
		sources = append(sources, src)
	}

	if s.providers != nil {
		records, err := s.store.ListProviders(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, record := range records {
			specs, _ := ParseModelSpecs(record.Models)
			src := boardSource{source: ProviderSource(record.ID), name: record.Name, specs: specs, enabled: record.Enabled,
				status: s.providerStatus(record), avail: -1}
			if parsed, err := url.Parse(record.BaseURL); err == nil {
				src.detail = parsed.Host
			}
			sources = append(sources, src)
		}
	}
	return sources, infos, groupsErr, nil
}

// countOAuthCandidates 现查此刻可用的 OAuth 号数，并记下来（看板和定时配置的预览都用它）。读失败返回 0。
func (s *Service) countOAuthCandidates(ctx context.Context) int {
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows, err := s.candidates.ListIQCandidates(listCtx)
	if err != nil {
		return 0
	}
	n := 0
	for _, row := range rows {
		if isOAuth(row.Type) {
			n++
		}
	}
	s.mu.Lock()
	if s.lastCandidates == 0 {
		s.lastCandidates = n
	}
	s.mu.Unlock()
	return n
}

// poolRunStatus 是号池（以及跟着号池一起测的分组）的运行状态，与供应商的状态同一个形状。
func (s *Service) poolRunStatus(ctx context.Context) model.IQProviderStatus {
	config, source := s.scheduleConfig(ctx)
	sched := s.schedule(ctx, config, source)
	return model.IQProviderStatus{
		Running: sched.Running, RunningKind: sched.RunningKind, RunningTargets: sched.RunningTargets,
		LastRunAt: sched.LastRunAt, LastFinishedAt: sched.LastFinishedAt, LastRoundSeconds: sched.LastRoundSeconds,
		NextRunAt: sched.NextRunAt, NextAnchorAt: sched.NextAnchorAt, SkippedSlots: sched.SkippedSlots, LastError: sched.LastError,
	}
}

// splitRecent 取最近 RecentWindow 次可判分的结果，以及同一段时间里出错的次数（probes 新 → 旧）。
func (s *Service) splitRecent(probes []Probe) ([]Probe, int) {
	recent := make([]Probe, 0, s.options.RecentWindow)
	errs := 0
	for _, p := range probes {
		if p.Outcome == OutcomeError {
			errs++
			continue
		}
		recent = append(recent, p)
		if len(recent) == s.options.RecentWindow {
			break
		}
	}
	return recent, errs
}

func stripOf(points []model.IQHistoryPoint, kind string) []model.IQStripPoint {
	if len(points) > boardStripPoints {
		points = points[len(points)-boardStripPoints:]
	}
	out := make([]model.IQStripPoint, 0, len(points))
	for _, p := range points {
		point := model.IQStripPoint{At: p.At, Outcome: p.Outcome, Answer: p.Answer, Variant: p.Variant}
		if kind != model.IQSourceProvider {
			point.Who = fmt.Sprintf("#%d %s", p.AccountID, p.AccountName)
		}
		out = append(out, point)
	}
	return out
}

// notesOf 从最近的记录里找出最常见的出错原因和最常见的错误答案（写结论用）。
func notesOf(probes []Probe) cellNotes {
	errors, wrong := map[string]int{}, map[string]int{}
	for i, p := range probes {
		if i >= boardStripPoints*2 {
			break
		}
		switch p.Outcome {
		case OutcomeError:
			errors[truncateRunes(strings.TrimSpace(p.ErrorMessage), 60)]++
		case OutcomeWrong:
			wrong[p.AnswerValue]++
		}
	}
	var out cellNotes
	out.topError, out.topErrorN = mostCommon(errors)
	out.wrongAnswer, out.wrongN = mostCommon(wrong)
	return out
}

func mostCommon(counts map[string]int) (string, int) {
	best, n := "", 0
	for key, count := range counts {
		if count > n || (count == n && key < best) {
			best, n = key, count
		}
	}
	return best, n
}

// markBoardBest 在样本足够的格子里标出答对率最高的（并列都标）；能比较的少于两格时不标。
func markBoardBest(cells []*model.IQBoardCell) {
	best, eligible := -1.0, 0
	for _, cell := range cells {
		if cell.Insufficient || cell.Stats.AccuracyPercent == nil {
			continue
		}
		eligible++
		if *cell.Stats.AccuracyPercent > best {
			best = *cell.Stats.AccuracyPercent
		}
	}
	if eligible < 2 {
		return
	}
	for _, cell := range cells {
		if !cell.Insufficient && cell.Stats.AccuracyPercent != nil && *cell.Stats.AccuracyPercent == best {
			cell.Best = true
		}
	}
}

// markGapVsPool 给每格算与号池整体（第一行）的差。双方样本都够才算。
func markGapVsPool(cells []*model.IQBoardCell) {
	if len(cells) == 0 {
		return
	}
	pool := cells[0]
	if pool.Insufficient || pool.Stats.AccuracyPercent == nil {
		return
	}
	for _, cell := range cells[1:] {
		if cell.Insufficient || cell.Stats.AccuracyPercent == nil {
			continue
		}
		gap := math.Round((*cell.Stats.AccuracyPercent-*pool.Stats.AccuracyPercent)*10) / 10
		cell.GapVsPool = &gap
	}
}

func specSet(specs []ModelSpec) map[string]bool {
	set := map[string]bool{}
	for _, spec := range specs {
		set[spec.String()] = true
	}
	return set
}

// significantGap 判断两个答对率的差是否值得写进结论：至少 notableGap 个百分点，且两比例 z 检验显著（|z| ≥ 2.58）。
func significantGap(c1, n1, c2, n2 int) bool {
	if n1 == 0 || n2 == 0 {
		return false
	}
	p1, p2 := float64(c1)/float64(n1), float64(c2)/float64(n2)
	if math.Abs(p1-p2)*100 < notableGap {
		return false
	}
	pooled := float64(c1+c2) / float64(n1+n2)
	se := math.Sqrt(pooled * (1 - pooled) * (1/float64(n1) + 1/float64(n2)))
	if se == 0 {
		return true
	}
	return math.Abs(p1-p2)/se >= 2.58
}

// ---------- 结论 ----------

var advisoryRank = map[string]int{"critical": 0, "warning": 1, "info": 2, "ok": 3}

// 常见错误答案的含义：29 是这道题最典型的错——没用上「形状靠手感能分辨」，按盲摸算最坏情况。
var knownWrongAnswers = map[string]string{
	"29": "没用上「形状靠手感能分辨」这个条件，按盲摸的最坏情况算出来的",
}

func (s *Service) boardAdvisories(ctx context.Context, board model.IQBoard, sources []boardSource, notes map[string]map[int]cellNotes,
	infos map[int64]model.IQGroupInfo, groupsErr error) ([]model.IQAdvisory, error) {
	out := []model.IQAdvisory{}
	add := func(a model.IQAdvisory) { out = append(out, a) }
	gate := s.gate(ctx, false)
	config, _ := s.scheduleConfig(ctx)
	if !gate.Open {
		add(model.IQAdvisory{Level: "critical", SourceKey: "pool", Source: "号池整体", Title: "号池和分组的检测停了", Detail: gateMessage(gate) + "。第三方供应商不受影响。"})
	}
	if !config.Enabled {
		add(model.IQAdvisory{Level: "info", SourceKey: "pool", Source: "号池整体", Title: "号池定时检测已关闭", Detail: "分组跟着号池一起测，也不会定时检测；可以手动「立即检测」。"})
	}
	if groupsErr != nil {
		add(model.IQAdvisory{Level: "warning", Title: "读不到 sub2api 的分组信息", Detail: "分组的名称和可用号数暂时显示不全，检测照常进行。", Evidence: truncateRunes(groupsErr.Error(), 120)})
	}

	insufficient, better := []string{}, []string{}
	for r, row := range board.Rows {
		src := sources[r]
		label := sourceLabel(row)
		if src.missing {
			add(model.IQAdvisory{Level: "warning", SourceKey: row.Key, Source: label, Title: "这个分组在 sub2api 里已经没有了", Detail: "可以在「分组」里取消勾选。"})
			continue
		}
		if src.source.Kind == model.IQSourceGroup && src.avail == 0 {
			add(model.IQAdvisory{Level: "warning", SourceKey: row.Key, Source: label, Title: "分组里没有可调度的号，这一轮测不到",
				Detail: "组里的号都停用、限流或在冷却中。恢复后会自动接着测。"})
		}
		if src.source.Kind == model.IQSourceProvider && row.LastError != "" && src.enabled {
			add(model.IQAdvisory{Level: "warning", SourceKey: row.Key, Source: label, Title: "上一轮检测没跑成",
				Detail: "通常是 Key、余额或接口地址的问题，在「第三方」里点「测试连接」排查。", Evidence: truncateRunes(row.LastError, 120)})
		}

		// 一个来源的几个模型合成一条结论：同一个来源两个模型都降智，只说一次。
		var danger, warn, worse []int
		var betterGaps []float64
		errorHeavy := []string{}
		wrong := map[string]int{}
		for i, cell := range row.Cells {
			if !cell.Configured {
				continue
			}
			note := notes[row.Key][i]
			stats := cell.Stats
			if attempts := stats.Graded + stats.Errors; attempts >= s.options.MinSamples && stats.Errors*10 >= attempts*3 {
				evidence := fmt.Sprintf("%s 最近 %d 次出错 %d 次", board.Columns[i].Label, attempts, stats.Errors)
				if note.topError != "" {
					evidence += fmt.Sprintf("（多为「%s」）", note.topError)
				}
				errorHeavy = append(errorHeavy, evidence)
			}
			if cell.Insufficient {
				insufficient = append(insufficient, fmt.Sprintf("%s · %s（%d/%d）", label, board.Columns[i].Label, stats.Graded, s.options.MinSamples))
				continue
			}
			if note.wrongAnswer != "" {
				wrong[note.wrongAnswer] += note.wrongN
			}
			switch cell.Level {
			case LevelDanger:
				danger = append(danger, i)
			case LevelWarning:
				warn = append(warn, i)
			default:
				if cell.GapVsPool == nil || row.Kind == model.IQSourcePool || math.Abs(*cell.GapVsPool) < notableGap {
					continue
				}
				pool := board.Rows[0].Cells[i].Stats
				if !significantGap(stats.Correct, stats.Graded, pool.Correct, pool.Graded) {
					continue
				}
				if *cell.GapVsPool > 0 {
					betterGaps = append(betterGaps, *cell.GapVsPool)
				} else {
					worse = append(worse, i)
				}
			}
		}
		low := append(append([]int{}, danger...), warn...)
		switch {
		case len(danger) > 0:
			add(s.cellAdvisory("critical", row, label, board.Columns, low, "降智：多数题答错", wrongAnswerDetail(wrong)))
		case len(warn) > 0:
			add(s.cellAdvisory("warning", row, label, board.Columns, low, "疑似降智：答对率偏低", wrongAnswerDetail(wrong)))
		}
		if len(worse) > 0 {
			add(s.cellAdvisory("warning", row, label, board.Columns, worse, "明显比号池整体答得差", "同一道题、同一个模型和推理强度，差距在统计上显著。"))
		}
		if len(errorHeavy) > 0 {
			add(model.IQAdvisory{Level: "warning", SourceKey: row.Key, Source: label, Title: "出错太多，答对率参考价值有限",
				Detail: "出错（限流、超时、上游报错）不计入答对率，但会让样本变少。", Evidence: strings.Join(errorHeavy, "；")})
		}
		if len(betterGaps) > 0 && len(low) == 0 {
			better = append(better, fmt.Sprintf("%s（%s）", label, gapRange(betterGaps)))
		}
	}
	if len(better) > 0 {
		add(model.IQAdvisory{Level: "info", Title: "这些来源明显比号池整体答得好",
			Detail:   "同一道题、同一个模型和推理强度，差距在统计上显著。号池整体偏低时，可以对照看看是哪些号、哪个分组拖了后腿。",
			Evidence: strings.Join(limitStrings(better, 4), "；")})
	}

	variantAdvisories, err := s.variantAdvisories(ctx, board)
	if err != nil {
		return nil, err
	}
	out = append(out, variantAdvisories...)

	if len(insufficient) > 0 {
		add(model.IQAdvisory{Level: "info", Title: fmt.Sprintf("%d 处样本不足，暂不下结论", len(insufficient)),
			Detail: fmt.Sprintf("可判分的结果少于 %d 次时不判级，再跑几轮就有了。", s.options.MinSamples), Evidence: strings.Join(limitStrings(insufficient, 4), "；")})
	}
	alarming := false
	for _, a := range out {
		if a.Level == "critical" || a.Level == "warning" {
			alarming = true
			break
		}
	}
	if !alarming && len(board.Rows) > 0 {
		add(model.IQAdvisory{Level: "ok", Title: "各来源最近答题正常", Detail: fmt.Sprintf("按最近 %d 次可判分的结果，没有降智或明显偏低的来源。", s.options.RecentWindow)})
	}
	sort.SliceStable(out, func(i, j int) bool { return advisoryRank[out[i].Level] < advisoryRank[out[j].Level] })
	return out, nil
}

func sourceLabel(row model.IQBoardRow) string {
	switch row.Kind {
	case model.IQSourceGroup:
		return "分组 " + row.Name
	case model.IQSourceProvider:
		return "第三方 " + row.Name
	default:
		return row.Name
	}
}

// cellAdvisory 把一个来源的几格合成一条结论：证据逐个列出，点开时定位到答得最差的那一格。
// 只涉及一个模型时在标签上写出模型名；涉及多个时模型名都在证据里。
func (s *Service) cellAdvisory(level string, row model.IQBoardRow, label string, columns []model.IQBoardColumn, cols []int, title, detail string) model.IQAdvisory {
	worst := cols[0]
	parts := make([]string, 0, len(cols))
	for _, i := range cols {
		stats := row.Cells[i].Stats
		if accuracyOf(stats) < accuracyOf(row.Cells[worst].Stats) {
			worst = i
		}
		part := fmt.Sprintf("%s %s（%d/%d", columns[i].Label, formatPercent(stats.AccuracyPercent), stats.Correct, stats.Graded)
		if gap := row.Cells[i].GapVsPool; gap != nil && row.Kind != model.IQSourcePool {
			part += "，比号池" + formatGap(*gap)
		}
		parts = append(parts, part+"）")
	}
	advisory := model.IQAdvisory{Level: level, SourceKey: row.Key, Source: label, FocusModel: columns[worst].Label, Title: title, Detail: detail,
		Evidence: "最近：" + strings.Join(parts, "；")}
	if len(cols) == 1 {
		advisory.Model = columns[worst].Label
	}
	return advisory
}

// wrongAnswerDetail 说出最常见的错答，常见错答顺带解释它错在哪。
func wrongAnswerDetail(wrong map[string]int) string {
	answer, n := mostCommon(wrong)
	if answer == "" {
		return ""
	}
	detail := fmt.Sprintf("最常见的错答是 %s（%d 次）", answer, n)
	if meaning, ok := knownWrongAnswers[answer]; ok {
		detail += "，" + meaning
	}
	return detail + "。"
}

func gapRange(gaps []float64) string {
	lo, hi := gaps[0], gaps[0]
	for _, g := range gaps {
		lo, hi = math.Min(lo, g), math.Max(hi, g)
	}
	if math.Round(lo) == math.Round(hi) {
		return fmt.Sprintf("高 %.0f 个百分点", hi)
	}
	return fmt.Sprintf("高 %.0f～%.0f 个百分点", lo, hi)
}

// variantAdvisories 对比题面随机化前后的答对率：随机化之后明显下降，说明之前的高分可能来自问答缓存
// 或针对这道题的特殊处理。两边各至少 variantCompareMin 次才比较；还不够时给一条进度提示。
func (s *Service) variantAdvisories(ctx context.Context, board model.IQBoard) ([]model.IQAdvisory, error) {
	tallies, err := s.store.TallySources(ctx, time.Time{}, questionIDs(QuestionScopeAll))
	if err != nil {
		return nil, err
	}
	type pair struct{ fixed, random Tally }
	byKey := map[string]map[string]*pair{}
	for _, t := range tallies {
		key := t.Source.Key()
		label := ModelSpec{Model: t.Model, ReasoningEffort: t.ReasoningEffort}.String()
		if byKey[key] == nil {
			byKey[key] = map[string]*pair{}
		}
		if byKey[key][label] == nil {
			byKey[key][label] = &pair{}
		}
		if t.QuestionID == fixedCandyQuestionID {
			mergeTally(&byKey[key][label].fixed, t.Tally)
		} else {
			mergeTally(&byKey[key][label].random, t.Tally)
		}
	}
	out := []model.IQAdvisory{}
	steady, waiting := []string{}, 0
	for _, row := range board.Rows {
		drops, focus := []string{}, ""
		stable := 0
		for i, column := range board.Columns {
			if !row.Cells[i].Configured {
				continue
			}
			p := byKey[row.Key][column.Label]
			if p == nil || p.fixed.Graded() < variantCompareMin {
				continue // 随机化之前没怎么测过的来源，没什么可比的
			}
			if p.random.Graded() < variantCompareMin {
				waiting++
				continue
			}
			before := percent(p.fixed.Correct, p.fixed.Graded())
			after := percent(p.random.Correct, p.random.Graded())
			if *before-*after >= notableGap && significantGap(p.fixed.Correct, p.fixed.Graded(), p.random.Correct, p.random.Graded()) {
				drops = append(drops, fmt.Sprintf("%s 固定题面 %s（%d/%d）→ 随机题面 %s（%d/%d）", column.Label,
					formatPercent(before), p.fixed.Correct, p.fixed.Graded(), formatPercent(after), p.random.Correct, p.random.Graded()))
				if focus == "" {
					focus = column.Label
				}
				continue
			}
			stable++
		}
		if len(drops) > 0 {
			advisory := model.IQAdvisory{Level: "warning", SourceKey: row.Key, Source: sourceLabel(row), FocusModel: focus,
				Title:    "题面随机化后答对率明显下降",
				Detail:   "固定题面时期的高分可能来自问答缓存，或对这道题做了特殊处理（比如换更强的模型来答）。以随机题面的结果为准。",
				Evidence: strings.Join(drops, "；")}
			if len(drops) == 1 {
				advisory.Model = focus
			}
			out = append(out, advisory)
		} else if stable > 0 {
			steady = append(steady, sourceLabel(row))
		}
	}
	if len(steady) > 0 {
		out = append(out, model.IQAdvisory{Level: "ok", Title: "题面随机化前后答对率没有明显变化",
			Detail: "没有看到问答缓存的迹象。", Evidence: strings.Join(limitStrings(steady, 4), "、")})
	}
	if waiting > 0 {
		out = append(out, model.IQAdvisory{Level: "info", Title: "题面已改为每次随机改写",
			Detail:   fmt.Sprintf("条件、数字和答案（%d）都不变，只换说法。每处积累 %d 次随机题面的结果后，会自动对比改版前后的答对率，看有没有缓存迹象。", CandyQuestion.Answer, variantCompareMin),
			Evidence: fmt.Sprintf("还有 %d 处在积累样本", waiting)})
	}
	return out, nil
}

func formatPercent(value *float64) string {
	if value == nil {
		return "—"
	}
	return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", *value), "0"), ".") + "%"
}

func formatGap(gap float64) string {
	if gap >= 0 {
		return fmt.Sprintf("高 %.0f 个百分点", gap)
	}
	return fmt.Sprintf("低 %.0f 个百分点", -gap)
}

func limitStrings(items []string, n int) []string {
	if len(items) <= n {
		return items
	}
	return append(append([]string(nil), items[:n]...), fmt.Sprintf("等 %d 处", len(items)))
}

// ---------- 单格详情 ----------

// Card 是矩阵里一格的详情：完整历史条（最近 HistoryPoints 次）和最近 20 次的作答。
func (s *Service) Card(ctx context.Context, sourceKey, modelLabel, scope string) (model.IQCardView, error) {
	source, err := ParseSource(sourceKey)
	if err != nil || source.Kind == model.IQSourceAccounts {
		return model.IQCardView{}, &model.IQError{Status: http.StatusBadRequest, Message: "来源无效"}
	}
	specs, err := ParseModelSpecs(modelLabel)
	if err != nil || len(specs) != 1 {
		return model.IQCardView{}, &model.IQError{Status: http.StatusBadRequest, Message: "模型无效"}
	}
	if scope == "" {
		scope = QuestionScopeAll
	}
	view := model.IQCardView{GeneratedAt: s.options.Now().UTC(), SourceKey: source.Key(), Kind: source.Kind, Scope: scope}
	switch source.Kind {
	case model.IQSourcePool:
		view.SourceName = "号池整体"
	case model.IQSourceGroup:
		infos, _ := s.groupInfos(ctx)
		view.SourceName = "分组 " + groupName(infos, source.ID)
	case model.IQSourceProvider:
		record, err := s.store.GetProvider(ctx, source.ID)
		if err != nil {
			return model.IQCardView{}, &model.IQError{Status: http.StatusNotFound, Message: "供应商不存在"}
		}
		view.SourceName = record.Name
	}
	card, probes, err := s.modelCardFor(ctx, source, specs[0], scope, s.options.HistoryPoints)
	if err != nil {
		return model.IQCardView{}, err
	}
	view.Card = card
	if len(probes) > 20 {
		probes = probes[:20]
	}
	if err := s.store.LoadGroups(ctx, probes); err != nil {
		return model.IQCardView{}, err
	}
	view.Probes = probeViews(probes)
	return view, nil
}
