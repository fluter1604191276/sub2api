package handler

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Response diagnostics are intentionally on a separate bounded queue from
// error logs: normal completed Responses traffic must not starve failure
// telemetry. The queue drops under pressure instead of delaying a client.
const (
	opsResponseDiagnosticQueueSize = 2048
	opsResponseDiagnosticWorkers   = 2
	opsResponseDiagnosticTimeout   = 2 * time.Second
	opsResponseDiagnosticDrainWait = 10 * time.Second
)

type opsResponseDiagnosticJob struct {
	ops   *service.OpsService
	entry *service.OpsResponseDiagnostic
}

var (
	opsResponseDiagnosticOnce          sync.Once
	opsResponseDiagnosticStop          sync.Once
	opsResponseDiagnosticQ             chan opsResponseDiagnosticJob
	opsResponseDiagnosticWg            sync.WaitGroup
	opsResponseDiagnosticMu            sync.RWMutex
	opsResponseDiagnosticStopRequested bool
	opsResponseDiagnosticEnqueued      atomic.Int64
	opsResponseDiagnosticDropped       atomic.Int64
	opsResponseDiagnosticPersisted     atomic.Int64
	opsResponseDiagnosticPersistFailed atomic.Int64
	opsResponseDiagnosticLastDropLog   atomic.Int64
	opsResponseDiagnosticLastFailLog   atomic.Int64
)

func startOpsResponseDiagnosticWorkers() {
	opsResponseDiagnosticMu.Lock()
	defer opsResponseDiagnosticMu.Unlock()
	if opsResponseDiagnosticStopRequested {
		return
	}
	opsResponseDiagnosticQ = make(chan opsResponseDiagnosticJob, opsResponseDiagnosticQueueSize)
	for range opsResponseDiagnosticWorkers {
		opsResponseDiagnosticWg.Add(1)
		go func() {
			defer opsResponseDiagnosticWg.Done()
			for job := range opsResponseDiagnosticQ {
				if job.ops == nil || job.entry == nil {
					continue
				}
				ctx, cancel := context.WithTimeout(context.Background(), opsResponseDiagnosticTimeout)
				if err := job.ops.InsertResponseDiagnostic(ctx, job.entry); err != nil {
					opsResponseDiagnosticPersistFailed.Add(1)
					maybeLogOpsResponseDiagnosticPersistenceFailure()
				} else {
					opsResponseDiagnosticPersisted.Add(1)
				}
				cancel()
			}
		}()
	}
}

func enqueueOpsResponseDiagnostic(ops *service.OpsService, entry *service.OpsResponseDiagnostic) {
	if ops == nil || entry == nil {
		return
	}
	validated := *entry
	if err := service.NormalizeResponseDiagnostic(&validated); err != nil {
		return
	}
	opsResponseDiagnosticOnce.Do(startOpsResponseDiagnosticWorkers)
	opsResponseDiagnosticMu.RLock()
	defer opsResponseDiagnosticMu.RUnlock()
	if opsResponseDiagnosticStopRequested || opsResponseDiagnosticQ == nil {
		return
	}
	select {
	case opsResponseDiagnosticQ <- opsResponseDiagnosticJob{ops: ops, entry: &validated}:
		opsResponseDiagnosticEnqueued.Add(1)
	default:
		// Deliberately drop rather than blocking the request path. The error-log
		// pipeline remains independent and retains its own counters/guarantees.
		opsResponseDiagnosticDropped.Add(1)
		maybeLogOpsResponseDiagnosticDrop()
	}
}

// StopOpsResponseDiagnosticWorkers is available to the server lifecycle and
// tests. It tries to drain accepted work before returning, bounded by the
// server shutdown deadline.
func StopOpsResponseDiagnosticWorkers() {
	opsResponseDiagnosticStop.Do(func() {
		opsResponseDiagnosticMu.Lock()
		opsResponseDiagnosticStopRequested = true
		q := opsResponseDiagnosticQ
		if q != nil {
			close(q)
		}
		opsResponseDiagnosticMu.Unlock()
		drained := make(chan struct{})
		go func() {
			opsResponseDiagnosticWg.Wait()
			close(drained)
		}()
		select {
		case <-drained:
		case <-time.After(opsResponseDiagnosticDrainWait):
			log.Printf("[OpsResponseDiagnostic] shutdown drain timed out")
		}
	})
}

func OpsResponseDiagnosticEnqueuedTotal() int64  { return opsResponseDiagnosticEnqueued.Load() }
func OpsResponseDiagnosticDroppedTotal() int64   { return opsResponseDiagnosticDropped.Load() }
func OpsResponseDiagnosticPersistedTotal() int64 { return opsResponseDiagnosticPersisted.Load() }
func OpsResponseDiagnosticPersistFailuresTotal() int64 {
	return opsResponseDiagnosticPersistFailed.Load()
}

func maybeLogOpsResponseDiagnosticDrop() {
	now := time.Now().UnixNano()
	previous := opsResponseDiagnosticLastDropLog.Load()
	if previous != 0 && now-previous < int64(time.Minute) {
		return
	}
	if opsResponseDiagnosticLastDropLog.CompareAndSwap(previous, now) {
		log.Printf("[OpsResponseDiagnostic] queue full; dropped metadata record")
	}
}

func maybeLogOpsResponseDiagnosticPersistenceFailure() {
	now := time.Now().UnixNano()
	previous := opsResponseDiagnosticLastFailLog.Load()
	if previous != 0 && now-previous < int64(time.Minute) {
		return
	}
	if opsResponseDiagnosticLastFailLog.CompareAndSwap(previous, now) {
		// Do not include DB error text; diagnostics metadata must not become a
		// route for arbitrary values into application logs.
		log.Printf("[OpsResponseDiagnostic] persistence failed")
	}
}

// recordOpsResponseDiagnostic must run after c.Next(). Unlike ops_error_logs,
// it records terminal protocol metadata even when the HTTP status is 200.
func recordOpsResponseDiagnostic(c *gin.Context, ops *service.OpsService) {
	if c == nil || ops == nil || c.Request == nil || !ops.IsMonitoringEnabled(c.Request.Context()) {
		return
	}
	snapshot, ok := service.OpenAIResponseDiagnosticSnapshotFromContext(c)
	if !ok || !snapshot.Active {
		return
	}
	entry := buildOpsResponseDiagnostic(c, snapshot)
	if entry == nil {
		return
	}
	enqueueOpsResponseDiagnostic(ops, entry)
}

// buildOpsResponseDiagnostic projects request-scoped scalar observation into
// storage input. It has no side effects, which keeps status precedence and ID
// attribution independently testable.
func buildOpsResponseDiagnostic(c *gin.Context, snapshot service.OpenAIResponseDiagnosticSnapshot) *service.OpsResponseDiagnostic {
	if c == nil || c.Request == nil {
		return nil
	}

	requestID, _ := c.Request.Context().Value(ctxkey.RequestID).(string)
	if requestID == "" {
		requestID = c.Writer.Header().Get("X-Request-Id")
	}
	if requestID == "" {
		return nil
	}
	clientRequestID, _ := c.Request.Context().Value(ctxkey.ClientRequestID).(string)

	entry := &service.OpsResponseDiagnostic{
		RequestID:                requestID,
		ClientRequestID:          clientRequestID,
		UpstreamRequestID:        snapshot.UpstreamRequestID,
		ResponseID:               snapshot.ResponseID,
		Model:                    c.GetString(opsModelKey),
		Stream:                   c.GetBool(opsStreamKey),
		ClientDisconnected:       snapshot.ClientDisconnected,
		HTTPStatus:               c.Writer.Status(),
		RequestedMaxOutputTokens: snapshot.RequestedMaxOutputTokens,
		ForwardedMaxOutputTokens: snapshot.ForwardedMaxOutputTokens,
		UpstreamMaxOutputTokens:  snapshot.UpstreamMaxOutputTokens,
		InputTokens:              snapshot.InputTokens,
		CacheReadTokens:          snapshot.CacheReadTokens,
		CacheCreationTokens:      snapshot.CacheCreationTokens,
		OutputTokens:             snapshot.OutputTokens,
		ReasoningTokens:          snapshot.ReasoningTokens,
	}
	// Only the original client request context is considered here. Detached
	// upstream contexts may end during normal retry/cleanup and must not be
	// classified as a client disconnect.
	if c.Request.Context().Err() == context.Canceled {
		entry.ClientDisconnected = true
	}
	if snapshot.Terminal {
		entry.Status = snapshot.Status
		entry.Reason = snapshot.Reason
	} else if entry.ClientDisconnected {
		entry.Status = "client_disconnected"
	} else if entry.HTTPStatus >= 400 {
		entry.Status = "failed"
	} else {
		entry.Status = "missing_terminal"
	}
	if entry.Status == "" {
		entry.Status = "missing_terminal"
	}
	if snapshot.AccountID > 0 {
		id := snapshot.AccountID
		entry.AccountID = &id
	}
	if apiKey := getOpsAPIKey(c); apiKey != nil {
		if apiKey.User != nil && apiKey.User.ID > 0 {
			id := apiKey.User.ID
			entry.UserID = &id
		}
		if apiKey.Group != nil && apiKey.Group.ID > 0 {
			id := apiKey.Group.ID
			entry.GroupID = &id
		}
	}
	return entry
}
