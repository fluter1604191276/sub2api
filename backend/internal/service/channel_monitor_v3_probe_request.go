package service

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/google/uuid"
)

const (
	channelMonitorV3RequestHeader  = "X-Sub2API-Channel-Monitor-V3"
	channelMonitorV3DispatchHeader = "X-Sub2API-Channel-Monitor-V3-Dispatch"
)

type channelMonitorV3RequestContextKey struct{}

func IsChannelMonitorV3Request(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	marked, _ := ctx.Value(channelMonitorV3RequestContextKey{}).(bool)
	return marked
}

// MarkRequest strips internal headers on every request and only marks a token
// authenticated loopback dispatch. It does not bypass regular gateway auth,
// scheduling, or billing.
func (r *ChannelMonitorV3ProbeRunner) MarkRequest(req *http.Request) *http.Request {
	if req == nil {
		return nil
	}
	token := req.Header.Get(channelMonitorV3RequestHeader)
	dispatch := req.Header.Get(channelMonitorV3DispatchHeader)
	req.Header.Del(channelMonitorV3RequestHeader)
	req.Header.Del(channelMonitorV3DispatchHeader)
	// Legacy monitors already use this user-agent family for usage filtering.
	// Keep failed attempts consistent with that classification. This hint grants
	// no V3 trust or budget/profit privileges.
	if strings.Contains(strings.ToLower(req.UserAgent()), "sub2api-channel-monitor/") {
		req = req.WithContext(WithUpstreamAttemptSource(req.Context(), "channel_monitor"))
	}
	if r == nil || len(r.requestToken) == 0 || token == "" || subtle.ConstantTimeCompare([]byte(token), r.requestToken) != 1 {
		return req
	}
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return req
	}
	id, err := uuid.Parse(strings.TrimSpace(dispatch))
	if err != nil || id.String() != strings.TrimSpace(dispatch) {
		return req
	}
	guard, ok := r.requests.Load(id.String())
	if !ok {
		// An expired trusted dispatch remains guarded even if its loopback
		// handler starts after the caller has already timed out.
		guard = &channelMonitorV3AttemptGuard{now: r.now}
	}
	ctx := context.WithValue(req.Context(), channelMonitorV3RequestContextKey{}, true)
	ctx = context.WithValue(ctx, channelMonitorV3AttemptGuardKey{}, guard)
	ctx = WithUpstreamAttemptSource(ctx, "channel_monitor_v3")
	ctx = context.WithValue(ctx, ctxkey.ClientRequestID, "cmv3-"+id.String())
	return req.WithContext(ctx)
}
