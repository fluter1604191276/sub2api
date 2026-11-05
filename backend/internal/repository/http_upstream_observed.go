package repository

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// ProvideObservedHTTPUpstream retains the production transport's proxy, TLS,
// retry and cancellation policies, adding only usage observation to its body.
func ProvideObservedHTTPUpstream(cfg *config.Config, recorder *service.UpstreamAttemptRecorder) service.HTTPUpstream {
	return service.NewObservedHTTPUpstream(NewHTTPUpstream(cfg), recorder)
}
