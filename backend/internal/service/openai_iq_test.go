package service

import (
	"strings"
	"testing"
)

func TestDefaultIQSettingsUsesCorrectedReferencePuzzle(t *testing.T) {
	settings := DefaultIQSettings()

	if settings.ExpectedAnswer != "15" {
		t.Fatalf("ExpectedAnswer = %q, want 15", settings.ExpectedAnswer)
	}
	for _, number := range []string{"苹果味 6", "桃子味 6", "西瓜味 5", "苹果味 5", "桃子味 5", "西瓜味 3"} {
		if !strings.Contains(settings.Question, number) {
			t.Fatalf("reference question does not contain %q", number)
		}
	}
}

func TestExtractIQAnswerRequiresUniqueFinalMarker(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
		ok     bool
	}{
		{name: "final line", output: "reasoning\nFINAL_ANSWER: 15\n", want: "15", ok: true},
		{name: "marker is not final", output: "FINAL_ANSWER: 15\nmore reasoning", ok: false},
		{name: "conflicting marker", output: "FINAL_ANSWER: 14\nFINAL_ANSWER: 15", ok: false},
		{name: "empty marker", output: "FINAL_ANSWER:", ok: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := ExtractIQAnswer(test.output)
			if got != test.want || ok != test.ok {
				t.Fatalf("ExtractIQAnswer() = (%q, %v), want (%q, %v)", got, ok, test.want, test.ok)
			}
		})
	}
}

func TestIQAnswerCorrectToleratesCountUnitsOnlyForIntegerAnswers(t *testing.T) {
	for _, test := range []struct {
		answer   string
		expected string
		correct  bool
	}{
		{answer: "15颗", expected: "15", correct: true},
		{answer: "15 个", expected: "15", correct: true},
		{answer: "15个颗", expected: "15", correct: false},
		{answer: "约15颗", expected: "15", correct: false},
		{answer: "15颗或21颗", expected: "15", correct: false},
		{answer: "15", expected: "15个", correct: false},
		{answer: "Apple", expected: "apple", correct: true},
	} {
		t.Run(test.answer+"/"+test.expected, func(t *testing.T) {
			if got := IQAnswerCorrect("FINAL_ANSWER: "+test.answer, test.expected); got != test.correct {
				t.Fatalf("IQAnswerCorrect() = %v, want %v", got, test.correct)
			}
		})
	}
}
