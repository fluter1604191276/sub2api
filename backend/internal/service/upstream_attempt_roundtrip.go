package service

import (
	"context"
	"net/http"
	"sync/atomic"
)

type upstreamAttemptRoundTripKey struct{}
type upstreamAttemptRoundTripObserver func(*http.Request, http.RoundTripper) (*http.Response, error)

// ObserveHTTPUpstreamRoundTrip observes a physical HTTP exchange inside the
// shared client, including redirects and provider compatibility fallbacks.
// Requests without an attempt recorder use their existing transport directly.
func ObserveHTTPUpstreamRoundTrip(req *http.Request, base http.RoundTripper) (*http.Response, error) {
	if observer, ok := req.Context().Value(upstreamAttemptRoundTripKey{}).(upstreamAttemptRoundTripObserver); ok {
		return observer(req, base)
	}
	return base.RoundTrip(req)
}

func (u *observedHTTPUpstream) trackRoundTrips(req *http.Request, first *UpstreamAttempt, accountID int64) (*http.Request, *atomic.Bool) {
	observed := &atomic.Bool{}
	observer := upstreamAttemptRoundTripObserver(func(actual *http.Request, base http.RoundTripper) (*http.Response, error) {
		attempt := first
		if observed.Swap(true) {
			attempt = u.start(actual, accountID)
		}
		resp, err := base.RoundTrip(actual)
		return u.observe(actual, attempt, resp, err)
	})
	return req.WithContext(context.WithValue(req.Context(), upstreamAttemptRoundTripKey{}, observer)), observed
}
