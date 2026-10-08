// Package persistenceobs records bounded persistence diagnostics without request
// identities, monetary values, SQL text, or dynamic error messages.
package persistenceobs

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

type Operation string
type Phase string

const (
	Billing             Operation = "usage_billing"
	Log                 Operation = "usage_log"
	Apply               Phase     = "apply"
	BatchInsert         Phase     = "batch_insert"
	SingleFallback      Phase     = "single_fallback"
	BestEffort          Phase     = "best_effort"
	SyncFallback        Phase     = "sync_fallback"
	SyncFallbackSkipped Phase     = "sync_fallback_skipped_no_key"
)

func Observe(operation Operation, phase Phase, started time.Time, ctx context.Context, err error) {
	level := slog.LevelDebug
	outcome := "success"
	if err != nil {
		level = slog.LevelWarn
		outcome = "error"
	}
	contextState := "active"
	if ctx != nil && ctx.Err() != nil {
		contextState = errorClass(ctx.Err())
	}
	// A background context also prevents a handler from attaching request IDs
	// carried in the caller's context to this deliberately anonymous record.
	slog.LogAttrs(context.Background(), level, "persistence_stage",
		slog.String("operation", safeOperation(operation)),
		slog.String("phase", safePhase(phase)),
		slog.String("outcome", outcome),
		slog.String("error_class", errorClass(err)),
		slog.String("context_state", contextState),
		slog.Int64("elapsed_ms", max(0, time.Since(started).Milliseconds())),
	)
}

func safeOperation(operation Operation) string {
	switch operation {
	case Billing, Log:
		return string(operation)
	default:
		return "other"
	}
}

func safePhase(phase Phase) string {
	switch phase {
	case Apply, BatchInsert, SingleFallback, BestEffort, SyncFallback, SyncFallbackSkipped:
		return string(phase)
	default:
		return "other"
	}
}

func errorClass(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		switch state.SQLState() {
		case "57014":
			return "postgres_query_canceled"
		case "40P01":
			return "postgres_deadlock"
		case "53300":
			return "postgres_connection_limit"
		default:
			return "postgres_other"
		}
	}
	return "other"
}
