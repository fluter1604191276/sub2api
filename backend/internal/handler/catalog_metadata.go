package handler

import (
	"time"

	"github.com/gin-gonic/gin"
)

const (
	catalogSourceConfiguredChannels      = "configured_channels"
	catalogAvailabilityConfiguredNotLive = "configured_not_live"
	catalogPricingBasisBeforeGroup       = "before_group_multiplier"
	catalogPolicyPresentationOnly        = "presentation_only"
	catalogUserRateNotRequested          = "not_requested"
	catalogUserRateResolved              = "resolved"
	catalogUserRateUnavailableFallback   = "unavailable_fallback_to_group"
	catalogUserRateNotIncluded           = "not_included"

	catalogHeaderSource             = "X-Catalog-Source"
	catalogHeaderAvailability       = "X-Catalog-Availability"
	catalogHeaderPricingBasis       = "X-Catalog-Pricing-Basis"
	catalogHeaderPolicy             = "X-Catalog-Policy"
	catalogHeaderGeneratedAt        = "X-Catalog-Generated-At"
	catalogHeaderUserRateResolution = "X-Catalog-User-Rate-Resolution"
)

// catalogMetadata states what the public catalogue represents. Availability is
// configuration-derived and must not be interpreted as a live health signal.
type catalogMetadata struct {
	Source             string `json:"source"`
	Availability       string `json:"availability"`
	PricingBasis       string `json:"pricing_basis"`
	Policy             string `json:"policy"`
	GeneratedAt        string `json:"generated_at"`
	UserRateResolution string `json:"user_rate_resolution"`
}

func newCatalogMetadata(now time.Time, userRateResolution string) *catalogMetadata {
	return &catalogMetadata{
		Source:             catalogSourceConfiguredChannels,
		Availability:       catalogAvailabilityConfiguredNotLive,
		PricingBasis:       catalogPricingBasisBeforeGroup,
		Policy:             catalogPolicyPresentationOnly,
		GeneratedAt:        now.UTC().Format(time.RFC3339),
		UserRateResolution: userRateResolution,
	}
}

func setCatalogMetadataHeaders(c *gin.Context, metadata *catalogMetadata) {
	if metadata == nil {
		return
	}
	c.Header(catalogHeaderSource, metadata.Source)
	c.Header(catalogHeaderAvailability, metadata.Availability)
	c.Header(catalogHeaderPricingBasis, metadata.PricingBasis)
	c.Header(catalogHeaderPolicy, metadata.Policy)
	c.Header(catalogHeaderGeneratedAt, metadata.GeneratedAt)
	c.Header(catalogHeaderUserRateResolution, metadata.UserRateResolution)
}
