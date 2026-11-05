package service

import "context"

// Public DTOs deliberately omit probe configuration, pricing, identities and
// raw latency data. Visibility is evaluated on every request.
type ChannelMonitorV3PublicGroup struct {
	GroupID int64                 `json:"group_id"`
	Name    string                `json:"name"`
	Last10  ChannelMonitorV3Grade `json:"last_10"`
	Last100 ChannelMonitorV3Grade `json:"last_100"`
}

type ChannelMonitorV3PublicView struct {
	Groups []ChannelMonitorV3PublicGroup `json:"groups"`
}

func (s *ChannelMonitorV3Service) Public(ctx context.Context) (*ChannelMonitorV3PublicView, error) {
	groups, err := s.repo.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(groups))
	for _, group := range groups {
		if group.Visible && group.Status == StatusActive {
			ids = append(ids, group.GroupID)
		}
	}
	quality, err := s.quality.GroupQuality(ctx, ids)
	if err != nil {
		return nil, err
	}
	byGroup := make(map[int64]ChannelMonitorV3GroupQuality, len(quality))
	for _, value := range quality {
		byGroup[value.GroupID] = value
	}
	out := &ChannelMonitorV3PublicView{Groups: []ChannelMonitorV3PublicGroup{}}
	for _, group := range groups {
		if !group.Visible || group.Status != StatusActive {
			continue
		}
		value := byGroup[group.GroupID]
		out.Groups = append(out.Groups, ChannelMonitorV3PublicGroup{
			GroupID: group.GroupID, Name: group.Name, Last10: value.Last10, Last100: value.Last100,
		})
	}
	return out, nil
}
