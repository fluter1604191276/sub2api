package service

import "testing"

func TestPricingCurrencyForPlatform(t *testing.T) {
	tests := []struct {
		platform string
		want     string
	}{
		{platform: PlatformDeepseek, want: "CNY"},
		{platform: PlatformZhipu, want: "CNY"},
		{platform: PlatformKimi, want: "CNY"},
		{platform: PlatformMiniMax, want: "CNY"},
		{platform: "qwen", want: "CNY"},
		{platform: PlatformOpenAI, want: "USD"},
		{platform: PlatformAnthropic, want: "USD"},
		{platform: PlatformGrok, want: "USD"},
		{platform: "", want: "USD"},
	}
	for _, tt := range tests {
		t.Run(tt.platform, func(t *testing.T) {
			if got := PricingCurrencyForPlatform(tt.platform); got != tt.want {
				t.Fatalf("PricingCurrencyForPlatform(%q) = %q, want %q", tt.platform, got, tt.want)
			}
		})
	}
}
