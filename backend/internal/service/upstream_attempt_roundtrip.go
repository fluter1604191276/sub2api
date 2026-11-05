package service

import (
	"context"
	"net/http"
	"sync/atomic"
)

type upstreamAttemptRoundTripKey struct{}
type upstreamAttemptRoundTripObserver func(*http.Request, http.RoundTripper) (*http.Response, error)

type upstreamAttemptPreparingRoundTripper struct {
	base     http.RoundTripper
	prepares []func(*http.Response)
}

func (t upstreamAttemptPreparingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if resp != nil {
		for _, prepare := range t.prepares {
			if prepare != nil {
				prepare(resp)
			}
		}
	}
	return resp, err
}

// ObserveHTTPUpstreamRoundTrip observes a physical HTTP exchange inside the
// shared client, including redirects and provider compatibility fallbacks.
// Requests without an attempt recorder use their existing transport directly.
// A prepare callback runs after the physical response arrives but before the
// observer installs its body wrapper, so callers can preserve pre-existing
// response transformations such as gzip decompression.
func ObserveHTTPUpstreamRoundTrip(req *http.Request, base http.RoundTripper, prepares ...func(*http.Response)) (*http.Response, error) {
	if observer, ok := req.Context().Value(upstreamAttemptRoundTripKey{}).(upstreamAttemptRoundTripObserver); ok {
		if len(prepares) > 0 {
			base = upstreamAttemptPreparingRoundTripper{base: base, prepares: prepares}
		}
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
