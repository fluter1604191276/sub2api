package service

import "github.com/Wei-Shaw/sub2api/internal/config"

func ProvideChannelMonitorV3ProbeRunner(repo ChannelMonitorV3Repository, attempts UpstreamAttemptRepository,
	billing *BillingService, channels *ChannelService, accounts AccountRepository,
	cfg *config.Config, groups GroupRepository, subscriptions UserSubscriptionRepository) *ChannelMonitorV3ProbeRunner {
	runner := NewChannelMonitorV3ProbeRunner(repo, attempts, billing, channels, accounts, cfg, groups, subscriptions)
	runner.Start()
	return runner
}
