package service

import (
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const openAIResponsesPartialStreamEndedMessage = "Responses stream ended after partial output; request was not replayed"

// openAIResponsesEmptyCompletionApplies reports whether this account's
// Responses stream should treat an empty completion or a pre-output disconnect
// as a supplier failure. Native DeepSeek/Kimi/MiniMax Responses traffic shares
// the OpenAI handler and was previously outside the OpenAI-only gate.
func openAIResponsesEmptyCompletionApplies(account *Account) bool {
	if account == nil {
		return false
	}
	switch account.Platform {
	case PlatformOpenAI, PlatformGrok, PlatformDeepseek:
		return true
	default:
		return account.UsesNativeCNResponses()
	}
}

func (s *OpenAIGatewayService) failOpenAIResponsesEmptyStream(c *gin.Context, account *Account, upstreamRequestID, model string) *UpstreamFailoverError {
	s.noteOpenAIEmptyStreamFailure(c, account, model, upstreamRequestID)
	s.clearOpenAIStreamSticky(c)
	return newOpenAIResponsesEmptyCompletedFailoverError(c, account, upstreamRequestID)
}

// noteOpenAIEmptyStreamFailure records one real empty/abnormal Responses
// result against the existing account+normalized-model breaker. Probe traffic,
// client cancels, and non-supplier terminals are ignored. The same request and
// account are counted once.
func (s *OpenAIGatewayService) noteOpenAIEmptyStreamFailure(c *gin.Context, account *Account, model, upstreamRequestID string) {
	if s == nil || account == nil || !openAIEmptyStreamCountsForBreaker(c) {
		return
	}
	state := s.getOpenAIAccountModelTransientState()
	if state == nil {
		return
	}
	canonical := canonicalOpenAIAccountSchedulingModel(account, model)
	state.recordEmptyStream(account.ID, openAIAccountModelTransientModel(canonical), openAIEmptyStreamDedupeKey(c, upstreamRequestID), time.Now())
}

func openAIEmptyStreamCountsForBreaker(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return true
	}
	if c.Request.Context() != nil && c.Request.Context().Err() != nil {
		return false
	}
	ua := strings.ToLower(c.GetHeader("User-Agent"))
	if strings.Contains(ua, "sub2api-recovery-probe") || strings.Contains(ua, "sub2api-channel-monitor") {
		return false
	}
	return true
}

func openAIEmptyStreamDedupeKey(c *gin.Context, upstreamRequestID string) string {
	if c != nil && c.Request != nil && c.Request.Context() != nil {
		if requestID, _ := c.Request.Context().Value(ctxkey.RequestID).(string); strings.TrimSpace(requestID) != "" {
			return strings.TrimSpace(requestID)
		}
	}
	if requestID := strings.TrimSpace(upstreamRequestID); requestID != "" {
		return "upstream:" + requestID
	}
	return ""
}

func (s *OpenAIGatewayService) clearOpenAIStreamSticky(c *gin.Context) {
	if s == nil || c == nil || c.Request == nil {
		return
	}
	apiKey := getAPIKeyFromContext(c)
	var groupID *int64
	if apiKey != nil {
		groupID = apiKey.GroupID
	}
	if sessionHash := openAIStreamRecoverySession(c, s); sessionHash != "" {
		_ = s.deleteStickySessionAccountIDAllFormats(c.Request.Context(), groupID, sessionHash)
	}
}

func openAIResponsesEventExcludedFromEmptyStream(data []byte) bool {
	if isOpenAIContextWindowError("", data) {
		return true
	}
	reason := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		gjson.GetBytes(data, "response.incomplete_details.reason").String(),
		gjson.GetBytes(data, "incomplete_details.reason").String(),
	)))
	switch reason {
	case "max_output_tokens", "max_tokens", "length", "content_filter":
		return true
	}
	for _, path := range []string{
		"response.error.code",
		"error.code",
		"response.error.message",
		"error.message",
		"response.status_details.reason",
	} {
		text := strings.ToLower(gjson.GetBytes(data, path).String())
		if text == "" {
			continue
		}
		if strings.Contains(text, "content_filter") || strings.Contains(text, "content filter") {
			return true
		}
	}
	return false
}

func openAIResponsesPayloadUsageHasTokens(data []byte) bool {
	for _, path := range []string{"usage", "response.usage"} {
		usage := gjson.GetBytes(data, path)
		if !usage.Exists() || !usage.IsObject() {
			continue
		}
		parsed, ok := openAIUsageFromGJSON(usage)
		if ok && openAIUsageHasTokens(&parsed) {
			return true
		}
	}
	return false
}

func openAIResponsesPayloadHasSemanticOutput(data []byte) bool {
	for _, path := range []string{"response.output", "output"} {
		output := gjson.GetBytes(data, path)
		if !output.Exists() || !output.IsArray() {
			continue
		}
		for _, item := range output.Array() {
			if openAIResponsesOutputItemIsSemantic(item) {
				return true
			}
		}
	}
	return false
}

func openAIResponsesOutputItemIsSemantic(item gjson.Result) bool {
	switch strings.TrimSpace(item.Get("type").String()) {
	case "", "message":
		return openAIResponsesMessageHasSemanticContent(item)
	case "reasoning":
		return openAIResponsesReasoningIsSemantic(item)
	default:
		// function_call, custom tools, image/audio/file items and other
		// non-message types are legitimate non-text output.
		return strings.TrimSpace(item.Get("type").String()) != ""
	}
}

func openAIResponsesMessageHasSemanticContent(item gjson.Result) bool {
	if text := strings.TrimSpace(item.Get("text").String()); text != "" {
		return true
	}
	content := item.Get("content")
	if !content.Exists() || !content.IsArray() {
		return false
	}
	for _, part := range content.Array() {
		if strings.TrimSpace(part.Get("text").String()) != "" || strings.TrimSpace(part.Get("refusal").String()) != "" {
			return true
		}
		partType := strings.TrimSpace(part.Get("type").String())
		switch partType {
		case "", "output_text", "text", "refusal":
			continue
		default:
			return true
		}
	}
	return false
}

func openAIResponsesReasoningIsSemantic(item gjson.Result) bool {
	if strings.TrimSpace(item.Get("encrypted_content").String()) != "" || strings.TrimSpace(item.Get("text").String()) != "" {
		return true
	}
	summary := item.Get("summary")
	if !summary.IsArray() {
		return false
	}
	for _, part := range summary.Array() {
		if strings.TrimSpace(part.Get("text").String()) != "" || strings.TrimSpace(part.Get("encrypted_content").String()) != "" {
			return true
		}
	}
	return false
}
