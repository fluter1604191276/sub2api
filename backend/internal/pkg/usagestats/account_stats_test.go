package usagestats

import "testing"

func TestCalculateCacheHitRate(t *testing.T) {
	tests := []struct {
		name                string
		inputTokens         int64
		cacheCreationTokens int64
		cacheReadTokens     int64
		wantRate            float64
		wantHasRate         bool
	}{
		{
			name:                "weighted rate includes cache creation misses",
			inputTokens:         100,
			cacheCreationTokens: 100,
			cacheReadTokens:     300,
			wantRate:            60,
			wantHasRate:         true,
		},
		{
			name:        "zero cache reads is a valid zero percent rate",
			inputTokens: 100,
			wantRate:    0,
			wantHasRate: true,
		},
		{
			name:        "no cacheable tokens has no rate",
			wantHasRate: false,
		},
		{
			name:                "negative values are treated as zero",
			inputTokens:         -1,
			cacheCreationTokens: -2,
			cacheReadTokens:     10,
			wantRate:            100,
			wantHasRate:         true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotRate, gotHasRate := CalculateCacheHitRate(tc.inputTokens, tc.cacheCreationTokens, tc.cacheReadTokens)
			if gotHasRate != tc.wantHasRate {
				t.Fatalf("has rate = %v, want %v", gotHasRate, tc.wantHasRate)
			}
			if gotHasRate && gotRate != tc.wantRate {
				t.Fatalf("rate = %v, want %v", gotRate, tc.wantRate)
			}
		})
	}
}
