//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Exercise the production transport and dial guard unchanged. No replacement
// HTTP client, resolver, allowlist or network security setting is used.
func TestChannelMonitorSSRFTransportRejectsPrivateTargets(t *testing.T) {
	for _, target := range []string{
		"https://127.0.0.1:443/v1", "https://[::1]:443/v1",
		"https://[::ffff:127.0.0.1]:443/v1", "https://10.0.0.1/v1",
		"https://172.16.0.1/v1", "https://192.168.0.1/v1",
		"https://169.254.169.254/latest/meta-data", "https://100.64.0.1/v1",
		"https://[fd00::1]/v1", "https://localhost/v1",
		"https://metadata.google.internal/v1", "https://INSTANCE-DATA/v1",
	} {
		t.Run(target, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
			require.NoError(t, err)
			res, err := newSSRFSafeHTTPClient(time.Second).Do(req)
			require.Nil(t, res)
			require.ErrorContains(t, err, "blocked by SSRF policy")
		})
	}
}
