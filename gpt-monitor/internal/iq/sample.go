package iq

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
	"time"
)

// Candidate 是一个可被抽中的号。
type Candidate struct {
	ID   int64
	Name string
}

// Sample 从候选里抽 n 个，越久没测过的号权重越高（从没测过的最优先）。
// 纯随机抽样下，60 个号每轮抽 6 个，某个号连续 50 轮（4 小时）抽不到的概率约 0.5%，
// 加权后每个号的被测间隔更均匀，长期准确率的样本数不会差太多。
func Sample(candidates []Candidate, n int, lastProbed map[int64]time.Time, now time.Time, rng *rand.Rand) []Candidate {
	if n <= 0 || len(candidates) == 0 {
		return nil
	}
	if n >= len(candidates) {
		out := append([]Candidate(nil), candidates...)
		rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
		return out
	}
	// Efraimidis–Spirakis 加权无放回抽样：key = u^(1/w)，取 key 最大的 n 个。
	type keyed struct {
		candidate Candidate
		key       float64
	}
	items := make([]keyed, 0, len(candidates))
	for _, c := range candidates {
		items = append(items, keyed{candidate: c, key: math.Pow(rng.Float64(), 1/sampleWeight(lastProbed[c.ID], now))})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].key > items[j].key })
	out := make([]Candidate, 0, n)
	for _, item := range items[:n] {
		out = append(out, item.candidate)
	}
	return out
}

// sampleWeight：从没测过 = 24 小时没测；权重随距上次的小时数线性增长，封顶 24 小时。
func sampleWeight(last time.Time, now time.Time) float64 {
	hours := 24.0
	if !last.IsZero() {
		hours = math.Min(math.Max(now.Sub(last).Hours(), 0), 24)
	}
	return 1 + hours
}

// NextDelay 是 base ± jitter 内的均匀随机值。
func NextDelay(base, jitter time.Duration, rng *rand.Rand) time.Duration {
	if jitter <= 0 {
		return base
	}
	offset := time.Duration(rng.Int64N(int64(2*jitter)+1)) - jitter
	return base + offset
}

// ModelSpec 是一个测试模型及其推理强度，例如 gpt-6-astra@xhigh。
type ModelSpec struct {
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
}

func (m ModelSpec) String() string {
	if m.ReasoningEffort == "" {
		return m.Model
	}
	return m.Model + "@" + m.ReasoningEffort
}

var reasoningEfforts = map[string]bool{"": true, "none": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}

// ParseModelSpecs 解析 "gpt-6-astra@xhigh, gpt-5.6-sol@max"。去重、校验推理强度。
func ParseModelSpecs(raw string) ([]ModelSpec, error) {
	out := []ModelSpec{}
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		model, effort, _ := strings.Cut(part, "@")
		spec := ModelSpec{Model: strings.TrimSpace(model), ReasoningEffort: strings.ToLower(strings.TrimSpace(effort))}
		if err := spec.Validate(); err != nil {
			return nil, err
		}
		if seen[spec.String()] {
			continue
		}
		seen[spec.String()] = true
		out = append(out, spec)
	}
	return out, nil
}

func (m ModelSpec) Validate() error {
	if m.Model == "" || len(m.Model) > 64 || strings.ContainsAny(m.Model, " \t\r\n\"'\\/") {
		return fmt.Errorf("模型名无效: %q", m.Model)
	}
	if !reasoningEfforts[m.ReasoningEffort] {
		return fmt.Errorf("推理强度无效: %q（可选 none/low/medium/high/xhigh/max）", m.ReasoningEffort)
	}
	return nil
}

func FormatModelSpecs(specs []ModelSpec) string {
	parts := make([]string, 0, len(specs))
	for _, s := range specs {
		parts = append(parts, s.String())
	}
	return strings.Join(parts, ",")
}
