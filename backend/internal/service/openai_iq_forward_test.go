package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIIQForwardStopsRejectedFieldReplay(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, iq := range []bool{false, true} {
			name := "native"
			if passthrough {
				name = "passthrough"
			}
			if iq {
				name += "/iq"
			} else {
				name += "/ordinary"
			}
			t.Run(name, func(t *testing.T) {
				body := []byte(`{"model":"gpt-6-astra","stream":true,"instructions":"test","input":[{"type":"message","role":"user","status":"completed","content":"hello"}]}`)
				upstream := &httpUpstreamRecorder{responses: []*http.Response{
					newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, `{"error":{"code":"unknown_parameter","message":"Unknown parameter: 'input[0].status'.","param":"input[0].status"}}`),
					newOpenAIRejectedFieldTestResponse(http.StatusOK, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ok\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\ndata: [DONE]\n\n"),
				}}
				upstream.responses[1].Header.Set("Content-Type", "text/event-stream")
				svc := newOpenAIRejectedFieldTestService(upstream)
				c, _ := newOpenAIAstraProRetryContext(body)
				account := newOpenAIOAuthNamespaceTestAccount()
				account.Extra = map[string]any{"openai_passthrough": passthrough}
				ctx := context.Background()
				if iq {
					ctx = context.WithValue(ctx, openAIIQRequestContextKey{}, true)
				}
				c.Request = c.Request.WithContext(ctx)
				_, err := svc.Forward(ctx, c, account, body)
				if iq {
					require.Error(t, err)
					require.Len(t, upstream.requests, 1)
				} else {
					require.NoError(t, err)
					require.Len(t, upstream.requests, 2)
				}
			})
		}
	}
}
