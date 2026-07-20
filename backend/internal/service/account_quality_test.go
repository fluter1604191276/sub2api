package service

import (
	"testing"

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

	t.Run("nonstreaming requests use duration only", func(t *testing.T) {
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
