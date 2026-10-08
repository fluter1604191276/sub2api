//go:build e2e

package integration

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Each flow registers an independent synthetic user. No administrator or real
// account credentials are read; unavailable local services fail the test.
func TestUserRegistrationAndLogin(t *testing.T) {
	email, password := registerE2EUser(t)
	token := loginE2EUser(t, email, password)
	profile := requireE2EJSON(t, http.MethodGet, "/api/v1/auth/me", nil, token)
	require.Equal(t, email, profile["email"])
	require.Greater(t, profile["id"], float64(0))
	requireE2EStatus(t, http.MethodGet, "/api/v1/user/profile", nil, "", http.StatusUnauthorized)
}

func TestAPIKeyLifecycle(t *testing.T) {
	email, password := registerE2EUser(t)
	token := loginE2EUser(t, email, password)
	groupID, err := strconv.ParseInt(os.Getenv("E2E_USER_GROUP_ID"), 10, 64)
	require.NoError(t, err, "E2E_USER_GROUP_ID must identify an available synthetic group")
	require.Positive(t, groupID)
	created := requireE2EJSON(t, http.MethodPost, "/api/v1/keys", map[string]any{"name": "synthetic-e2e-key", "group_id": groupID}, token)
	key, ok := created["key"].(string)
	require.True(t, ok, "created key must be a string")
	require.NotEmpty(t, key)
	id, ok := created["id"].(float64)
	require.True(t, ok, "created key must have an ID")
	require.Greater(t, id, float64(0))
	path := fmt.Sprintf("/api/v1/keys/%.0f", id)
	loaded := requireE2EJSON(t, http.MethodGet, path, nil, token)
	require.Equal(t, created["id"], loaded["id"])
	require.Equal(t, "synthetic-e2e-key", loaded["name"])

	resp := requireE2EStatus(t, http.MethodGet, "/v1/models", nil, key, http.StatusOK)
	var models struct {
		Object string            `json:"object"`
		Data   []json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(resp, &models))
	require.Equal(t, "list", models.Object)
	require.NotNil(t, models.Data)
	stats := requireE2EJSON(t, http.MethodGet, "/api/v1/usage/dashboard/stats", nil, token)
	require.Equal(t, float64(0), stats["total_requests"], "model listing must not create billable usage")
	requireE2EStatus(t, http.MethodDelete, path, nil, token, http.StatusOK)
	requireE2EStatus(t, http.MethodGet, path, nil, token, http.StatusNotFound)
	require.Eventually(t, func() bool {
		response, err := doRequest(t, http.MethodGet, "/v1/models", nil, key)
		if err != nil {
			return false
		}
		defer func() { _ = response.Body.Close() }()
		return response.StatusCode == http.StatusUnauthorized
	}, 5*time.Second, 50*time.Millisecond, "deleted key must lose gateway access")
}

func registerE2EUser(t *testing.T) (string, string) {
	t.Helper()
	var suffix [12]byte
	require.NoError(t, func() error { _, err := rand.Read(suffix[:]); return err }())
	email := "synthetic-e2e-" + hex.EncodeToString(suffix[:]) + "@example.invalid"
	password := "Synthetic-E2E-only-123!"
	requireE2EJSON(t, http.MethodPost, "/api/v1/auth/register", map[string]any{"email": email, "password": password}, "")
	return email, password
}

func loginE2EUser(t *testing.T, email, password string) string {
	t.Helper()
	result := requireE2EJSON(t, http.MethodPost, "/api/v1/auth/login", map[string]any{"email": email, "password": password}, "")
	token, ok := result["access_token"].(string)
	require.True(t, ok, "login must return an access token")
	require.NotEmpty(t, token)
	return token
}

func requireE2EJSON(t *testing.T, method, path string, payload any, token string) map[string]any {
	t.Helper()
	body := requireE2EStatus(t, method, path, payload, token, http.StatusOK)
	var envelope struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope), "%s %s must return JSON", method, path)
	require.Zero(t, envelope.Code, "%s %s must return a success envelope", method, path)
	require.NotNil(t, envelope.Data, "%s %s must return data", method, path)
	return envelope.Data
}

func requireE2EStatus(t *testing.T, method, path string, payload any, token string, status int) []byte {
	t.Helper()
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		require.NoError(t, err)
	}
	response, err := doRequest(t, method, path, body, token)
	require.NoError(t, err, "%s %s request failed", method, path)
	defer func() { require.NoError(t, response.Body.Close()) }()
	require.Equal(t, status, response.StatusCode, "%s %s returned unexpected status", method, path)
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	require.NoError(t, err)
	return responseBody
}

func doRequest(t *testing.T, method, path string, body []byte, token string) (*http.Response, error) {
	t.Helper()
	request, err := http.NewRequest(method, baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return (&http.Client{Timeout: 30 * time.Second}).Do(request)
}
