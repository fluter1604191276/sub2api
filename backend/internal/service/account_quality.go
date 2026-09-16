package service

import (
	"context"
	"fmt"
	"math"
	"time"
)

const (
	AccountQualityRealtimeWindowHours  = 1
	AccountQualityWindowHours          = 24
	AccountQualityScoreVersion         = 3
	accountQualityMinSamples           = 3
	accountQualityMinTTFTSamples       = 3
	accountQualityRobustMedianWeight   = 0.80
	accountQualityRobustTailWeight     = 0.20
	accountQualityTTFTWeight           = 0.70
	accountQualityGenerationWeight     = 0.30
	accountQualityTTFTOnlyMax          = 79
	accountQualityDurationOnlyMax      = 69
	accountQualityFailingMinErrors     = 3
	accountQualityDegradedMinAttempts  = 5
	accountQualityDegradedFailureRatio = 0.20
	accountUnifiedQualityMinLiveWeight = 0.40
	accountUnifiedQualityMaxLiveWeight = 0.80
	accountUnifiedQualityHistoricalCap = 0.70

	accountQualityBasisTTFTDuration   = "ttft_duration"
	accountQualityBasisTTFTOnly       = "ttft_only"
	accountQualityBasisDurationOnly   = "duration_only"
	accountQualityBasisTTFTGeneration = "ttft_generation"
	accountQualityBasisGenerationOnly = "generation_only"

	accountQualityActivityActive    = "active"
	accountQualityActivityLowSample = "low_sample"
	accountQualityActivityDegraded  = "degraded"
	accountQualityActivityFailing   = "failing"
	accountQualityActivityIdle      = "idle"
)

type accountQualityCurvePoint struct {
	LatencyMs float64
	Score     float64
}

var accountQualityTTFTCurve = []accountQualityCurvePoint{
	{LatencyMs: 800, Score: 100},
	{LatencyMs: 2000, Score: 95},
	{LatencyMs: 4000, Score: 85},
	{LatencyMs: 8000, Score: 72},
	{LatencyMs: 12000, Score: 62},
	{LatencyMs: 20000, Score: 48},
	{LatencyMs: 30000, Score: 35},
	{LatencyMs: 45000, Score: 20},
	{LatencyMs: 60000, Score: 10},
	{LatencyMs: 90000, Score: 0},
}

var accountQualityDurationCurve = []accountQualityCurvePoint{
	{LatencyMs: 5000, Score: 100},
	{LatencyMs: 10000, Score: 90},
	{LatencyMs: 20000, Score: 75},
	{LatencyMs: 40000, Score: 55},
	{LatencyMs: 60000, Score: 40},
	{LatencyMs: 90000, Score: 25},
	{LatencyMs: 120000, Score: 12},
	{LatencyMs: 180000, Score: 0},
}

var accountQualityGenerationCurve = []accountQualityCurvePoint{
	{LatencyMs: 10, Score: 0},
	{LatencyMs: 20, Score: 40},
	{LatencyMs: 30, Score: 65},
	{LatencyMs: 40, Score: 80},
	{LatencyMs: 50, Score: 92},
	{LatencyMs: 70, Score: 100},
}

// AccountQualityWindow contains the latency summary for one recent-request window.
// A nil score means there is not enough evidence to make a useful judgement.
type AccountQualityWindow struct {
	SampleCount                      int64    `json:"sample_count"`
	FirstTokenSampleCount            int64    `json:"first_token_sample_count"`
	AverageFirstTokenMs              *float64 `json:"average_first_token_ms"`
	AverageDurationMs                *float64 `json:"average_duration_ms"`
	P50FirstTokenMs                  *float64 `json:"p50_first_token_ms,omitempty"`
	P90FirstTokenMs                  *float64 `json:"p90_first_token_ms,omitempty"`
	GenerationSampleCount            int64    `json:"generation_sample_count,omitempty"`
	P50GenerationTokensPerSecond     *float64 `json:"p50_generation_tokens_per_second,omitempty"`
	P10GenerationTokensPerSecond     *float64 `json:"p10_generation_tokens_per_second,omitempty"`
	RoutingFirstTokenMs              *float64 `json:"routing_first_token_ms,omitempty"`
	RoutingGenerationTokensPerSecond *float64 `json:"routing_generation_tokens_per_second,omitempty"`
	QualityScore                     *int     `json:"quality_score"`
	QualityGrade                     string   `json:"quality_grade,omitempty"`
	ScoreBasis                       string   `json:"score_basis,omitempty"`
}

type AccountQualityPeriod struct {
	Last10      AccountQualityWindow `json:"last_10"`
	Last100     AccountQualityWindow `json:"last_100"`
	WindowHours int                  `json:"window_hours"`
}

type AccountQualityActivity struct {
	State                  string     `json:"state"`
	SuccessfulRequestCount int64      `json:"successful_request_count"`
	FailedRequestCount     int64      `json:"failed_request_count"`
	LastSuccessAt          *time.Time `json:"last_success_at"`
	LastErrorAt            *time.Time `json:"last_error_at"`
}

type AccountQualityStats struct {
	Last10       AccountQualityWindow   `json:"last_10"`
	Last100      AccountQualityWindow   `json:"last_100"`
	WindowHours  int                    `json:"window_hours"`
	Recent1h     AccountQualityPeriod   `json:"recent_1h"`
	Activity     AccountQualityActivity `json:"activity"`
	Unified      AccountUnifiedQuality  `json:"unified"`
	ScoreVersion int                    `json:"score_version"`
}

type AccountUnifiedQuality struct {
	Score                 *int    `json:"score"`
	Grade                 string  `json:"grade,omitempty"`
	Confidence            float64 `json:"confidence"`
	Source                string  `json:"source"`
	SampleCount           int64   `json:"sample_count"`
	FirstTokenSampleCount int64   `json:"first_token_sample_count"`
}

type AccountQualityPeriodSamples struct {
	Last10  AccountQualityWindow
	Last100 AccountQualityWindow
}

// AccountQualitySamples is the repository result before the service applies the
// display-only scoring policy.
type AccountQualitySamples struct {
	Recent1h             AccountQualityPeriodSamples
	Last24h              AccountQualityPeriodSamples
	SuccessfulRequests1h int64
	FailedRequests1h     int64
	LastSuccessAt        *time.Time
	LastErrorAt          *time.Time
}

type accountQualityStatsReader interface {
	GetAccountQualityStatsBatch(ctx context.Context, accountIDs []int64, startTime, realtimeStartTime, endTime time.Time) (map[int64]AccountQualitySamples, error)
}

type groupQualityStatsReader interface {
	GetGroupQualityStatsBatch(ctx context.Context, groupIDs []int64, startTime, realtimeStartTime, endTime time.Time) (map[int64]AccountQualitySamples, error)
}

func normalizeQualityIDs(ids []int64) []int64 {
	uniqueIDs := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}
	return uniqueIDs
}

func buildAccountQualityStats(ids []int64, samples map[int64]AccountQualitySamples) map[int64]AccountQualityStats {
	result := make(map[int64]AccountQualityStats, len(ids))
	for _, id := range ids {
		sample := samples[id]
		recent := AccountQualityPeriod{
			Last10:      applyAccountQualityScore(sample.Recent1h.Last10),
			Last100:     applyAccountQualityScore(sample.Recent1h.Last100),
			WindowHours: AccountQualityRealtimeWindowHours,
		}
		stable := AccountQualityPeriod{
			Last10:      applyAccountQualityScore(sample.Last24h.Last10),
			Last100:     applyAccountQualityScore(sample.Last24h.Last100),
			WindowHours: AccountQualityWindowHours,
		}
		result[id] = AccountQualityStats{
			Last10:      stable.Last10,
			Last100:     stable.Last100,
			WindowHours: stable.WindowHours,
			Recent1h:    recent,
			Activity: AccountQualityActivity{
				State:                  classifyAccountQualityActivity(sample.SuccessfulRequests1h, sample.FailedRequests1h),
				SuccessfulRequestCount: sample.SuccessfulRequests1h,
				FailedRequestCount:     sample.FailedRequests1h,
				LastSuccessAt:          sample.LastSuccessAt,
				LastErrorAt:            sample.LastErrorAt,
			},
			Unified:      buildAccountUnifiedQuality(recent, stable),
			ScoreVersion: AccountQualityScoreVersion,
		}
	}
	return result
}

func buildAccountUnifiedQuality(recent, stable AccountQualityPeriod) AccountUnifiedQuality {
	live, liveTarget, hasLive := preferredAccountQualityWindow(recent, true)
	baseline, baselineTarget, hasBaseline := preferredAccountQualityWindow(stable, false)
	summary := AccountUnifiedQuality{Source: "unscored"}

	switch {
	case hasLive && hasBaseline:
		liveCoverage := accountQualityCoverage(live.SampleCount, liveTarget)
		baselineCoverage := accountQualityCoverage(baseline.SampleCount, baselineTarget)
		liveWeight := accountUnifiedQualityMinLiveWeight +
			(accountUnifiedQualityMaxLiveWeight-accountUnifiedQualityMinLiveWeight)*liveCoverage
		score := int(math.Round(float64(*live.QualityScore)*liveWeight + float64(*baseline.QualityScore)*(1-liveWeight)))
		summary.Score = &score
		summary.Grade = accountQualityGrade(score)
		summary.Confidence = roundAccountQualityConfidence(0.55*liveCoverage + 0.45*baselineCoverage)
		summary.Source = "realtime_blend"
	case hasLive:
		score := *live.QualityScore
		summary.Score = &score
		summary.Grade = accountQualityGrade(score)
		summary.Confidence = roundAccountQualityConfidence(0.8 * accountQualityCoverage(live.SampleCount, liveTarget))
		summary.Source = "realtime_only"
	case hasBaseline:
		score := *baseline.QualityScore
		summary.Score = &score
		summary.Grade = accountQualityGrade(score)
		summary.Confidence = roundAccountQualityConfidence(accountUnifiedQualityHistoricalCap * accountQualityCoverage(baseline.SampleCount, baselineTarget))
		summary.Source = "historical"
	default:
		return summary
	}

	summary.SampleCount = maxInt64(live.SampleCount, baseline.SampleCount)
	summary.FirstTokenSampleCount = maxInt64(live.FirstTokenSampleCount, baseline.FirstTokenSampleCount)
	return summary
}

func preferredAccountQualityWindow(period AccountQualityPeriod, preferRecent bool) (AccountQualityWindow, int64, bool) {
	first, firstTarget := period.Last100, int64(100)
	second, secondTarget := period.Last10, int64(10)
	if preferRecent {
		first, firstTarget = period.Last10, 10
		second, secondTarget = period.Last100, 100
	}
	if first.QualityScore != nil {
		return first, firstTarget, true
	}
	if second.QualityScore != nil {
		return second, secondTarget, true
	}
	return AccountQualityWindow{}, 0, false
}

func accountQualityCoverage(samples, target int64) float64 {
	if samples <= 0 || target <= 0 {
		return 0
	}
	return math.Min(1, float64(samples)/float64(target))
}

func roundAccountQualityConfidence(value float64) float64 {
	return math.Round(math.Max(0, math.Min(1, value))*100) / 100
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

// GetAccountQualityStatsBatch returns display-only latency summaries for accounts.
// It deliberately uses an optional repository interface so existing test doubles
// and alternate repositories do not need to grow a mandatory method.
func (s *AccountUsageService) GetAccountQualityStatsBatch(ctx context.Context, accountIDs []int64, now time.Time) (map[int64]AccountQualityStats, error) {
	uniqueIDs := normalizeQualityIDs(accountIDs)

	result := make(map[int64]AccountQualityStats, len(uniqueIDs))
	if len(uniqueIDs) == 0 {
		return result, nil
	}
	reader, ok := s.usageLogRepo.(accountQualityStatsReader)
	if !ok {
		return nil, fmt.Errorf("account quality statistics are not supported by the usage repository")
	}
	endTime := now.UTC()
	samples, err := reader.GetAccountQualityStatsBatch(
		ctx,
		uniqueIDs,
		endTime.Add(-AccountQualityWindowHours*time.Hour),
		endTime.Add(-AccountQualityRealtimeWindowHours*time.Hour),
		endTime,
	)
	if err != nil {
		return nil, fmt.Errorf("get account quality stats failed: %w", err)
	}
	return buildAccountQualityStats(uniqueIDs, samples), nil
}

// GetGroupQualityStatsBatch returns the same display-only quality summary as
// account quality, but aggregates successful timed requests by group.
func (s *DashboardService) GetGroupQualityStatsBatch(ctx context.Context, groupIDs []int64, now time.Time) (map[int64]AccountQualityStats, error) {
	uniqueIDs := normalizeQualityIDs(groupIDs)
	result := make(map[int64]AccountQualityStats, len(uniqueIDs))
	if len(uniqueIDs) == 0 {
		return result, nil
	}
	reader, ok := s.usageRepo.(groupQualityStatsReader)
	if !ok {
		return nil, fmt.Errorf("group quality statistics are not supported by the usage repository")
	}
	endTime := now.UTC()
	samples, err := reader.GetGroupQualityStatsBatch(
		ctx,
		uniqueIDs,
		endTime.Add(-AccountQualityWindowHours*time.Hour),
		endTime.Add(-AccountQualityRealtimeWindowHours*time.Hour),
		endTime,
	)
	if err != nil {
		return nil, fmt.Errorf("get group quality stats failed: %w", err)
	}
	return buildAccountQualityStats(uniqueIDs, samples), nil
}

// GetAccountQualityStatsBatch exposes the same display-only quality calculation
// to read-only preview services that already depend on DashboardService.
func (s *DashboardService) GetAccountQualityStatsBatch(ctx context.Context, accountIDs []int64, now time.Time) (map[int64]AccountQualityStats, error) {
	uniqueIDs := normalizeQualityIDs(accountIDs)
	result := make(map[int64]AccountQualityStats, len(uniqueIDs))
	if len(uniqueIDs) == 0 {
		return result, nil
	}
	reader, ok := s.usageRepo.(accountQualityStatsReader)
	if !ok {
		return nil, fmt.Errorf("account quality statistics are not supported by the usage repository")
	}
	endTime := now.UTC()
	samples, err := reader.GetAccountQualityStatsBatch(
		ctx,
		uniqueIDs,
		endTime.Add(-AccountQualityWindowHours*time.Hour),
		endTime.Add(-AccountQualityRealtimeWindowHours*time.Hour),
		endTime,
	)
	if err != nil {
		return nil, fmt.Errorf("get account quality stats failed: %w", err)
	}
	return buildAccountQualityStats(uniqueIDs, samples), nil
}

func classifyAccountQualityActivity(successfulRequests, failedRequests int64) string {
	if successfulRequests == 0 && failedRequests >= accountQualityFailingMinErrors {
		return accountQualityActivityFailing
	}
	attempts := successfulRequests + failedRequests
	if attempts >= accountQualityDegradedMinAttempts && float64(failedRequests)/float64(attempts) >= accountQualityDegradedFailureRatio {
		return accountQualityActivityDegraded
	}
	if successfulRequests >= accountQualityMinSamples {
		return accountQualityActivityActive
	}
	if attempts > 0 {
		return accountQualityActivityLowSample
	}
	return accountQualityActivityIdle
}

func applyAccountQualityScore(window AccountQualityWindow) AccountQualityWindow {
	if window.SampleCount < accountQualityMinSamples {
		return window
	}
	if hasRobustQualityEvidence(window) {
		return applyRobustQualityScore(
			window,
			accountQualityBasisTTFTGeneration,
			accountQualityBasisTTFTOnly,
			accountQualityBasisGenerationOnly,
		)
	}

	ttftScore, hasTTFT := qualityCurveScore(window.AverageFirstTokenMs, accountQualityTTFTCurve)
	if window.FirstTokenSampleCount < accountQualityMinTTFTSamples {
		hasTTFT = false
	}
	durationScore, hasDuration := qualityCurveScore(window.AverageDurationMs, accountQualityDurationCurve)

	var score float64
	var basis string
	if hasTTFT && hasDuration {
		score = math.Min(ttftScore*0.85+durationScore*0.15, accountQualityDurationOnlyMax)
		basis = accountQualityBasisTTFTDuration
	} else if hasTTFT {
		score = math.Min(ttftScore, accountQualityDurationOnlyMax)
		basis = accountQualityBasisTTFTOnly
	} else if hasDuration {
		score = math.Min(durationScore, accountQualityDurationOnlyMax)
		basis = accountQualityBasisDurationOnly
	} else {
		return window
	}

	rounded := int(math.Round(math.Max(0, math.Min(100, score))))
	window.QualityScore = &rounded
	window.QualityGrade = accountQualityGrade(rounded)
	window.ScoreBasis = basis
	return window
}

func hasRobustQualityEvidence(window AccountQualityWindow) bool {
	return window.P50FirstTokenMs != nil || window.P90FirstTokenMs != nil ||
		window.P50GenerationTokensPerSecond != nil || window.P10GenerationTokensPerSecond != nil
}

func applyRobustQualityScore(window AccountQualityWindow, combinedBasis, ttftOnlyBasis, generationOnlyBasis string) AccountQualityWindow {
	if window.SampleCount < accountQualityMinSamples {
		return window
	}

	var ttftScore *float64
	if window.FirstTokenSampleCount >= accountQualityMinTTFTSamples && validQualityMetric(window.P50FirstTokenMs) && validQualityMetric(window.P90FirstTokenMs) {
		medianScore, medianOK := qualityCurveScore(window.P50FirstTokenMs, accountQualityTTFTCurve)
		tailScore, tailOK := qualityCurveScore(window.P90FirstTokenMs, accountQualityTTFTCurve)
		if medianOK && tailOK {
			routingTTFT := *window.P50FirstTokenMs*accountQualityRobustMedianWeight + *window.P90FirstTokenMs*accountQualityRobustTailWeight
			window.RoutingFirstTokenMs = &routingTTFT
			score := medianScore*accountQualityRobustMedianWeight + tailScore*accountQualityRobustTailWeight
			ttftScore = &score
		}
	}

	var generationScore *float64
	if window.GenerationSampleCount >= accountQualityMinSamples && validQualityMetric(window.P50GenerationTokensPerSecond) && validQualityMetric(window.P10GenerationTokensPerSecond) {
		routingGeneration := *window.P50GenerationTokensPerSecond*accountQualityRobustMedianWeight + *window.P10GenerationTokensPerSecond*accountQualityRobustTailWeight
		window.RoutingGenerationTokensPerSecond = &routingGeneration
		medianScore := accountQualityGenerationScore(*window.P50GenerationTokensPerSecond)
		tailScore := accountQualityGenerationScore(*window.P10GenerationTokensPerSecond)
		score := medianScore*accountQualityRobustMedianWeight + tailScore*accountQualityRobustTailWeight
		generationScore = &score
	}

	var score float64
	switch {
	case ttftScore != nil && generationScore != nil:
		score = *ttftScore*accountQualityTTFTWeight + *generationScore*accountQualityGenerationWeight
		window.ScoreBasis = combinedBasis
	case ttftScore != nil:
		score = math.Min(*ttftScore, accountQualityTTFTOnlyMax)
		window.ScoreBasis = ttftOnlyBasis
	case generationScore != nil:
		score = math.Min(*generationScore, accountQualityDurationOnlyMax)
		window.ScoreBasis = generationOnlyBasis
	default:
		return window
	}

	rounded := int(math.Round(math.Max(0, math.Min(100, score))))
	window.QualityScore = &rounded
	window.QualityGrade = accountQualityGrade(rounded)
	return window
}

func validQualityMetric(value *float64) bool {
	return value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0
}

func accountQualityGenerationScore(tokensPerSecond float64) float64 {
	if tokensPerSecond <= accountQualityGenerationCurve[0].LatencyMs {
		return accountQualityGenerationCurve[0].Score
	}
	for i := 1; i < len(accountQualityGenerationCurve); i++ {
		current := accountQualityGenerationCurve[i]
		if tokensPerSecond > current.LatencyMs {
			continue
		}
		previous := accountQualityGenerationCurve[i-1]
		ratio := (tokensPerSecond - previous.LatencyMs) / (current.LatencyMs - previous.LatencyMs)
		return previous.Score + ratio*(current.Score-previous.Score)
	}
	return accountQualityGenerationCurve[len(accountQualityGenerationCurve)-1].Score
}

func qualityCurveScore(value *float64, curve []accountQualityCurvePoint) (float64, bool) {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) || len(curve) == 0 {
		return 0, false
	}
	if *value <= curve[0].LatencyMs {
		return curve[0].Score, true
	}
	for i := 1; i < len(curve); i++ {
		current := curve[i]
		if *value > current.LatencyMs {
			continue
		}
		previous := curve[i-1]
		ratio := (*value - previous.LatencyMs) / (current.LatencyMs - previous.LatencyMs)
		return previous.Score + ratio*(current.Score-previous.Score), true
	}
	return curve[len(curve)-1].Score, true
}

func accountQualityGrade(score int) string {
	switch {
	case score >= 95:
		return "S+"
	case score >= 90:
		return "S"
	case score >= 85:
		return "S-"
	case score >= 80:
		return "A+"
	case score >= 75:
		return "A"
	case score >= 70:
		return "A-"
	case score >= 65:
		return "B+"
	case score >= 60:
		return "B"
	case score >= 50:
		return "B-"
	default:
		return "C"
	}
}
