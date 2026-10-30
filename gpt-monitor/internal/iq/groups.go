package iq

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"gpt-pro-monitor/internal/model"
)

// 分组检测：测 sub2api 里指定的 OpenAI 分组，分组之间横向对比。
//
// 分组的智商 = 检测时属于这个分组的号的答题结果。号池每轮照常按比例抽号，抽完后给每个选中的分组补测：
// 这一轮里属于该分组的号不到 PerRound 个时，从组内此刻可用的号里补齐（组里有 API Key 号也会测到）。
// 每条记录都存下检测时号所属的分组，号以后换了分组，已有的记录仍算在原来的分组上。
//
// 测的是号本身（经 sub2api 测试接口直连上游），不经过网关的分组级设置（推理强度上限、模型映射等）。
// 147 上目前没有分组用这些设置；要测用户经网关实际拿到的效果，可以把网关地址和该分组的 API Key 加成一个第三方。

const (
	maxGroups        = 30
	maxGroupPerRound = 10
	defaultPerRound  = 2
)

func defaultGroupConfig() model.IQGroupConfig {
	return model.IQGroupConfig{IDs: []int64{}, PerRound: defaultPerRound}
}

// groupConfig 读分组检测配置；没配过或坏了按默认（不测任何分组）。
func (s *Service) groupConfig(ctx context.Context) model.IQGroupConfig {
	raw, ok, err := s.store.GetSetting(ctx, settingGroups)
	if err != nil || !ok {
		return defaultGroupConfig()
	}
	config, valid := decodeGroups(raw)
	if !valid {
		return defaultGroupConfig()
	}
	return config
}

func decodeGroups(raw string) (model.IQGroupConfig, bool) {
	config := defaultGroupConfig()
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return defaultGroupConfig(), false
	}
	if config.IDs == nil {
		config.IDs = []int64{}
	}
	if validateGroupConfig(config) != nil {
		return defaultGroupConfig(), false
	}
	return config, true
}

func encodeGroups(config model.IQGroupConfig) string {
	raw, _ := json.Marshal(config)
	return string(raw)
}

func validateGroupConfig(config model.IQGroupConfig) error {
	if len(config.IDs) > maxGroups {
		return fmt.Errorf("最多同时检测 %d 个分组", maxGroups)
	}
	seen := map[int64]bool{}
	for _, id := range config.IDs {
		if id <= 0 {
			return fmt.Errorf("分组 ID 无效: %d", id)
		}
		if seen[id] {
			return fmt.Errorf("分组 #%d 重复了", id)
		}
		seen[id] = true
	}
	if config.PerRound < 1 || config.PerRound > maxGroupPerRound {
		return fmt.Errorf("每个分组每轮测 1 到 %d 个号", maxGroupPerRound)
	}
	return nil
}

// validateGroupUpdate 把局部修改叠到当前配置上并校验。新加进来的分组必须是 sub2api 里现存的 OpenAI 分组；
// 已经选着、但 sub2api 里删掉了的分组照样允许取消勾选。
func (s *Service) validateGroupUpdate(ctx context.Context, update model.IQGroupUpdate) (model.IQGroupConfig, error) {
	current := s.groupConfig(ctx)
	next := model.IQGroupConfig{IDs: append([]int64(nil), current.IDs...), PerRound: current.PerRound}
	if update.IDs != nil {
		next.IDs = append([]int64{}, (*update.IDs)...)
	}
	if update.PerRound != nil {
		next.PerRound = *update.PerRound
	}
	if err := validateGroupConfig(next); err != nil {
		return model.IQGroupConfig{}, &model.IQError{Status: http.StatusBadRequest, Message: err.Error()}
	}
	added := []int64{}
	for _, id := range next.IDs {
		if !containsID(current.IDs, id) {
			added = append(added, id)
		}
	}
	if len(added) > 0 {
		listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		groups, err := s.candidates.ListIQGroups(listCtx)
		if err != nil {
			return model.IQGroupConfig{}, &model.IQError{Status: http.StatusServiceUnavailable, Message: "读取 sub2api 分组失败，稍后再试"}
		}
		known := map[int64]bool{}
		for _, g := range groups {
			known[g.ID] = true
		}
		for _, id := range added {
			if !known[id] {
				return model.IQGroupConfig{}, &model.IQError{Status: http.StatusBadRequest, Message: fmt.Sprintf("sub2api 里没有 #%d 这个 OpenAI 分组", id)}
			}
		}
	}
	return next, nil
}

// groupInfos 读 sub2api 的分组表（名称、成员、可用数），给看板用。读失败时返回空表和错误，调用方用「#id」代替名称。
func (s *Service) groupInfos(ctx context.Context) (map[int64]model.IQGroupInfo, error) {
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	groups, err := s.candidates.ListIQGroups(listCtx)
	out := make(map[int64]model.IQGroupInfo, len(groups))
	for _, g := range groups {
		out[g.ID] = g
	}
	return out, err
}

func groupName(infos map[int64]model.IQGroupInfo, id int64) string {
	if info, ok := infos[id]; ok && strings.TrimSpace(info.Name) != "" {
		return info.Name
	}
	return fmt.Sprintf("#%d", id)
}

// ---------- 分组列表 ----------

// Groups 列出 sub2api 的全部 OpenAI 分组：成员与可用数、近 24 小时请求数、窗口内的答对率，以及是否在检测。
// 选中但 sub2api 里已经没有的分组也列出来（标 missing），方便取消勾选。
func (s *Service) Groups(ctx context.Context, window string) (model.IQGroupsView, error) {
	since, window, err := s.windowSince(window)
	if err != nil {
		return model.IQGroupsView{}, err
	}
	config := s.groupConfig(ctx)
	view := model.IQGroupsView{
		GeneratedAt: s.options.Now().UTC(), Window: window, Since: timePtr(since), MinSamples: s.options.MinSamples,
		Config: config, Limits: model.IQGroupLimits{MaxGroups: maxGroups, MaxPerRound: maxGroupPerRound},
		Groups: []model.IQGroupRow{},
	}
	infos, listErr := s.groupInfos(ctx)
	if listErr != nil {
		view.Error = "读取 sub2api 分组失败：" + listErr.Error()
	}
	tallies, err := s.store.TallySources(ctx, since, questionIDs(QuestionScopeAll))
	if err != nil {
		return model.IQGroupsView{}, err
	}
	byGroup := map[int64]*Tally{}
	var pool Tally
	for _, t := range tallies {
		switch t.Source.Kind {
		case model.IQSourceGroup:
			if byGroup[t.Source.ID] == nil {
				byGroup[t.Source.ID] = &Tally{}
			}
			mergeTally(byGroup[t.Source.ID], t.Tally)
		case model.IQSourcePool:
			mergeTally(&pool, t.Tally)
		}
	}
	view.Pool = statsFromTally(pool)

	ids := []int64{}
	for id := range infos {
		ids = append(ids, id)
	}
	for _, id := range config.IDs {
		if _, ok := infos[id]; !ok {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		info, known := infos[id]
		row := model.IQGroupRow{
			ID: id, Name: groupName(infos, id), Status: info.Status, Selected: containsID(config.IDs, id),
			Missing: !known && listErr == nil, Members: info.Members, OAuth: info.OAuth, APIKey: info.APIKey,
			Available: info.Available, Requests24h: info.Requests24h,
		}
		if !known {
			row.Requests24h = -1
		}
		if t := byGroup[id]; t != nil {
			row.Stats = statsFromTally(*t)
			row.LastAt = timePtr(t.LastAt)
		}
		row.Insufficient = row.Stats.Graded < s.options.MinSamples
		view.Groups = append(view.Groups, row)
	}
	// 选中的在前；其余按近 24 小时请求数（有客户流量的分组更值得看），再按成员数、ID。
	sort.SliceStable(view.Groups, func(i, j int) bool {
		a, b := view.Groups[i], view.Groups[j]
		if a.Selected != b.Selected {
			return a.Selected
		}
		if a.Selected {
			return indexOf(config.IDs, a.ID) < indexOf(config.IDs, b.ID)
		}
		if a.Requests24h != b.Requests24h {
			return a.Requests24h > b.Requests24h
		}
		if a.Members != b.Members {
			return a.Members > b.Members
		}
		return a.ID < b.ID
	})
	return view, nil
}

func indexOf(ids []int64, id int64) int {
	for i, v := range ids {
		if v == id {
			return i
		}
	}
	return len(ids)
}
