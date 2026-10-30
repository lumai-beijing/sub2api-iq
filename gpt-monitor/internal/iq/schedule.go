package iq

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"gpt-pro-monitor/internal/model"
)

// 定时检测配置的取值范围。周期下限 1 分钟、上限 24 小时；随机范围最多是周期的一半，
// 这样相邻两轮的随机窗口不会重叠，开跑时刻永远保持锚点的先后顺序。
const (
	minIntervalSeconds = 60
	maxIntervalSeconds = 24 * 3600
	maxSamplePercent   = 100
	maxSampleCount     = 200
)

// DefaultSchedule 是没在界面上改过时的配置：沿用 .env 里的周期，抽样 1/10、至少 2 个。
func DefaultSchedule(interval, jitter time.Duration) model.IQScheduleConfig {
	return model.IQScheduleConfig{
		Enabled:         true,
		IntervalSeconds: int(interval.Seconds()),
		JitterSeconds:   int(jitter.Seconds()),
		SampleMode:      model.IQSampleRatio,
		SamplePercent:   10,
		SampleMin:       2,
		SampleCount:     5,
	}
}

// ValidateSchedule 校验一份完整配置，返回给用户看的错误。
func ValidateSchedule(c model.IQScheduleConfig) error {
	if c.IntervalSeconds < minIntervalSeconds || c.IntervalSeconds > maxIntervalSeconds {
		return fmt.Errorf("检测周期需在 1 分钟到 24 小时之间")
	}
	if c.JitterSeconds < 0 || c.JitterSeconds*2 > c.IntervalSeconds {
		return fmt.Errorf("随机范围需在 0 到周期的一半之间（周期 %s 时最多 ±%s）",
			formatSeconds(c.IntervalSeconds), formatSeconds(c.IntervalSeconds/2))
	}
	switch c.SampleMode {
	case model.IQSampleRatio:
		if c.SamplePercent < 1 || c.SamplePercent > maxSamplePercent {
			return fmt.Errorf("抽样比例需在 1%% 到 100%% 之间")
		}
		if c.SampleMin < 1 || c.SampleMin > maxSampleCount {
			return fmt.Errorf("每轮最少个数需在 1 到 %d 之间", maxSampleCount)
		}
	case model.IQSampleCount:
		if c.SampleCount < 1 || c.SampleCount > maxSampleCount {
			return fmt.Errorf("每轮个数需在 1 到 %d 之间", maxSampleCount)
		}
	default:
		return fmt.Errorf("抽样方式只能是按比例（ratio）或按个数（count）")
	}
	return nil
}

// ApplyScheduleUpdate 把局部修改叠到当前配置上，再整体校验。
func ApplyScheduleUpdate(current model.IQScheduleConfig, update model.IQScheduleUpdate) (model.IQScheduleConfig, error) {
	next := current
	if update.Enabled != nil {
		next.Enabled = *update.Enabled
	}
	if update.IntervalSeconds != nil {
		next.IntervalSeconds = *update.IntervalSeconds
	}
	if update.JitterSeconds != nil {
		next.JitterSeconds = *update.JitterSeconds
	}
	if update.SampleMode != nil {
		next.SampleMode = *update.SampleMode
	}
	if update.SamplePercent != nil {
		next.SamplePercent = *update.SamplePercent
	}
	if update.SampleMin != nil {
		next.SampleMin = *update.SampleMin
	}
	if update.SampleCount != nil {
		next.SampleCount = *update.SampleCount
	}
	if err := ValidateSchedule(next); err != nil {
		return current, err
	}
	return next, nil
}

// decodeSchedule 读库里存的配置。缺的字段用默认值补（以后加字段，老数据不用迁移）；
// 坏了或越界则整份退回默认，免得一条坏数据让检测停摆。
func decodeSchedule(raw string, defaults model.IQScheduleConfig) (model.IQScheduleConfig, bool) {
	config := defaults
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return defaults, false
	}
	if ValidateSchedule(config) != nil {
		return defaults, false
	}
	return config, true
}

func encodeSchedule(c model.IQScheduleConfig) string {
	raw, _ := json.Marshal(c)
	return string(raw)
}

// SampleSizeFor 按配置算每轮抽几个，不超过可用总数。
func SampleSizeFor(c model.IQScheduleConfig, available int) int {
	if available <= 0 {
		return 0
	}
	var n int
	if c.SampleMode == model.IQSampleCount {
		n = c.SampleCount
	} else {
		n = int(math.Ceil(float64(available) * float64(c.SamplePercent) / 100))
		if n < c.SampleMin {
			n = c.SampleMin
		}
	}
	return min(max(n, 1), available)
}

// SampleSize 是默认口径：1/10 向上取整、最少 2 个。
func SampleSize(available int) int {
	return SampleSizeFor(model.IQScheduleConfig{SampleMode: model.IQSampleRatio, SamplePercent: 10, SampleMin: 2}, available)
}

// ---------- 锚定周期 ----------

// slotPlan 是一个周期：Anchor 是锚点，FireAt 是这一轮在锚点 ± jitter 内抽到的实际开跑时刻。
type slotPlan struct {
	Anchor time.Time
	FireAt time.Time
}

// anchorOrigin 是锚点的对齐起点：显示时区里一个固定的午夜。周期能整除一天时，锚点就落在当地的整点上
// （5 分钟 = :00 :05 :10…，1 小时 = 每个整点，3 小时 = 北京时间 0/3/6…点）。
// 按 UTC 对齐的话，3 小时周期会落在北京时间 2/5/8 点，不符合直觉。
func anchorOrigin(loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	return time.Date(2000, 1, 1, 0, 0, 0, 0, loc)
}

// firstAnchor 是 origin 之后、对齐到周期整数倍的下一个锚点。重启或改配置后锚点不漂移，
// 每轮在锚点 ±jitter 内随机开跑。锚点离现在不足 jitter 时顺延一个周期，保证随机窗口的前半段不会落在过去。
func firstAnchor(now, origin time.Time, interval, jitter time.Duration) time.Time {
	elapsed := now.Sub(origin)
	anchor := origin.Add((elapsed/interval + 1) * interval)
	if anchor.Sub(now) < jitter {
		anchor = anchor.Add(interval)
	}
	return anchor
}

// planSlot 给锚点抽一个开跑时刻。
func planSlot(anchor time.Time, jitter time.Duration, rng *rand.Rand) slotPlan {
	return slotPlan{Anchor: anchor, FireAt: anchor.Add(NextDelay(0, jitter, rng))}
}

// nextAnchorAfter 返回 previous 之后、随机窗口还没开始关闭的第一个锚点，以及中间跳过了几个周期。
// 上一轮跑太久（比如 xhigh 推理撞上大面积超时）错过了整个窗口时，直接跳到下一个周期，
// 而不是补跑一串积压的轮次。
func nextAnchorAfter(previous, now time.Time, interval, jitter time.Duration) (time.Time, int) {
	anchor := previous.Add(interval)
	skipped := 0
	for !anchor.Add(jitter).After(now) {
		anchor = anchor.Add(interval)
		skipped++
	}
	return anchor, skipped
}

func formatSeconds(seconds int) string {
	switch {
	case seconds%3600 == 0 && seconds >= 3600:
		return fmt.Sprintf("%d 小时", seconds/3600)
	case seconds%60 == 0 && seconds >= 60:
		return fmt.Sprintf("%d 分钟", seconds/60)
	default:
		return fmt.Sprintf("%d 秒", seconds)
	}
}
