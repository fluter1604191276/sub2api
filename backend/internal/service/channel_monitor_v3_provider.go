package service

// The production usage repository supplies the bounded historical quality
// query while retaining its existing interface for other callers.
func ProvideChannelMonitorV3QualityService(repo UsageLogRepository) *ChannelMonitorV3QualityService {
	return NewChannelMonitorV3QualityService(repo.(ChannelMonitorV3QualityReader))
}
