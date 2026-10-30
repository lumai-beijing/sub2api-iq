package iq

import (
	"math/rand/v2"
	"testing"
	"time"
)

func TestSampleSize(t *testing.T) {
	cases := map[int]int{0: 0, 1: 1, 2: 2, 3: 2, 19: 2, 20: 2, 21: 3, 56: 6, 60: 6, 61: 7, 111: 12}
	for available, want := range cases {
		if got := SampleSize(available); got != want {
			t.Errorf("SampleSize(%d) = %d want %d", available, got, want)
		}
	}
}

func TestSamplePrefersStaleAccountsAndNeverRepeats(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	candidates := make([]Candidate, 0, 20)
	last := map[int64]time.Time{}
	for i := int64(1); i <= 20; i++ {
		candidates = append(candidates, Candidate{ID: i})
		if i > 2 {
			last[i] = now.Add(-5 * time.Minute) // 1、2 号从没测过
		}
	}
	rng := rand.New(rand.NewPCG(1, 2))
	hitsFresh := map[int64]int{}
	const rounds = 2000
	for r := 0; r < rounds; r++ {
		picked := Sample(candidates, 2, last, now, rng)
		if len(picked) != 2 || picked[0].ID == picked[1].ID {
			t.Fatalf("抽样数量或去重错: %+v", picked)
		}
		for _, p := range picked {
			hitsFresh[p.ID]++
		}
	}
	// 从没测过的号权重 25，刚测过的约 1.08；它俩应明显更常被抽中。
	if hitsFresh[1] < rounds/3 || hitsFresh[2] < rounds/3 {
		t.Errorf("从没测过的号应优先: 1号 %d 次，2号 %d 次 / %d 轮", hitsFresh[1], hitsFresh[2], rounds)
	}
	if hitsFresh[10] == 0 {
		t.Errorf("刚测过的号也要有机会被抽中")
	}
	if got := Sample(candidates[:3], 5, last, now, rng); len(got) != 3 {
		t.Errorf("候选不够时应全取: %d", len(got))
	}
}

func TestNextDelayStaysWithinJitter(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	base, jitter := 5*time.Minute, 60*time.Second
	seenLow, seenHigh := false, false
	for i := 0; i < 5000; i++ {
		d := NextDelay(base, jitter, rng)
		if d < base-jitter || d > base+jitter {
			t.Fatalf("越界: %s", d)
		}
		seenLow = seenLow || d < base-50*time.Second
		seenHigh = seenHigh || d > base+50*time.Second
	}
	if !seenLow || !seenHigh {
		t.Errorf("抖动没覆盖两端: low=%v high=%v", seenLow, seenHigh)
	}
	if NextDelay(base, 0, rng) != base {
		t.Errorf("jitter=0 应返回 base")
	}
}

func TestParseModelSpecs(t *testing.T) {
	specs, err := ParseModelSpecs(" gpt-6-astra@XHigh , gpt-5.6-sol@max, gpt-6-astra@xhigh, gpt-6-sol ")
	if err != nil || len(specs) != 3 {
		t.Fatalf("解析错: %+v %v", specs, err)
	}
	if specs[0] != (ModelSpec{Model: "gpt-6-astra", ReasoningEffort: "xhigh"}) || specs[2].ReasoningEffort != "" {
		t.Fatalf("字段错: %+v", specs)
	}
	if FormatModelSpecs(specs) != "gpt-6-astra@xhigh,gpt-5.6-sol@max,gpt-6-sol" {
		t.Fatalf("格式化错: %s", FormatModelSpecs(specs))
	}
	for _, bad := range []string{"gpt-6@turbo", "bad model@high", `x"y`} {
		if _, err := ParseModelSpecs(bad); err == nil {
			t.Errorf("%q 应被拒绝", bad)
		}
	}
}
