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

const iqRequestHeader = "X-Sub2API-IQ-Request"
const iqDispatchHeader = "X-Sub2API-IQ-Dispatch"

type openAIIQRequestContextKey struct{}

func IsOpenAIIQRequest(ctx context.Context) bool {
	marked, _ := ctx.Value(openAIIQRequestContextKey{}).(bool)
	return marked
}

// MarkIQRequest strips the private header and marks only this process's
// loopback IQ requests. It grants no other privilege.
func (s *OpenAIIQService) MarkIQRequest(req *http.Request) *http.Request {
	if req == nil {
		return nil
	}
	token := req.Header.Get(iqRequestHeader)
	dispatch := req.Header.Get(iqDispatchHeader)
	req.Header.Del(iqRequestHeader)
	req.Header.Del(iqDispatchHeader)
	if s == nil || len(s.requestToken) == 0 || token == "" || subtle.ConstantTimeCompare([]byte(token), s.requestToken) != 1 {
		return req
	}
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return req
	}
	ctx := context.WithValue(req.Context(), openAIIQRequestContextKey{}, true)
	if dispatchID, ok := canonicalIQDispatchID(dispatch); ok {
		ctx = context.WithValue(ctx, ctxkey.ClientRequestID, "iq-"+dispatchID)
	}
	return req.WithContext(ctx)
}

func canonicalIQDispatchID(value string) (string, bool) {
	value = strings.TrimSpace(value)
	id, err := uuid.Parse(value)
	if err != nil || id.String() != value {
		return "", false
	}
	return value, true
}
