package service

import "context"

// ChannelMonitorV3Grade is the intentionally small public quality view. Raw
// timing metrics remain internal so this value can be reused by later monitor
// policy and public-page layers without widening their contract.
type ChannelMonitorV3Grade struct {
	Grade       string `json:"grade"`
	SampleCount int64  `json:"sample_count"`
}

type ChannelMonitorV3GroupQuality struct {
	GroupID int64                 `json:"group_id"`
	Last10  ChannelMonitorV3Grade `json:"last_10"`
	Last100 ChannelMonitorV3Grade `json:"last_100"`
}

// ChannelMonitorV3GroupQualityWindows is repository-only transport. It keeps
// latency percentiles and score bases internal to the service layer.
type ChannelMonitorV3GroupQualityWindows struct {
	Last10  AccountQualityWindow
	Last100 AccountQualityWindow
}

type ChannelMonitorV3QualityReader interface {
	GetChannelMonitorV3GroupQualityWindows(context.Context, []int64) (map[int64]ChannelMonitorV3GroupQualityWindows, error)
}

type ChannelMonitorV3QualityService struct {
	reader ChannelMonitorV3QualityReader
}

func NewChannelMonitorV3QualityService(reader ChannelMonitorV3QualityReader) *ChannelMonitorV3QualityService {
	return &ChannelMonitorV3QualityService{reader: reader}
}

// GroupQuality grades the latest ten and hundred observed successful rows for
// each group. V3 deliberately permits one valid sample; existing account and
// scheduler quality paths retain their three-sample minimum.
func (s *ChannelMonitorV3QualityService) GroupQuality(ctx context.Context, groupIDs []int64) ([]ChannelMonitorV3GroupQuality, error) {
	ids := normalizeQualityIDs(groupIDs)
	result := make([]ChannelMonitorV3GroupQuality, 0, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	if s == nil || s.reader == nil {
		for _, groupID := range ids {
			result = append(result, ChannelMonitorV3GroupQuality{GroupID: groupID})
		}
		return result, nil
	}
	windows, err := s.reader.GetChannelMonitorV3GroupQualityWindows(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, groupID := range ids {
		window := windows[groupID]
		last10 := applyAccountQualityScoreWithMinSamples(window.Last10, 1)
		last100 := applyAccountQualityScoreWithMinSamples(window.Last100, 1)
		result = append(result, ChannelMonitorV3GroupQuality{
			GroupID: groupID,
			Last10:  ChannelMonitorV3Grade{Grade: last10.QualityGrade, SampleCount: last10.SampleCount},
			Last100: ChannelMonitorV3Grade{Grade: last100.QualityGrade, SampleCount: last100.SampleCount},
		})
	}
	return result, nil
}
