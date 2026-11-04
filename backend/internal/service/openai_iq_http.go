package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

const iqHTTPMaxBodyBytes = 4 << 20
const iqHTTPTimeout = 120 * time.Second

// iqHTTPClient is deliberately small so IQ's loopback request can be tested
// without changing the service constructor.
type iqHTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

func (s *OpenAIIQService) configureIQHTTP() {
	port := 8080
	host := "127.0.0.1"
	if s != nil && s.cfg != nil {
		if s.cfg.Server.Port > 0 {
			port = s.cfg.Server.Port
		}
		if strings.TrimSpace(s.cfg.Server.Host) == "::1" {
			host = "::1"
		}
	}
	if s == nil {
		return
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		s.client = nil
		s.requestToken = nil
		return
	}
	s.requestToken = []byte(base64.RawURLEncoding.EncodeToString(token))
	s.endpoint = "http://" + net.JoinHostPort(host, fmt.Sprint(port)) + "/v1/responses"
	s.client = &http.Client{
		Timeout: iqHTTPTimeout,
		Transport: &http.Transport{
			Proxy: nil, DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
			ExpectContinueTimeout: time.Second, IdleConnTimeout: 30 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (s *OpenAIIQService) requestIQ(ctx context.Context, key string, body []byte, dispatchID string) (output, responseModel string, err error) {
	if s == nil || s.client == nil || s.endpoint == "" {
		return "", "", errors.New("IQ loopback client unavailable")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", "", errors.New("IQ request unavailable")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", "sub2api-openai-iq")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(iqRequestHeader, string(s.requestToken))
	req.Header.Set(iqDispatchHeader, dispatchID)
	resp, err := s.client.Do(req)
	if err != nil {
		return "", "", errors.New("IQ request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", errors.New("IQ loopback rejected request")
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, iqHTTPMaxBodyBytes+1))
	if len(raw) > iqHTTPMaxBodyBytes {
		return "", "", errors.New("IQ response too large")
	}
	if readErr != nil {
		return "", "", errors.New("IQ response incomplete")
	}
	output, responseModel, err = parseIQResponse(raw, strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream"))
	output = strings.ReplaceAll(output, key, "")
	responseModel = sanitizeIQResponseModel(strings.ReplaceAll(responseModel, key, ""))
	return output, responseModel, err
}

func sanitizeIQResponseModel(model string) string {
	model = strings.TrimSpace(model)
	if !utf8.ValidString(model) {
		return ""
	}
	model = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, model)
	for len(model) > 200 {
		_, width := utf8.DecodeLastRuneInString(model)
		model = model[:len(model)-width]
	}
	return model
}

func parseIQResponse(raw []byte, sse bool) (string, string, error) {
	if !sse {
		return parseIQCompleted(raw)
	}
	var delta, snapshot, model string
	completed := false
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), iqHTTPMaxBodyBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		if !gjson.Valid(data) {
			return cleanIQ(snapshot, delta), model, errors.New("IQ response incomplete")
		}
		v := gjson.Parse(data)
		if !v.IsObject() {
			return cleanIQ(snapshot, delta), model, errors.New("IQ response incomplete")
		}
		typ := v.Get("type").String()
		if completed {
			return cleanIQ(snapshot, delta), model, errors.New("IQ response incomplete")
		}
		status := v.Get("status").String()
		if responseStatus := v.Get("response.status").String(); responseStatus != "" {
			status = responseStatus
		}
		if strings.Contains(typ, "failed") || strings.Contains(typ, "incomplete") || strings.Contains(typ, "error") || status == "failed" || status == "incomplete" || status == "error" {
			return cleanIQ(snapshot, delta), model, errors.New("IQ response failed")
		}
		if m := v.Get("response.model").String(); m != "" {
			model = m
		}
		if m := v.Get("model").String(); m != "" {
			model = m
		}
		if typ == "response.output_text.delta" {
			delta += v.Get("delta").String()
		}
		if typ == "response.completed" {
			if completed || status != "completed" {
				return cleanIQ(snapshot, delta), model, errors.New("IQ response incomplete")
			}
			snapshot = iqOutput(v.Get("response.output"))
			completed = true
		}
	}
	if scanner.Err() != nil || !completed {
		return cleanIQ(snapshot, delta), model, errors.New("IQ response incomplete")
	}
	return cleanIQ(snapshot, delta), model, nil
}

func parseIQCompleted(raw []byte) (string, string, error) {
	v := gjson.ParseBytes(raw)
	if !v.Exists() || v.Get("status").String() != "completed" {
		return "", v.Get("model").String(), errors.New("IQ response incomplete")
	}
	return iqOutput(v.Get("output")), v.Get("model").String(), nil
}

func iqOutput(output gjson.Result) string {
	var b strings.Builder
	for _, item := range output.Array() {
		for _, content := range item.Get("content").Array() {
			if content.Get("type").String() == "output_text" || content.Get("type").String() == "text" {
				b.WriteString(content.Get("text").String())
			}
		}
	}
	return b.String()
}

func cleanIQ(snapshot, delta string) string {
	if snapshot != "" {
		return snapshot
	}
	return delta
}
