package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestApplyAccountQualityScore(t *testing.T) {
	t.Run("excellent latency scores 100", func(t *testing.T) {
		ttft := 800.0
		duration := 5000.0
		window := applyAccountQualityScore(AccountQualityWindow{
			SampleCount:           10,
			FirstTokenSampleCount: 10,
			AverageFirstTokenMs:   &ttft,
			AverageDurationMs:     &duration,
		})

		require.NotNil(t, window.QualityScore)
		require.Equal(t, 100, *window.QualityScore)
		require.Equal(t, "S+", window.QualityGrade)
		require.Equal(t, accountQualityBasisTTFTDuration, window.ScoreBasis)
	})

	t.Run("insufficient samples remain unscored", func(t *testing.T) {
		ttft := 500.0
		duration := 1000.0
		window := applyAccountQualityScore(AccountQualityWindow{
			SampleCount:           2,
			FirstTokenSampleCount: 2,
			AverageFirstTokenMs:   &ttft,
			AverageDurationMs:     &duration,
		})

		require.Nil(t, window.QualityScore)
		require.Empty(t, window.QualityGrade)
		require.Empty(t, window.ScoreBasis)
	})

	t.Run("requests without first token timing use duration only", func(t *testing.T) {
		duration := 62500.0
		window := applyAccountQualityScore(AccountQualityWindow{
			SampleCount:       10,
			AverageDurationMs: &duration,
		})

		require.NotNil(t, window.QualityScore)
		require.Equal(t, 39, *window.QualityScore)
		require.Equal(t, "C", window.QualityGrade)
		require.Equal(t, accountQualityBasisDurationOnly, window.ScoreBasis)
	})

	t.Run("fast duration only requests cannot receive an elite grade", func(t *testing.T) {
		duration := 2800.0
		window := applyAccountQualityScore(AccountQualityWindow{
			SampleCount:       10,
			AverageDurationMs: &duration,
		})

		require.NotNil(t, window.QualityScore)
		require.Equal(t, 69, *window.QualityScore)
		require.Equal(t, "B+", window.QualityGrade)
	})

	t.Run("sparse first token evidence falls back to duration", func(t *testing.T) {
		ttft := 1000.0
		duration := 20000.0
		window := applyAccountQualityScore(AccountQualityWindow{
			SampleCount:           10,
			FirstTokenSampleCount: 2,
			AverageFirstTokenMs:   &ttft,
			AverageDurationMs:     &duration,
		})

		require.NotNil(t, window.QualityScore)
		require.Equal(t, 69, *window.QualityScore)
		require.Equal(t, accountQualityBasisDurationOnly, window.ScoreBasis)
	})

	t.Run("realistic account latency stays distinguishable", func(t *testing.T) {
		ttft := 7700.0
		duration := 20000.0
		window := applyAccountQualityScore(AccountQualityWindow{
			SampleCount:           10,
			FirstTokenSampleCount: 10,
			AverageFirstTokenMs:   &ttft,
			AverageDurationMs:     &duration,
		})

		require.NotNil(t, window.QualityScore)
		require.Equal(t, 73, *window.QualityScore)
		require.Equal(t, "A-", window.QualityGrade)
	})
}

func TestBuildAccountQualityStatsIncludesRealtimeWindowAndActivity(t *testing.T) {
	ttft := 1200.0
	duration := 8000.0
	lastSuccess := time.Date(2026, 7, 22, 4, 10, 0, 0, time.UTC)
	lastError := lastSuccess.Add(-2 * time.Minute)

	stats := buildAccountQualityStats([]int64{7}, map[int64]AccountQualitySamples{
		7: {
			Recent1h: AccountQualityPeriodSamples{
				Last10: AccountQualityWindow{
					SampleCount:           10,
					FirstTokenSampleCount: 10,
					AverageFirstTokenMs:   &ttft,
					AverageDurationMs:     &duration,
				},
			},
			Last24h: AccountQualityPeriodSamples{
				Last100: AccountQualityWindow{
					SampleCount:           100,
					FirstTokenSampleCount: 100,
					AverageFirstTokenMs:   &ttft,
					AverageDurationMs:     &duration,
				},
			},
			SuccessfulRequests1h: 10,
			FailedRequests1h:     1,
			LastSuccessAt:        &lastSuccess,
			LastErrorAt:          &lastError,
		},
	})[7]

	require.Equal(t, 1, stats.Recent1h.WindowHours)
	require.NotNil(t, stats.Recent1h.Last10.QualityScore)
	require.Equal(t, 24, stats.WindowHours)
	require.NotNil(t, stats.Last100.QualityScore)
	require.Equal(t, accountQualityActivityActive, stats.Activity.State)
	require.Equal(t, int64(10), stats.Activity.SuccessfulRequestCount)
	require.Equal(t, int64(1), stats.Activity.FailedRequestCount)
	require.Equal(t, &lastSuccess, stats.Activity.LastSuccessAt)
	require.Equal(t, &lastError, stats.Activity.LastErrorAt)
	require.NotNil(t, stats.Unified.Score)
	require.Equal(t, "realtime_blend", stats.Unified.Source)
	require.Equal(t, 98, *stats.Unified.Score)
}

func TestBuildAccountUnifiedQuality(t *testing.T) {
	score := func(value int, samples int64) AccountQualityWindow {
		return AccountQualityWindow{
			SampleCount:           samples,
			FirstTokenSampleCount: samples,
			QualityScore:          &value,
		}
	}

	t.Run("full realtime evidence dominates the stable baseline", func(t *testing.T) {
		liveScore, stableScore := 80, 60
		summary := buildAccountUnifiedQuality(
			AccountQualityPeriod{Last10: score(liveScore, 10)},
			AccountQualityPeriod{Last100: score(stableScore, 100)},
		)

		require.NotNil(t, summary.Score)
		require.Equal(t, 76, *summary.Score)
		require.Equal(t, "A", summary.Grade)
		require.Equal(t, 1.0, summary.Confidence)
		require.Equal(t, "realtime_blend", summary.Source)
		require.Equal(t, int64(100), summary.SampleCount)
	})

	t.Run("sparse realtime evidence is blended conservatively", func(t *testing.T) {
		liveScore, stableScore := 70, 90
		summary := buildAccountUnifiedQuality(
			AccountQualityPeriod{Last10: score(liveScore, 3)},
			AccountQualityPeriod{Last100: score(stableScore, 100)},
		)

		require.NotNil(t, summary.Score)
		require.Equal(t, 80, *summary.Score)
		require.Equal(t, "A+", summary.Grade)
		require.Equal(t, 0.62, summary.Confidence)
	})

	t.Run("historical evidence stays visible but has capped confidence", func(t *testing.T) {
		stableScore := 85
		summary := buildAccountUnifiedQuality(
			AccountQualityPeriod{},
			AccountQualityPeriod{Last100: score(stableScore, 100)},
		)

		require.NotNil(t, summary.Score)
		require.Equal(t, 85, *summary.Score)
		require.Equal(t, "S-", summary.Grade)
		require.Equal(t, 0.7, summary.Confidence)
		require.Equal(t, "historical", summary.Source)
	})

	t.Run("missing scored windows remains unscored", func(t *testing.T) {
		summary := buildAccountUnifiedQuality(AccountQualityPeriod{}, AccountQualityPeriod{})

		require.Nil(t, summary.Score)
		require.Equal(t, "unscored", summary.Source)
		require.Zero(t, summary.Confidence)
	})
}

func TestClassifyAccountQualityActivity(t *testing.T) {
	tests := []struct {
		name      string
		successes int64
		failures  int64
		want      string
	}{
		{name: "idle without attempts", want: accountQualityActivityIdle},
		{name: "low sample success", successes: 2, want: accountQualityActivityLowSample},
		{name: "low sample failure", failures: 2, want: accountQualityActivityLowSample},
		{name: "failing without success", failures: 3, want: accountQualityActivityFailing},
		{name: "active with enough success", successes: 8, failures: 1, want: accountQualityActivityActive},
		{name: "degraded at twenty percent failures", successes: 8, failures: 2, want: accountQualityActivityDegraded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, classifyAccountQualityActivity(tt.successes, tt.failures))
		})
	}
}

func TestQualityCurveScore(t *testing.T) {
	value := 8000.0
	score, ok := qualityCurveScore(&value, accountQualityTTFTCurve)
	require.True(t, ok)
	require.InDelta(t, 72.0, score, 0.001)

	value = 10000.0
	score, ok = qualityCurveScore(&value, accountQualityTTFTCurve)
	require.True(t, ok)
	require.InDelta(t, 67.0, score, 0.001)

	value = 90000.0
	score, ok = qualityCurveScore(&value, accountQualityTTFTCurve)
	require.True(t, ok)
	require.Equal(t, 0.0, score)

	_, ok = qualityCurveScore(nil, accountQualityTTFTCurve)
	require.False(t, ok)
}

func TestAccountQualityGrade(t *testing.T) {
	tests := []struct {
		score int
		grade string
	}{
		{100, "S+"}, {95, "S+"}, {94, "S"}, {90, "S"}, {89, "S-"}, {85, "S-"},
		{84, "A+"}, {80, "A+"}, {79, "A"}, {75, "A"}, {74, "A-"}, {70, "A-"},
		{69, "B+"}, {65, "B+"}, {64, "B"}, {60, "B"}, {59, "B-"}, {50, "B-"},
		{49, "C"}, {0, "C"},
	}

	for _, tt := range tests {
		require.Equal(t, tt.grade, accountQualityGrade(tt.score), "score=%d", tt.score)
	}
}
