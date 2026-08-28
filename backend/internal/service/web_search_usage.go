package service

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// webSearchUsageTracker extracts provider-reported web-search usage without
// treating an ordinary client function named "web_search" as a billable
// server-side search. Explicit usage counters are cumulative; Responses
// output items are deduplicated because the same item can appear in both
// output_item.added and output_item.done events.
type webSearchUsageTracker struct {
	explicitCount int
	outputCount   int
	seenItems     map[string]struct{}
}

func (t *webSearchUsageTracker) ObserveJSON(body []byte) {
	if t == nil || len(body) == 0 || !json.Valid(body) {
		return
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return
	}

	for _, object := range webSearchUsageObjects(root) {
		if count := explicitWebSearchCount(object); count > t.explicitCount {
			t.explicitCount = count
		}
	}
	for _, output := range webSearchOutputArrays(root) {
		for _, item := range output {
			t.observeOutputItem(item)
		}
	}
}

func (t *webSearchUsageTracker) observeOutputItem(item json.RawMessage) {
	if t == nil {
		return
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(item, &value); err != nil {
		return
	}
	if webSearchStringValue(value["type"]) != "web_search_call" {
		return
	}

	key := webSearchStringValue(value["id"])
	if key == "" {
		key = string(item)
	}
	if t.seenItems == nil {
		t.seenItems = make(map[string]struct{})
	}
	if _, exists := t.seenItems[key]; exists {
		return
	}
	t.seenItems[key] = struct{}{}
	t.outputCount++
}

func (t *webSearchUsageTracker) Count() int {
	if t == nil {
		return 0
	}
	if t.outputCount > t.explicitCount {
		return t.outputCount
	}
	return t.explicitCount
}

func webSearchUsageObjects(root map[string]json.RawMessage) []map[string]json.RawMessage {
	objects := make([]map[string]json.RawMessage, 0, 5)
	addObject := func(raw json.RawMessage) {
		var object map[string]json.RawMessage
		if len(raw) == 0 || json.Unmarshal(raw, &object) != nil || object == nil {
			return
		}
		objects = append(objects, object)
	}
	if root != nil {
		objects = append(objects, root)
	}
	for _, key := range []string{"usage", "tool_usage"} {
		if raw, ok := root[key]; ok {
			addObject(raw)
		}
	}
	if raw, ok := root["response"]; ok {
		var response map[string]json.RawMessage
		if json.Unmarshal(raw, &response) == nil {
			if response != nil {
				objects = append(objects, response)
			}
			for _, key := range []string{"usage", "tool_usage"} {
				if nested, exists := response[key]; exists {
					addObject(nested)
				}
			}
		}
	}
	return objects
}

func explicitWebSearchCount(object map[string]json.RawMessage) int {
	maxCount := 0
	for _, key := range []string{
		"web_search_calls",
		"web_search_call_count",
		"web_search_count",
	} {
		if count := rawNonNegativeInt(object[key]); count > maxCount {
			maxCount = count
		}
	}
	if raw, ok := object["web_search"]; ok {
		var nested map[string]json.RawMessage
		if json.Unmarshal(raw, &nested) == nil {
			for _, key := range []string{"calls", "count"} {
				if count := rawNonNegativeInt(nested[key]); count > maxCount {
					maxCount = count
				}
			}
		}
	}
	return maxCount
}

func webSearchOutputArrays(root map[string]json.RawMessage) [][]json.RawMessage {
	outputs := make([][]json.RawMessage, 0, 2)
	add := func(raw json.RawMessage) {
		var items []json.RawMessage
		if len(raw) > 0 && json.Unmarshal(raw, &items) == nil && items != nil {
			outputs = append(outputs, items)
		}
	}
	add(root["output"])
	if raw, ok := root["response"]; ok {
		var response map[string]json.RawMessage
		if json.Unmarshal(raw, &response) == nil {
			add(response["output"])
		}
	}
	if raw, ok := root["item"]; ok {
		// SSE response.output_item.added/done carries one item, not an array.
		var item map[string]json.RawMessage
		if json.Unmarshal(raw, &item) == nil && item != nil {
			encoded, _ := json.Marshal(item)
			outputs = append(outputs, []json.RawMessage{encoded})
		}
	}
	return outputs
}

func rawNonNegativeInt(raw json.RawMessage) int {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return 0
	}
	var number int
	if json.Unmarshal(raw, &number) == nil && number > 0 {
		return number
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		parsed, err := strconv.Atoi(strings.TrimSpace(text))
		if err == nil && parsed > 0 {
			return parsed
		}
	}
	return 0
}

func webSearchStringValue(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return strings.TrimSpace(value)
	}
	return ""
}
