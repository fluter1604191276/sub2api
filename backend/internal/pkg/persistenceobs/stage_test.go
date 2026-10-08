package persistenceobs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestStageDiagnosticsDoNotEmitDynamicData(t *testing.T) {
	var out bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := &pq.Error{Code: "57014", Message: "secret-token SQL user-identity request-parameters", Detail: "secret-detail"}
	Observe(Billing, Apply, time.Now(), ctx, fmt.Errorf("secret-wrapper: %w", err))
	require.Contains(t, out.String(), `"operation":"usage_billing"`)
	require.Contains(t, out.String(), `"error_class":"postgres_query_canceled"`)
	require.Contains(t, out.String(), `"context_state":"canceled"`)
	for _, sensitive := range []string{"secret", "user-identity", "request-parameters", "SQL", "57014"} {
		require.NotContains(t, out.String(), sensitive)
	}
	out.Reset()
	Observe(Operation("secret-operation"), Phase("secret-phase"), time.Now(), nil, errors.New("secret-error"))
	require.NotContains(t, out.String(), "secret")
	require.Contains(t, out.String(), `"operation":"other"`)
	require.Contains(t, out.String(), `"phase":"other"`)
}

func TestStageErrorClasses(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, "none"}, {context.Canceled, "canceled"}, {fmt.Errorf("wrap: %w", context.DeadlineExceeded), "deadline_exceeded"},
		{&pq.Error{Code: "40P01"}, "postgres_deadlock"}, {&pq.Error{Code: "53300"}, "postgres_connection_limit"},
		{&pq.Error{Code: "23505", Message: "secret"}, "postgres_other"}, {errors.New("secret"), "other"},
	} {
		require.Equal(t, tc.want, errorClass(tc.err))
	}
}
