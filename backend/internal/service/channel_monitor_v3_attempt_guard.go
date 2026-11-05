package service

import (
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/tidwall/gjson"
)

type channelMonitorV3AttemptGuardKey struct{}

// A loopback dispatch shares this guard across all ordinary gateway retries.
// It allows one paid upstream attempt, priced against its durable reservation.
type channelMonitorV3AttemptGuard struct {
	day   string
	limit float64
	now   func() time.Time
	used  atomic.Bool
}

func (r *UpstreamAttemptRecorder) guardChannelMonitorV3Attempt(req *http.Request, attempt *UpstreamAttempt) error {
	guard, _ := req.Context().Value(channelMonitorV3AttemptGuardKey{}).(*channelMonitorV3AttemptGuard)
	if guard == nil {
		return nil
	}
	if err := req.Context().Err(); err != nil {
		return err
	}
	if guard.day != channelMonitorV3BudgetDay(guard.now()) || guard.used.Swap(true) {
		return fmt.Errorf("channel monitor v3 upstream attempt window exhausted")
	}
	if r == nil || r.cost == nil || attempt == nil || req.GetBody == nil {
		return fmt.Errorf("channel monitor v3 upstream pricing unavailable")
	}
	body, err := req.GetBody()
	if err != nil {
		return err
	}
	raw, readErr := io.ReadAll(io.LimitReader(body, (4<<20)+1))
	_ = body.Close()
	if readErr != nil || len(raw) > 4<<20 || !gjson.ValidBytes(raw) {
		return fmt.Errorf("channel monitor v3 upstream request exceeds estimate bounds")
	}
	bounded := false
	for _, path := range []string{"max_output_tokens", "max_completion_tokens", "max_tokens", "generationConfig.maxOutputTokens", "request.generationConfig.maxOutputTokens"} {
		value := gjson.GetBytes(raw, path)
		if !value.Exists() {
			continue
		}
		if value.Type != gjson.Number || value.Float() <= 0 || value.Float() > channelMonitorV3MaxOutputTokens {
			return fmt.Errorf("channel monitor v3 upstream output limit exceeds reservation bounds")
		}
		bounded = true
	}
	if !bounded {
		return fmt.Errorf("channel monitor v3 upstream output limit unavailable")
	}
	// A byte-count allowance bounds the small synthetic request, including
	// gateway-added prompts. Output includes the declared 128-token ceiling.
	input, output := int64(len(raw))+1024, int64(channelMonitorV3MaxOutputTokens)
	bound := *attempt
	bound.UsagePresent = true
	bound.InputTokens, bound.OutputTokens, bound.CacheCreationTokens = &input, &output, &input
	if err := r.cost.Price(req.Context(), &bound); err != nil {
		return err
	}
	if bound.CostUSD == nil || *bound.CostUSD > guard.limit {
		return fmt.Errorf("channel monitor v3 upstream attempt exceeds reservation")
	}
	return nil
}
