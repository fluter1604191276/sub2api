package service

import (
	"context"
	"fmt"
	"math"
	"strings"
)

type ChannelMonitorV3Settings struct {
	Enabled        bool    `json:"enabled"`
	DailyBudgetUSD float64 `json:"daily_budget_usd"`
}

func DefaultChannelMonitorV3Settings() ChannelMonitorV3Settings {
	return ChannelMonitorV3Settings{DailyBudgetUSD: 2}
}

type ChannelMonitorV3GroupConfig struct {
	GroupID              int64  `json:"group_id"`
	Name                 string `json:"name"`
	Platform             string `json:"platform"`
	Status               string `json:"status"`
	Visible              bool   `json:"visible"`
	ProbeIntervalSeconds int    `json:"probe_interval_seconds"`
	TestModel            string `json:"test_model"`
}

type ChannelMonitorV3ConfigView struct {
	Settings ChannelMonitorV3Settings      `json:"settings"`
	Groups   []ChannelMonitorV3GroupConfig `json:"groups"`
	Budget   *ChannelMonitorV3Budget       `json:"budget,omitempty"`
}

type ChannelMonitorV3Repository interface {
	Settings(context.Context) (ChannelMonitorV3Settings, error)
	SaveSettings(context.Context, ChannelMonitorV3Settings) (ChannelMonitorV3Settings, error)
	ListGroups(context.Context) ([]ChannelMonitorV3GroupConfig, error)
	SaveGroup(context.Context, ChannelMonitorV3GroupConfig) (*ChannelMonitorV3GroupConfig, error)
}

type ChannelMonitorV3Service struct {
	repo    ChannelMonitorV3Repository
	quality *ChannelMonitorV3QualityService
}

func NewChannelMonitorV3Service(repo ChannelMonitorV3Repository, quality *ChannelMonitorV3QualityService) *ChannelMonitorV3Service {
	return &ChannelMonitorV3Service{repo: repo, quality: quality}
}

func (s *ChannelMonitorV3Service) Config(ctx context.Context) (*ChannelMonitorV3ConfigView, error) {
	settings, err := s.repo.Settings(ctx)
	if err != nil {
		return nil, err
	}
	groups, err := s.repo.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	return &ChannelMonitorV3ConfigView{Settings: settings, Groups: groups}, nil
}

func (s *ChannelMonitorV3Service) SaveSettings(ctx context.Context, in ChannelMonitorV3Settings) (ChannelMonitorV3Settings, error) {
	if math.IsNaN(in.DailyBudgetUSD) || math.IsInf(in.DailyBudgetUSD, 0) || in.DailyBudgetUSD <= 0 {
		return in, fmt.Errorf("daily_budget_usd must be a positive finite amount")
	}
	in.DailyBudgetUSD = QuantizeUsageBillingAmount(in.DailyBudgetUSD)
	if in.DailyBudgetUSD <= 0 {
		return in, fmt.Errorf("daily_budget_usd is below the supported precision")
	}
	return s.repo.SaveSettings(ctx, in)
}

func (s *ChannelMonitorV3Service) SaveGroup(ctx context.Context, in ChannelMonitorV3GroupConfig) (*ChannelMonitorV3GroupConfig, error) {
	if in.GroupID <= 0 {
		return nil, fmt.Errorf("invalid group id")
	}
	if in.ProbeIntervalSeconds != 0 && (in.ProbeIntervalSeconds < 60 || in.ProbeIntervalSeconds > 86400) {
		return nil, fmt.Errorf("probe_interval_seconds must be 0 or between 60 and 86400")
	}
	in.TestModel = strings.TrimSpace(in.TestModel)
	if len(in.TestModel) > 256 || strings.ContainsAny(in.TestModel, "\r\n\t ") {
		return nil, fmt.Errorf("test_model must be a model identifier of at most 256 bytes")
	}
	return s.repo.SaveGroup(ctx, in)
}
