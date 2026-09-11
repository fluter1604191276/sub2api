//go:build unit

package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCatalogMetadataContract(t *testing.T) {
	now := time.Date(2026, time.September, 12, 3, 4, 5, 987, time.FixedZone("CST", 8*60*60))
	metadata := newCatalogMetadata(now, catalogUserRateUnavailableFallback)

	require.Equal(t, catalogSourceConfiguredChannels, metadata.Source)
	require.Equal(t, catalogAvailabilityConfiguredNotLive, metadata.Availability)
	require.Equal(t, catalogPricingBasisBeforeGroup, metadata.PricingBasis)
	require.Equal(t, catalogPolicyPresentationOnly, metadata.Policy)
	require.Equal(t, "2026-09-11T19:04:05Z", metadata.GeneratedAt)
	require.Equal(t, catalogUserRateUnavailableFallback, metadata.UserRateResolution)
	_, err := time.Parse(time.RFC3339, metadata.GeneratedAt)
	require.NoError(t, err)
}

func TestAvailableCatalogMetadataHeadersPreserveArrayData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	metadata := newCatalogMetadata(time.Unix(0, 0), catalogUserRateNotIncluded)

	setCatalogMetadataHeaders(c, metadata)
	response.Success(c, []userAvailableChannel{})

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, catalogSourceConfiguredChannels, w.Header().Get(catalogHeaderSource))
	require.Equal(t, catalogAvailabilityConfiguredNotLive, w.Header().Get(catalogHeaderAvailability))
	require.Equal(t, catalogPricingBasisBeforeGroup, w.Header().Get(catalogHeaderPricingBasis))
	require.Equal(t, catalogPolicyPresentationOnly, w.Header().Get(catalogHeaderPolicy))
	require.Equal(t, "1970-01-01T00:00:00Z", w.Header().Get(catalogHeaderGeneratedAt))
	require.Equal(t, catalogUserRateNotIncluded, w.Header().Get(catalogHeaderUserRateResolution))

	var body struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	var data []userAvailableChannel
	require.NoError(t, json.Unmarshal(body.Data, &data), "available endpoint data must remain an array")
	require.Empty(t, data)
}

func TestModelPlazaCatalogMetadataIsAdditiveAndRateResolutionIsExplicit(t *testing.T) {
	for _, resolution := range []string{
		catalogUserRateNotRequested,
		catalogUserRateResolved,
		catalogUserRateUnavailableFallback,
	} {
		raw, err := json.Marshal(modelPlazaResponse{
			Groups:          []modelPlazaGroup{},
			CatalogMetadata: newCatalogMetadata(time.Unix(0, 0), resolution),
		})
		require.NoError(t, err)

		var decoded map[string]any
		require.NoError(t, json.Unmarshal(raw, &decoded))
		require.IsType(t, []any{}, decoded["groups"])
		metadata := decoded["catalog_metadata"].(map[string]any)
		require.Equal(t, resolution, metadata["user_rate_resolution"])
	}
}
