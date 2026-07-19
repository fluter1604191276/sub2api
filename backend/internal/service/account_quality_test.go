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
			SampleCount:         10,
			AverageFirstTokenMs: &ttft,
			AverageDurationMs:   &duration,
		})

		require.NotNil(t, window.QualityScore)
		require.Equal(t, 100, *window.QualityScore)
	})

	t.Run("insufficient samples remain unscored", func(t *testing.T) {
		ttft := 500.0
		duration := 1000.0
		window := applyAccountQualityScore(AccountQualityWindow{
			SampleCount:         2,
			AverageFirstTokenMs: &ttft,
			AverageDurationMs:   &duration,
		})

		require.Nil(t, window.QualityScore)
	})

	t.Run("nonstreaming requests use duration only", func(t *testing.T) {
		duration := 62500.0
		window := applyAccountQualityScore(AccountQualityWindow{
			SampleCount:       10,
			AverageDurationMs: &duration,
		})

		require.NotNil(t, window.QualityScore)
		require.Equal(t, 50, *window.QualityScore)
	})
}

func TestLatencyScoreBounds(t *testing.T) {
	excellent := 400.0
	poor := 9000.0
	middle := 4400.0

	score, ok := latencyScore(&excellent, 800, 8000)
	require.True(t, ok)
	require.Equal(t, 100.0, score)

	score, ok = latencyScore(&poor, 800, 8000)
	require.True(t, ok)
	require.Equal(t, 0.0, score)

	score, ok = latencyScore(&middle, 800, 8000)
	require.True(t, ok)
	require.Equal(t, 50.0, score)

	_, ok = latencyScore(nil, 800, 8000)
	require.False(t, ok)
}
