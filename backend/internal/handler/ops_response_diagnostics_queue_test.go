package handler

import (
	"os"
	"os/exec"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Process isolation keeps the production singleton lifecycle test from changing
// the queue state of unrelated middleware tests in this package.
func TestOpsResponseDiagnosticQueueShutdown(t *testing.T) {
	mode := os.Getenv("SUB2_DIAGNOSTIC_QUEUE_TEST")
	if mode == "" {
		for _, mode := range []string{"before-start", "concurrent"} {
			t.Run(mode, func(t *testing.T) {
				cmd := exec.Command(os.Args[0], "-test.run=^TestOpsResponseDiagnosticQueueShutdown$", "-test.count=1", "-test.timeout=30s")
				cmd.Env = append(os.Environ(), "SUB2_DIAGNOSTIC_QUEUE_TEST="+mode)
				output, err := cmd.CombinedOutput()
				require.NoError(t, err, "%s", output)
			})
		}
		return
	}

	// Monitoring-disabled service: exercise the queue without database access.
	ops := &service.OpsService{}
	entry := &service.OpsResponseDiagnostic{RequestID: "fixture", Status: "completed"}
	if mode == "before-start" {
		StopOpsResponseDiagnosticWorkers()
		enqueueOpsResponseDiagnostic(ops, entry)
		require.Zero(t, OpsResponseDiagnosticEnqueuedTotal())
		return
	}

	enqueueOpsResponseDiagnostic(ops, entry)
	var writers sync.WaitGroup
	start := make(chan struct{})
	for range 16 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			<-start
			for range 100 {
				enqueueOpsResponseDiagnostic(ops, entry)
			}
		}()
	}
	close(start)
	StopOpsResponseDiagnosticWorkers()
	writers.Wait()
	accepted := OpsResponseDiagnosticEnqueuedTotal()
	enqueueOpsResponseDiagnostic(ops, entry)
	require.Equal(t, accepted, OpsResponseDiagnosticEnqueuedTotal())
}
