//go:build unit

package server

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A CONNECT response ends HTTP use of this connection, including a rejection.
// Exercise Go's actual server rather than ResponseRecorder: a handler that does
// not hijack the socket must not process the following synthetic HTTP request.
// This is a bounded loopback compatibility regression, not a tunnel or load test.
func TestHTTPServerCONNECTDoesNotReuseConnection(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var connectCalls, subsequentCalls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodConnect {
					connectCalls.Add(1)
					w.WriteHeader(status)
					return
				}
				subsequentCalls.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()
			defer srv.CloseClientConnections()

			conn, err := net.DialTimeout("tcp", srv.Listener.Addr().String(), time.Second)
			require.NoError(t, err)
			defer func() { _ = conn.Close() }()
			require.NoError(t, conn.SetDeadline(time.Now().Add(3*time.Second)))
			_, err = io.WriteString(conn, "CONNECT fixture.invalid:443 HTTP/1.1\r\nHost: fixture.invalid:443\r\n\r\n"+
				"GET /after-connect HTTP/1.1\r\nHost: fixture.invalid\r\n\r\n")
			require.NoError(t, err)

			// Read to EOF with a hard size bound. A timeout is a failure: the
			// socket must close without waiting for the client to close it.
			wire, err := io.ReadAll(io.LimitReader(conn, 4097))
			require.NoError(t, err, "CONNECT response must close the HTTP connection")
			require.LessOrEqual(t, len(wire), 4096, "response exceeds the bounded fixture")
			resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(wire)), &http.Request{Method: http.MethodConnect})
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			require.Equal(t, status, resp.StatusCode)
			if status == http.StatusForbidden {
				require.True(t, resp.Close)
			}
			require.Equal(t, 1, bytes.Count(wire, []byte("HTTP/1.1 ")), "no response to a pipelined request")
			require.Equal(t, int32(1), connectCalls.Load())
			require.Zero(t, subsequentCalls.Load(), "GET after CONNECT must never reach the handler")
		})
	}
}
