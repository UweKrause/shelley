package cursor

import (
	"strings"
	"testing"

	"shelley.exe.dev/llm"
)

func TestLatestUserText(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.MessageRoleUser, Content: []llm.Content{{Type: llm.ContentTypeText, Text: "first"}}},
		{Role: llm.MessageRoleAssistant, Content: []llm.Content{{Type: llm.ContentTypeText, Text: "ok"}}},
		{Role: llm.MessageRoleUser, Content: []llm.Content{{Type: llm.ContentTypeText, Text: "second"}}},
	}
	if got := latestUserText(msgs); got != "second" {
		t.Fatalf("got %q want second", got)
	}
}

func TestParseStreamJSON(t *testing.T) {
	input := strings.Join([]string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Hel"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Hello"}]}}`,
		`{"type":"result","subtype":"success","result":"Hello","usage":{"inputTokens":10,"outputTokens":2,"cacheReadTokens":1,"cacheWriteTokens":0}}`,
	}, "\n")

	var deltas []string
	result, usage, err := parseStreamJSON(strings.NewReader(input), func(d llm.StreamDelta) {
		deltas = append(deltas, d.Text)
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if result != "Hello" {
		t.Fatalf("result=%q", result)
	}
	if usage.InputTokens != 10 || usage.OutputTokens != 2 {
		t.Fatalf("usage=%+v", usage)
	}
	if len(deltas) == 0 {
		t.Fatal("expected stream deltas")
	}
}

func TestServiceProvider(t *testing.T) {
	s := &Service{Model: "composer-2.5"}
	if s.Provider() != "cursor" {
		t.Fatalf("provider=%q", s.Provider())
	}
}
