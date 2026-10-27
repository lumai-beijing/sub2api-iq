package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func stubRandomIntervalSeconds(t *testing.T, fn func(int64) int64) {
	t.Helper()
	original := randomIntervalSeconds
	randomIntervalSeconds = fn
	t.Cleanup(func() { randomIntervalSeconds = original })
}

func TestComputeNextRunRandomIntervalBounds(t *testing.T) {
	from := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	t.Run("minimum", func(t *testing.T) {
		stubRandomIntervalSeconds(t, func(int64) int64 { return 0 })
		next, err := computeNextRun("@random 10-15", from)
		require.NoError(t, err)
		require.Equal(t, from.Add(10*time.Minute), next)
	})
	t.Run("maximum", func(t *testing.T) {
		stubRandomIntervalSeconds(t, func(n int64) int64 { return n - 1 })
		next, err := computeNextRun("@random 10-15", from)
		require.NoError(t, err)
		require.Equal(t, from.Add(15*time.Minute), next)
	})
}

func TestComputeNextRunRandomIntervalStaysInsideRange(t *testing.T) {
	from := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	seen := map[time.Time]bool{}
	for i := 0; i < 200; i++ {
		next, err := computeNextRun("@random 10-15", from)
		require.NoError(t, err)
		require.False(t, next.Before(from.Add(10*time.Minute)))
		require.False(t, next.After(from.Add(15*time.Minute)))
		require.Zero(t, next.Nanosecond())
		seen[next] = true
	}
	require.Greater(t, len(seen), 1)
}

func TestComputeNextRunRandomIntervalFixedAndWhitespace(t *testing.T) {
	from := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	stubRandomIntervalSeconds(t, func(int64) int64 { return 0 })
	next, err := computeNextRun("@random 5-5", from)
	require.NoError(t, err)
	require.Equal(t, from.Add(5*time.Minute), next)

	next, err = computeNextRun("  @random 10 - 15  ", from)
	require.NoError(t, err)
	require.Equal(t, from.Add(10*time.Minute), next)
}

func TestComputeNextRunRandomIntervalRejectsInvalid(t *testing.T) {
	from := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	want := `random interval must look like "@random 10-15" (minutes, 1 <= min <= max <= 1440)`
	for _, tc := range []struct{ name, expr string }{
		{"descending", "@random 15-10"},
		{"zero minimum", "@random 0-5"},
		{"too large maximum", "@random 10-2000"},
		{"missing range", "@random"},
		{"non numeric", "@random abc"},
		{"missing maximum", "@random 10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := computeNextRun(tc.expr, from)
			require.EqualError(t, err, want)
		})
	}
}

func TestComputeNextRunKeepsCronBehavior(t *testing.T) {
	from := time.Date(2026, 3, 4, 5, 7, 0, 0, time.UTC)
	next, err := computeNextRun("*/15 * * * *", from)
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 3, 4, 5, 15, 0, 0, time.UTC), next)
}

func TestNextPlanRunRandomIntervalCandyRule(t *testing.T) {
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	for _, tc := range []struct {
		name string
		pick func(int64) int64
		want time.Time
	}{
		{"minimum", func(int64) int64 { return 0 }, now.Add(10 * time.Minute)},
		{"maximum", func(n int64) int64 { return n - 1 }, now.Add(15 * time.Minute)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubRandomIntervalSeconds(t, tc.pick)
			plan := multiModelQualityPlan(t, `["gpt-6-astra"]`)
			plan.CronExpression = "@random 10-15"
			next, err := nextPlanRun(plan, now)
			require.NoError(t, err)
			require.WithinRange(t, next, now.Add(10*time.Minute), now.Add(15*time.Minute))
			require.Equal(t, tc.want, next)
		})
	}
}
