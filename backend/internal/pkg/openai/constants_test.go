package openai

import "testing"

func TestDefaultModelsContainPublicRelayModels(t *testing.T) {
	want := []string{
		"codex-auto-review",
		"gpt-4o-audio-preview",
		"gpt-4o-realtime-preview",
		"gpt-5.2",
		"gpt-5.2-2025-12-11",
		"gpt-5.2-chat-latest",
		"gpt-5.2-pro",
		"gpt-5.2-pro-2025-12-11",
		"gpt-5.3-codex",
		"gpt-5.3-codex-spark",
		"gpt-5.4",
		"gpt-5.4-2026-03-05",
		"gpt-5.4-mini",
		"gpt-5.5",
		"gpt-image-1",
		"gpt-image-1.5",
		"gpt-image-2",
	}

	got := make(map[string]struct{}, len(DefaultModels))
	for _, model := range DefaultModels {
		got[model.ID] = struct{}{}
	}
	for _, id := range want {
		if _, ok := got[id]; !ok {
			t.Fatalf("DefaultModels missing %q", id)
		}
	}
}
