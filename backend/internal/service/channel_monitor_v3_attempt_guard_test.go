//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type cmv3GuardUpstream struct{ calls atomic.Int32 }

func (s *cmv3GuardUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	s.calls.Add(1)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"usage":{"input_tokens":1,"output_tokens":1}}`))}, nil
}

func (s *cmv3GuardUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(req, proxy, id, concurrency)
}

func newCMV3GuardTransport() (HTTPUpstream, *cmv3GuardUpstream) {
	accounts := &cmv3AccountsFake{accounts: []Account{{ID: 31, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true}}}
	cost := NewUpstreamAttemptCostCalculator(accounts, nil, NewBillingService(&config.Config{}, nil))
	base := &cmv3GuardUpstream{}
	return NewObservedHTTPUpstream(base, NewUpstreamAttemptRecorder(&attemptCaptureRepo{}, cost)), base
}

func cmv3GuardRequest(t *testing.T, guard *channelMonitorV3AttemptGuard, model string) *http.Request {
	t.Helper()
	ctx := context.WithValue(context.Background(), channelMonitorV3AttemptGuardKey{}, guard)
	ctx = WithUpstreamAttemptSource(ctx, "channel_monitor_v3")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://upstream.invalid/v1/messages", strings.NewReader(`{"model":"`+model+`","max_tokens":128,"messages":[{"role":"user","content":"fixture"}]}`))
	require.NoError(t, err)
	return req
}

func TestChannelMonitorV3AttemptGuardAllowsOneConcurrentUpstreamAttempt(t *testing.T) {
	client, base := newCMV3GuardTransport()
	guard := &channelMonitorV3AttemptGuard{day: channelMonitorV3BudgetDay(time.Now()), limit: 2, now: time.Now}
	req := cmv3GuardRequest(t, guard, "claude-sonnet-4")
	var wg sync.WaitGroup
	var allowed atomic.Int32
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var resp *http.Response
			var err error
			if i%2 == 0 {
				resp, err = client.Do(req, "", 31, 1)
			} else {
				resp, err = client.DoWithTLS(req, "", 31, 1, nil)
			}
			if err == nil {
				allowed.Add(1)
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, allowed.Load())
	require.EqualValues(t, 1, base.calls.Load())
}

func TestChannelMonitorV3AttemptGuardRejectsUnpricedOverBudgetAndPreviousDay(t *testing.T) {
	for _, tc := range []struct {
		name, day, model string
		limit            float64
	}{
		{"unknown price", channelMonitorV3BudgetDay(time.Now()), "unknown-price-fixture", 2},
		{"insufficient reservation", channelMonitorV3BudgetDay(time.Now()), "claude-sonnet-4", 0.0000000001},
		{"day rollover", channelMonitorV3BudgetDay(time.Now().Add(-24 * time.Hour)), "claude-sonnet-4", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, base := newCMV3GuardTransport()
			guard := &channelMonitorV3AttemptGuard{day: tc.day, limit: tc.limit, now: time.Now}
			resp, err := client.Do(cmv3GuardRequest(t, guard, tc.model), "", 31, 1)
			require.Error(t, err)
			require.Nil(t, resp)
			require.Zero(t, base.calls.Load())
		})
	}
}

func TestChannelMonitorV3AttemptGuardRejectsGatewayRaisedOrMissingOutputLimit(t *testing.T) {
	for _, body := range []string{
		`{"model":"claude-sonnet-4","max_tokens":128000}`,
		`{"model":"claude-sonnet-4"}`,
		`{"model":"claude-sonnet-4","max_tokens":128,"tools":[{"type":"web_search"}]}`,
		`{"model":"claude-sonnet-4","max_tokens":128,"n":2}`,
		`{"model":"claude-sonnet-4","max_tokens":128,"generationConfig":{"candidateCount":2}}`,
		`{"model":"claude-sonnet-4","max_tokens":128,"padding":"` + strings.Repeat("x", 32<<10) + `"}`,
	} {
		client, base := newCMV3GuardTransport()
		guard := &channelMonitorV3AttemptGuard{day: channelMonitorV3BudgetDay(time.Now()), limit: 2, now: time.Now}
		ctx := context.WithValue(context.Background(), channelMonitorV3AttemptGuardKey{}, guard)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://upstream.invalid/v1/messages", strings.NewReader(body))
		require.NoError(t, err)
		_, err = client.Do(req, "", 31, 1)
		require.Error(t, err)
		require.Zero(t, base.calls.Load())
	}
}
