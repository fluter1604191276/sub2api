// Package openai provides helpers and types for OpenAI API integration.
package openai

import _ "embed"

// Model represents an OpenAI model
type Model struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Created     int64  `json:"created"`
	OwnedBy     string `json:"owned_by"`
	Type        string `json:"type"`
	DisplayName string `json:"display_name"`
}

// DefaultModels OpenAI models list
var DefaultModels = []Model{
	{ID: "codex-auto-review", Object: "model", Created: 1735689600, OwnedBy: "openai", Type: "model", DisplayName: "Codex Auto Review"},
	{ID: "gpt-4o-audio-preview", Object: "model", Created: 1727654400, OwnedBy: "openai", Type: "model", DisplayName: "GPT-4o Audio Preview"},
	{ID: "gpt-4o-realtime-preview", Object: "model", Created: 1727654400, OwnedBy: "openai", Type: "model", DisplayName: "GPT-4o Realtime Preview"},
	{ID: "gpt-5.2", Object: "model", Created: 1733875200, OwnedBy: "openai", Type: "model", DisplayName: "GPT-5.2"},
	{ID: "gpt-5.2-2025-12-11", Object: "model", Created: 1765411200, OwnedBy: "openai", Type: "model", DisplayName: "GPT-5.2 2025-12-11"},
	{ID: "gpt-5.2-chat-latest", Object: "model", Created: 1765411200, OwnedBy: "openai", Type: "model", DisplayName: "GPT-5.2 Chat Latest"},
	{ID: "gpt-5.2-pro", Object: "model", Created: 1765411200, OwnedBy: "openai", Type: "model", DisplayName: "GPT-5.2 Pro"},
	{ID: "gpt-5.2-pro-2025-12-11", Object: "model", Created: 1765411200, OwnedBy: "openai", Type: "model", DisplayName: "GPT-5.2 Pro 2025-12-11"},
	{ID: "gpt-5.3-codex", Object: "model", Created: 1735689600, OwnedBy: "openai", Type: "model", DisplayName: "GPT-5.3 Codex"},
	{ID: "gpt-5.3-codex-spark", Object: "model", Created: 1735689600, OwnedBy: "openai", Type: "model", DisplayName: "GPT-5.3 Codex Spark"},
	{ID: "gpt-5.4", Object: "model", Created: 1738368000, OwnedBy: "openai", Type: "model", DisplayName: "GPT-5.4"},
	{ID: "gpt-5.4-2026-03-05", Object: "model", Created: 1772668800, OwnedBy: "openai", Type: "model", DisplayName: "GPT-5.4 2026-03-05"},
	{ID: "gpt-5.4-mini", Object: "model", Created: 1738368000, OwnedBy: "openai", Type: "model", DisplayName: "GPT-5.4 Mini"},
	{ID: "gpt-5.5", Object: "model", Created: 1776873600, OwnedBy: "openai", Type: "model", DisplayName: "GPT-5.5"},
	{ID: "gpt-image-1", Object: "model", Created: 1733875200, OwnedBy: "openai", Type: "model", DisplayName: "GPT Image 1"},
	{ID: "gpt-image-1.5", Object: "model", Created: 1735689600, OwnedBy: "openai", Type: "model", DisplayName: "GPT Image 1.5"},
	{ID: "gpt-image-2", Object: "model", Created: 1738368000, OwnedBy: "openai", Type: "model", DisplayName: "GPT Image 2"},
}

// DefaultModelIDs returns the default model ID list
func DefaultModelIDs() []string {
	ids := make([]string, len(DefaultModels))
	for i, m := range DefaultModels {
		ids[i] = m.ID
	}
	return ids
}

// DefaultTestModel default model for testing OpenAI accounts
const DefaultTestModel = "gpt-5.4"

// DefaultInstructions default instructions for non-Codex CLI requests
// Content loaded from instructions.txt at compile time
//
//go:embed instructions.txt
var DefaultInstructions string
