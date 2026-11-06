//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type attemptRoundTripFunc func(*http.Request) (*http.Response, error)

func (f attemptRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type attemptRoundTripClient struct{ client *http.Client }

func (c attemptRoundTripClient) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return c.client.Do(req)
}

func (c attemptRoundTripClient) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return c.Do(req, proxy, id, concurrency)
}

func TestUpstreamAttemptRecordsEachPhysicalHTTPRedirectExchange(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if req.URL.Path == "/first/responses" {
			w.Header().Set("X-Request-Id", "first-hop")
			w.Header().Set("Location", "/second/responses")
			w.WriteHeader(http.StatusTemporaryRedirect)
			_, _ = io.WriteString(w, `{"usage":{"input_tokens":7,"output_tokens":2}}`)
			return
		}
		w.Header().Set("X-Request-Id", "second-hop")
		_, _ = io.WriteString(w, `{"status":"completed","usage":{"input_tokens":11,"output_tokens":3}}`)
	}))
	defer server.Close()
	repo := &attemptCaptureRepo{}
	calculator := NewUpstreamAttemptCostCalculator(attemptCostAccountRepo{multiplier: 1}, nil,
		newTestBillingServiceWithPrices(map[string]*ModelPricing{"claude-sonnet-4": {InputPricePerToken: .001, OutputPricePerToken: .002}}))
	base := attemptRoundTripClient{client: &http.Client{Transport: attemptRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return ObserveHTTPUpstreamRoundTrip(req, http.DefaultTransport)
	})}}
	client := NewObservedHTTPUpstream(base, NewUpstreamAttemptRecorder(repo, calculator))
	req, err := http.NewRequest(http.MethodPost, server.URL+"/first/responses", strings.NewReader(`{"model":"claude-sonnet-4","input":"fixture"}`))
	require.NoError(t, err)
	resp, err := client.Do(req, "", 1, 1)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	repo.wait(t, 2)
	require.EqualValues(t, 2, requests.Load())
	require.NotEqual(t, repo.items[0].AttemptID, repo.items[1].AttemptID)
	require.Equal(t, "first-hop", repo.items[0].UpstreamRequestID)
	require.Equal(t, "second-hop", repo.items[1].UpstreamRequestID)
	require.EqualValues(t, 7, *repo.items[0].InputTokens)
	require.EqualValues(t, 11, *repo.items[1].InputTokens)
	require.InDelta(t, .011, *repo.items[0].CostUSD, 1e-10)
	require.InDelta(t, .017, *repo.items[1].CostUSD, 1e-10)
}

func TestObserveHTTPUpstreamRoundTripPreparesOnlyObservedRequests(t *testing.T) {
	base := attemptRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("raw"))}, nil
	})
	prepareCalls := 0
	prepare := func(resp *http.Response) {
		prepareCalls++
		resp.Header = http.Header{"X-Prepared": []string{"yes"}}
	}

	plain, err := http.NewRequest(http.MethodGet, "https://example.invalid/plain", nil)
	require.NoError(t, err)
	resp, err := ObserveHTTPUpstreamRoundTrip(plain, base, prepare)
	require.NoError(t, err)
	require.Zero(t, prepareCalls)
	require.Empty(t, resp.Header.Get("X-Prepared"))
	require.NoError(t, resp.Body.Close())

	observed := false
	ctx := context.WithValue(context.Background(), upstreamAttemptRoundTripKey{}, upstreamAttemptRoundTripObserver(func(req *http.Request, transport http.RoundTripper) (*http.Response, error) {
		response, roundTripErr := transport.RoundTrip(req)
		require.Equal(t, "yes", response.Header.Get("X-Prepared"))
		observed = true
		return response, roundTripErr
	}))
	tracked, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.invalid/tracked", nil)
	require.NoError(t, err)
	resp, err = ObserveHTTPUpstreamRoundTrip(tracked, base, prepare)
	require.NoError(t, err)
	require.True(t, observed)
	require.Equal(t, 1, prepareCalls)
	require.NoError(t, resp.Body.Close())
}
