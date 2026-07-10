package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	openAIVideoGenerationsEndpoint = "/v1/video/generations"
	openAIVideosEndpoint           = "/v1/videos"

	openAIVideoGenerationsURL = "https://api.openai.com/v1/video/generations"
	openAIVideosURL           = "https://api.openai.com/v1/videos"
)

type OpenAIVideoGenerationRequest struct {
	Endpoint      string
	Model         string
	ExplicitModel bool
	Prompt        string
	Body          []byte
}

func (s *OpenAIGatewayService) ParseOpenAIVideoGenerationRequest(body []byte) (*OpenAIVideoGenerationRequest, error) {
	if !gjson.ValidBytes(body) {
		return nil, fmt.Errorf("failed to parse request body")
	}
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if model == "" {
		return nil, fmt.Errorf("model is required")
	}
	return &OpenAIVideoGenerationRequest{
		Endpoint:      openAIVideoGenerationsEndpoint,
		Model:         model,
		ExplicitModel: true,
		Prompt:        strings.TrimSpace(gjson.GetBytes(body, "prompt").String()),
		Body:          body,
	}, nil
}

func (r *OpenAIVideoGenerationRequest) ModerationBody() []byte {
	if r == nil || strings.TrimSpace(r.Prompt) == "" {
		return nil
	}
	body, err := json.Marshal(map[string]any{"prompt": strings.TrimSpace(r.Prompt)})
	if err != nil {
		return nil
	}
	return body
}

func (s *OpenAIGatewayService) ForwardVideoGeneration(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	parsed *OpenAIVideoGenerationRequest,
	channelMappedModel string,
) (*OpenAIForwardResult, error) {
	if parsed == nil {
		return nil, fmt.Errorf("parsed video generation request is required")
	}
	if account == nil {
		return nil, fmt.Errorf("account is required")
	}
	if account.Type != AccountTypeAPIKey {
		return nil, fmt.Errorf("unsupported account type for video generation: %s", account.Type)
	}

	startTime := time.Now()
	requestModel := strings.TrimSpace(parsed.Model)
	if mapped := strings.TrimSpace(channelMappedModel); mapped != "" {
		requestModel = mapped
	}
	upstreamModel := account.GetMappedModel(requestModel)
	forwardBody := body
	if strings.TrimSpace(upstreamModel) != "" {
		forwardBody = ReplaceModelInBody(body, upstreamModel)
	}

	upstreamCtx, releaseUpstreamCtx := detachStreamUpstreamContext(ctx, false)
	defer releaseUpstreamCtx()

	token, _, err := s.GetAccessToken(upstreamCtx, account)
	if err != nil {
		return nil, err
	}
	upstreamReq, err := s.buildOpenAIVideoGenerationRequest(upstreamCtx, c, account, forwardBody, token)
	if err != nil {
		return nil, err
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	upstreamStart := time.Now()
	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	if err != nil {
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		setOpsUpstreamError(c, 0, safeErr, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:    account.Platform,
			AccountID:   account.ID,
			AccountName: account.Name,
			UpstreamURL: safeUpstreamURL(upstreamReq.URL.String()),
			Kind:        "request_error",
			Message:     safeErr,
		})
		return nil, fmt.Errorf("upstream request failed: %s", safeErr)
	}
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
		upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
		if s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, respBody) {
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.Header.Get("x-request-id"),
				UpstreamURL:        safeUpstreamURL(upstreamReq.URL.String()),
				Kind:               "failover",
				Message:            upstreamMsg,
			})
			s.handleFailoverSideEffects(upstreamCtx, resp, account, respBody, upstreamModel)
			return nil, &UpstreamFailoverError{
				StatusCode:             resp.StatusCode,
				ResponseBody:           respBody,
				RetryableOnSameAccount: account.IsPoolMode() && isPoolModeRetryableStatus(resp.StatusCode),
			}
		}
		return s.handleErrorResponse(upstreamCtx, resp, c, account, forwardBody)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := s.writeOpenAIVideoResponse(resp, c)
	if err != nil {
		return nil, err
	}
	requestID := strings.TrimSpace(resp.Header.Get("x-request-id"))
	if requestID == "" {
		requestID = firstJSONText(bodyBytes, "task_id", "id")
	}
	return &OpenAIForwardResult{
		RequestID:       requestID,
		Model:           requestModel,
		BillingModel:    requestModel,
		UpstreamModel:   upstreamModel,
		ResponseHeaders: resp.Header.Clone(),
		Duration:        time.Since(startTime),
		// Reuse the existing per-request media billing path for video tasks.
		// usage_logs currently requires image_size whenever image_count > 0.
		ImageCount: 1,
		ImageSize:  "1K",
	}, nil
}

func (s *OpenAIGatewayService) ForwardVideoTask(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	taskID string,
) error {
	if account == nil {
		return fmt.Errorf("account is required")
	}
	if account.Type != AccountTypeAPIKey {
		return fmt.Errorf("unsupported account type for video task query: %s", account.Type)
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return fmt.Errorf("task_id is required")
	}
	upstreamCtx, releaseUpstreamCtx := detachStreamUpstreamContext(ctx, false)
	defer releaseUpstreamCtx()

	token, _, err := s.GetAccessToken(upstreamCtx, account)
	if err != nil {
		return err
	}
	upstreamReq, err := s.buildOpenAIVideoTaskRequest(upstreamCtx, c, account, taskID, token)
	if err != nil {
		return err
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	upstreamStart := time.Now()
	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	if err != nil {
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		setOpsUpstreamError(c, 0, safeErr, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:    account.Platform,
			AccountID:   account.ID,
			AccountName: account.Name,
			UpstreamURL: safeUpstreamURL(upstreamReq.URL.String()),
			Kind:        "request_error",
			Message:     safeErr,
		})
		return fmt.Errorf("upstream request failed: %s", safeErr)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return s.handleOpenAIVideoTaskError(upstreamCtx, resp, c, account)
	}
	_, err = s.writeOpenAIVideoResponse(resp, c)
	return err
}

func (s *OpenAIGatewayService) buildOpenAIVideoGenerationRequest(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	token string,
) (*http.Request, error) {
	targetURL := openAIVideoGenerationsURL
	if baseURL := account.GetOpenAIBaseURL(); baseURL != "" {
		validatedURL, err := s.validateUpstreamBaseURL(baseURL)
		if err != nil {
			return nil, err
		}
		targetURL = buildOpenAIVideoURL(validatedURL, openAIVideoGenerationsEndpoint)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	for key, values := range c.Request.Header {
		if !openaiPassthroughAllowedHeaders[strings.ToLower(key)] {
			continue
		}
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if customUA := account.GetOpenAIUserAgent(); customUA != "" {
		req.Header.Set("User-Agent", customUA)
	}
	return req, nil
}

func (s *OpenAIGatewayService) buildOpenAIVideoTaskRequest(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	taskID string,
	token string,
) (*http.Request, error) {
	targetURL := strings.TrimRight(openAIVideosURL, "/") + "/" + url.PathEscape(taskID)
	if baseURL := account.GetOpenAIBaseURL(); baseURL != "" {
		validatedURL, err := s.validateUpstreamBaseURL(baseURL)
		if err != nil {
			return nil, err
		}
		targetURL = strings.TrimRight(buildOpenAIVideoURL(validatedURL, openAIVideosEndpoint), "/") + "/" + url.PathEscape(taskID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	for key, values := range c.Request.Header {
		if !openaiPassthroughAllowedHeaders[strings.ToLower(key)] {
			continue
		}
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if customUA := account.GetOpenAIUserAgent(); customUA != "" {
		req.Header.Set("User-Agent", customUA)
	}
	return req, nil
}

func buildOpenAIVideoURL(base string, endpoint string) string {
	normalized := strings.TrimRight(strings.TrimSpace(base), "/")
	relative := strings.TrimPrefix(strings.TrimSpace(endpoint), "/v1")
	if strings.HasSuffix(normalized, endpoint) || strings.HasSuffix(normalized, relative) {
		return normalized
	}
	if strings.HasSuffix(normalized, "/v1") {
		return normalized + relative
	}
	return normalized + endpoint
}

func (s *OpenAIGatewayService) writeOpenAIVideoResponse(resp *http.Response, c *gin.Context) ([]byte, error) {
	body, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	contentType := "application/json"
	if s.cfg != nil && !s.cfg.Security.ResponseHeaders.Enabled {
		if upstreamType := resp.Header.Get("Content-Type"); upstreamType != "" {
			contentType = upstreamType
		}
	}
	c.Data(resp.StatusCode, contentType, body)
	return body, nil
}

func (s *OpenAIGatewayService) handleOpenAIVideoTaskError(ctx context.Context, resp *http.Response, c *gin.Context, account *Account) error {
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(respBody))
	upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
	upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  resp.Header.Get("x-request-id"),
		Kind:               "upstream_error",
		Message:            upstreamMsg,
	})
	_, err := s.handleErrorResponse(ctx, resp, c, account, nil)
	return err
}

func firstJSONText(body []byte, paths ...string) string {
	for _, path := range paths {
		value := strings.TrimSpace(gjson.GetBytes(body, path).String())
		if value != "" {
			return value
		}
	}
	return ""
}
