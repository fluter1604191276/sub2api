//go:build unit

package service

import (
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
