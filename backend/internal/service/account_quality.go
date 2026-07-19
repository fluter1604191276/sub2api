package service

import (
	"context"
	"fmt"
	"math"
	"time"
)

const (
	AccountQualityWindowHours  = 24
	AccountQualityScoreVersion = 1
	accountQualityMinSamples   = 3
)

// AccountQualityWindow contains the latency summary for one recent-request window.
// A nil score means there is not enough evidence to make a useful judgement.
type AccountQualityWindow struct {
	SampleCount           int64    `json:"sample_count"`
	FirstTokenSampleCount int64    `json:"first_token_sample_count"`
	AverageFirstTokenMs   *float64 `json:"average_first_token_ms"`
	AverageDurationMs     *float64 `json:"average_duration_ms"`
	QualityScore          *int     `json:"quality_score"`
}

type AccountQualityStats struct {
	Last10       AccountQualityWindow `json:"last_10"`
	Last100      AccountQualityWindow `json:"last_100"`
	WindowHours  int                  `json:"window_hours"`
	ScoreVersion int                  `json:"score_version"`
}

// AccountQualitySamples is the repository result before the service applies the
// display-only scoring policy.
type AccountQualitySamples struct {
	Last10  AccountQualityWindow
	Last100 AccountQualityWindow
}

type accountQualityStatsReader interface {
	GetAccountQualityStatsBatch(ctx context.Context, accountIDs []int64, startTime, endTime time.Time) (map[int64]AccountQualitySamples, error)
}

// GetAccountQualityStatsBatch returns display-only latency summaries for accounts.
// It deliberately uses an optional repository interface so existing test doubles
// and alternate repositories do not need to grow a mandatory method.
func (s *AccountUsageService) GetAccountQualityStatsBatch(ctx context.Context, accountIDs []int64, now time.Time) (map[int64]AccountQualityStats, error) {
	uniqueIDs := make([]int64, 0, len(accountIDs))
	seen := make(map[int64]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		if accountID <= 0 {
			continue
		}
		if _, exists := seen[accountID]; exists {
			continue
		}
		seen[accountID] = struct{}{}
		uniqueIDs = append(uniqueIDs, accountID)
	}

	result := make(map[int64]AccountQualityStats, len(uniqueIDs))
	if len(uniqueIDs) == 0 {
		return result, nil
	}
	reader, ok := s.usageLogRepo.(accountQualityStatsReader)
	if !ok {
		return nil, fmt.Errorf("account quality statistics are not supported by the usage repository")
	}
	endTime := now.UTC()
	samples, err := reader.GetAccountQualityStatsBatch(ctx, uniqueIDs, endTime.Add(-AccountQualityWindowHours*time.Hour), endTime)
	if err != nil {
		return nil, fmt.Errorf("get account quality stats failed: %w", err)
	}
	for _, accountID := range uniqueIDs {
		sample := samples[accountID]
		result[accountID] = AccountQualityStats{
			Last10:       applyAccountQualityScore(sample.Last10),
			Last100:      applyAccountQualityScore(sample.Last100),
			WindowHours:  AccountQualityWindowHours,
			ScoreVersion: AccountQualityScoreVersion,
		}
	}
	return result, nil
}

func applyAccountQualityScore(window AccountQualityWindow) AccountQualityWindow {
	if window.SampleCount < accountQualityMinSamples {
		return window
	}

	// TTFT is weighted more heavily than total duration. Total duration is kept
	// as a broad signal because it varies with output length and media requests.
	ttftScore, hasTTFT := latencyScore(window.AverageFirstTokenMs, 800, 8000)
	durationScore, hasDuration := latencyScore(window.AverageDurationMs, 5000, 120000)
	if hasTTFT && hasDuration {
		score := int(math.Round(ttftScore*0.65 + durationScore*0.35))
		window.QualityScore = &score
	} else if hasTTFT {
		score := int(math.Round(ttftScore))
		window.QualityScore = &score
	} else if hasDuration {
		score := int(math.Round(durationScore))
		window.QualityScore = &score
	}
	return window
}

func latencyScore(value *float64, excellent, poor float64) (float64, bool) {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return 0, false
	}
	if *value <= excellent {
		return 100, true
	}
	if *value >= poor {
		return 0, true
	}
	return 100 * (poor - *value) / (poor - excellent), true
}
