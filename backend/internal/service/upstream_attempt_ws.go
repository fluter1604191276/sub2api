package service

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// upstreamAttemptWebSocketObserver records one already-sent Responses WebSocket
// request. It deliberately shares only the HTTP ledger's parser and recorder,
// never its transport or billing path.
type upstreamAttemptWebSocketObserver struct {
	recorder *UpstreamAttemptRecorder
	body     upstreamAttemptBody
	once     sync.Once
}

func (s *OpenAIGatewayService) startUpstreamAttemptWebSocket(ctx context.Context, c *gin.Context, account *Account, model, serviceTier, reasoningEffort string) *upstreamAttemptWebSocketObserver {
	if s == nil || account == nil {
		return nil
	}
	observed, ok := s.httpUpstream.(*observedHTTPUpstream)
	if !ok || observed.recorder == nil {
		return nil
	}
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// A WebSocket write commonly uses a short-lived timeout context. Keep the
	// request values but detach cancellation before snapshotting price metadata,
	// so a write timeout cannot erase the attempt's price identity.
	ctx = context.WithoutCancel(ctx)
	a := &UpstreamAttempt{
		AttemptID:       uuid.NewString(),
		AccountID:       account.ID,
		CreatedAt:       time.Now().UTC(),
		Protocol:        "responses",
		Source:          "request",
		Outcome:         "unknown",
		Model:           strings.TrimSpace(model),
		ServiceTier:     strings.TrimSpace(serviceTier),
		ReasoningEffort: strings.TrimSpace(reasoningEffort),
	}
	a.RequestID, _ = ctx.Value(ctxkey.RequestID).(string)
	a.ClientRequestID, _ = ctx.Value(ctxkey.ClientRequestID).(string)
	if id, ok := ctx.Value(ctxkey.UserID).(int64); ok && id > 0 {
		a.UserID = &id
	}
	if group, ok := ctx.Value(ctxkey.Group).(*Group); ok && group != nil {
		id := group.ID
		a.GroupID = &id
	}
	if source, ok := ctx.Value(upstreamAttemptSourceKey{}).(string); ok {
		a.Source = source
	} else if IsOpenAIIQRequest(ctx) {
		a.Source = "openai_iq"
	}
	if observed.recorder.cost != nil {
		snapshotCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := observed.recorder.cost.Snapshot(snapshotCtx, a)
		cancel()
		if err != nil {
			// A failed snapshot is represented as unknown pricing and must not alter
			// the successful WebSocket request.
			a.CostSnapshotAvailable = false
		}
	}
	return &upstreamAttemptWebSocketObserver{
		recorder: observed.recorder,
		body: upstreamAttemptBody{
			ctx:      ctx,
			recorder: observed.recorder,
			attempt:  a,
			stream:   true,
		},
	}
}

func (o *upstreamAttemptWebSocketObserver) observe(frame []byte) {
	if o == nil || o.body.attempt == nil {
		return
	}
	o.body.observeFrame(frame)
}

// setUpstreamRequestID records only an ID supplied by the upstream handshake.
// A Responses response.id identifies the generated response, not this upstream
// request, and must not be stored in the request-ID column.
func (o *upstreamAttemptWebSocketObserver) setUpstreamRequestID(headers http.Header) {
	if o == nil || o.body.attempt == nil {
		return
	}
	for _, header := range []string{"x-request-id", "request-id", "x-amzn-requestid", "x-goog-request-id"} {
		if id := strings.TrimSpace(headers.Get(header)); id != "" {
			o.body.attempt.UpstreamRequestID = id
			return
		}
	}
}

func (o *upstreamAttemptWebSocketObserver) finish() {
	if o == nil || o.recorder == nil || o.body.attempt == nil {
		return
	}
	o.once.Do(func() {
		o.body.finish(false)
		o.recorder.record(o.body.ctx, o.body.attempt)
	})
}
