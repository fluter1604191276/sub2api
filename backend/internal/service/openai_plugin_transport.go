package service

import (
	"errors"
	"net/http"
)

func (s *OpenAIGatewayService) SetPluginManager(manager *PluginManager) {
	s.pluginManager = manager
}

// doOpenAIUpstream 只在 OpenAI OAuth 能力绑定已启用时把真实请求交给插件。
// 插件返回标准 http.Response，响应解析、错误映射、SSE 和计费仍由现有核心链处理。
func (s *OpenAIGatewayService) doOpenAIUpstream(request *http.Request, proxyURL string, account *Account) (*http.Response, error) {
	if s.pluginManager != nil {
		response, handled, err := observeOpenAIPluginRoundTrip(s.httpUpstream, s.pluginManager, request, proxyURL, account)
		if handled {
			return response, err
		}
	}
	return s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
}

// doOpenAIAccountTestUpstream 让 OpenAI OAuth 账号测试与真实转发使用同一插件路径。
// API Key 和未命中插件的账号保持各自原有的 HTTPUpstream 行为。
func (s *AccountTestService) doOpenAIAccountTestUpstream(
	request *http.Request,
	proxyURL string,
	account *Account,
	useTLSFallback bool,
) (*http.Response, error) {
	if s.pluginManager != nil {
		response, handled, err := observeOpenAIPluginRoundTrip(s.httpUpstream, s.pluginManager, request, proxyURL, account)
		if handled {
			return response, err
		}
	}
	if useTLSFallback {
		return s.httpUpstream.DoWithTLS(
			request,
			proxyURL,
			account.ID,
			account.Concurrency,
			s.tlsFPProfileService.ResolveTLSProfile(account),
		)
	}
	return s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
}

func observeOpenAIPluginRoundTrip(upstream HTTPUpstream, manager *PluginManager, request *http.Request, proxyURL string, account *Account) (*http.Response, bool, error) {
	observed, enabled := upstream.(*observedHTTPUpstream)
	var attempt *UpstreamAttempt
	response, handled, err := manager.roundTripOpenAIOAuth(request.Context(), request, proxyURL, account, func() error {
		if enabled {
			attempt = observed.start(request, account.ID)
		}
		return nil
	})
	var transportError *PluginTransportError
	if response == nil && errors.As(err, &transportError) && !transportError.RequestSent {
		return response, handled, err
	}
	if handled && attempt != nil {
		response, err = observed.observe(request, attempt, response, err)
	}
	return response, handled, err
}
