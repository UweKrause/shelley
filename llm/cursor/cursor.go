// Package cursor implements llm.Service by delegating turns to the
// cursor-agent CLI, billing through the user's Cursor login or
// CURSOR_API_KEY.
package cursor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"shelley.exe.dev/llm"
	"shelley.exe.dev/llm/llmhttp"
)

const defaultBin = "cursor-agent"

// Service runs cursor-agent for each Shelley LLM turn. Cursor owns tool
// execution internally; Shelley receives the final assistant text only.
type Service struct {
	Model   string // cursor-agent --model value (e.g. "composer-2.5")
	APIKey  string // optional; uses cursor login when empty
	BinPath string

	conversationID string
	workingDir     string
	sessionID      string
	onSession      func(string) error
	mu             sync.Mutex
}

func (s *Service) Provider() string { return "cursor" }

func (s *Service) TokenContextWindow() int { return 200_000 }

func (s *Service) MaxImageDimension() int { return 0 }

func (s *Service) MaxImageBytes() int { return 0 }

func (s *Service) bin() string {
	if s.BinPath != "" {
		return s.BinPath
	}
	return defaultBin
}

// Bind returns a copy with per-conversation session state.
func (s *Service) Bind(conversationID, workingDir, sessionID string, onSession func(string) error) llm.Service {
	return &Service{
		Model:          s.Model,
		APIKey:         s.APIKey,
		BinPath:        s.BinPath,
		conversationID: conversationID,
		workingDir:     workingDir,
		sessionID:      sessionID,
		onSession:      onSession,
	}
}

func (s *Service) Do(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	prompt := latestUserText(req.Messages)
	if prompt == "" {
		return nil, fmt.Errorf("cursor: no user message in request")
	}

	workingDir := req.WorkingDir
	if workingDir == "" {
		workingDir = s.workingDir
	}
	conversationID := req.ConversationID
	if conversationID == "" {
		conversationID = s.conversationID
	}
	if conversationID == "" {
		conversationID = llmhttp.ConversationIDFromContext(ctx)
	}

	s.mu.Lock()
	sessionID := s.sessionID
	s.mu.Unlock()

	if sessionID == "" {
		id, err := createChat(ctx, s.bin(), s.APIKey)
		if err != nil {
			return nil, err
		}
		sessionID = id
		s.mu.Lock()
		s.sessionID = sessionID
		onSession := s.onSession
		s.mu.Unlock()
		if onSession != nil {
			if err := onSession(sessionID); err != nil {
				slog.Warn("cursor: failed to persist session id", "error", err)
			}
		}
	}

	streamPartial := req.OnStream != nil
	result, usage, err := runAgent(ctx, runConfig{
		bin:           s.bin(),
		apiKey:        s.APIKey,
		model:         s.Model,
		prompt:        prompt,
		workingDir:    workingDir,
		sessionID:     sessionID,
		streamPartial: streamPartial,
		onStream:      req.OnStream,
	})
	if err != nil {
		return nil, err
	}

	now := time.Now()
	return &llm.Response{
		Type:       "message",
		Role:       llm.MessageRoleAssistant,
		Model:      s.Model,
		StopReason: llm.StopReasonEndTurn,
		Content: []llm.Content{{
			Type: llm.ContentTypeText,
			Text: result,
		}},
		Usage:     usage,
		StartTime: &now,
		EndTime:   &now,
	}, nil
}

type runConfig struct {
	bin           string
	apiKey        string
	model         string
	prompt        string
	workingDir    string
	sessionID     string
	streamPartial bool
	onStream      func(llm.StreamDelta)
}

func createChat(ctx context.Context, bin, apiKey string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, "create-chat")
	if apiKey != "" {
		cmd.Env = append(os.Environ(), "CURSOR_API_KEY="+apiKey)
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("cursor create-chat: %w", err)
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		return "", fmt.Errorf("cursor create-chat: empty session id")
	}
	return id, nil
}

func runAgent(ctx context.Context, cfg runConfig) (string, llm.Usage, error) {
	args := []string{
		"-p", "--trust", "--force",
		"--output-format", "stream-json",
		"--resume", cfg.sessionID,
	}
	if cfg.model != "" {
		args = append(args, "--model", cfg.model)
	}
	if cfg.streamPartial && cfg.onStream != nil {
		args = append(args, "--stream-partial-output")
	}
	args = append(args, cfg.prompt)

	cmd := exec.CommandContext(ctx, cfg.bin, args...)
	cmd.Dir = cfg.workingDir
	if cfg.apiKey != "" {
		cmd.Env = append(os.Environ(), "CURSOR_API_KEY="+cfg.apiKey)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", llm.Usage{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return "", llm.Usage{}, fmt.Errorf("cursor-agent start: %w", err)
	}

	result, usage, parseErr := parseStreamJSON(stdout, cfg.onStream, cfg.streamPartial)
	waitErr := cmd.Wait()
	if waitErr != nil {
		msg := waitErr.Error()
		if stderr.Len() > 0 {
			msg += ": " + stderr.String()
		}
		if parseErr != nil {
			return "", usage, fmt.Errorf("cursor-agent: %s (parse: %v)", msg, parseErr)
		}
		return "", usage, fmt.Errorf("cursor-agent: %s", msg)
	}
	if parseErr != nil {
		return "", usage, parseErr
	}
	if result == "" {
		return "", usage, fmt.Errorf("cursor-agent: empty result")
	}
	return result, usage, nil
}

func parseStreamJSON(r io.Reader, onStream func(llm.StreamDelta), streamPartial bool) (string, llm.Usage, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		result      string
		usage       llm.Usage
	 streamedText string
	)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var evt map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}
		typ, _ := rawString(evt["type"])

		switch typ {
		case "assistant":
			text := assistantText(evt["message"])
			if text == "" {
				continue
			}
			if streamPartial && onStream != nil {
				delta := text
				if strings.HasPrefix(text, streamedText) {
					delta = strings.TrimPrefix(text, streamedText)
				}
				if delta != "" {
					onStream(llm.StreamDelta{Type: "text", Text: delta, Index: 0})
				}
				streamedText = text
			}
		case "result":
			sub, _ := rawString(evt["subtype"])
			if sub == "success" {
				if r, ok := rawString(evt["result"]); ok && r != "" {
					result = r
				}
			}
			usage = usageFromResult(evt["usage"])
			if m, ok := rawString(evt["model"]); ok {
				usage.Model = m
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", usage, err
	}
	return result, usage, nil
}

func assistantText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var msg struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &msg) != nil {
		return ""
	}
	var b strings.Builder
	for _, c := range msg.Content {
		if c.Type == "text" && c.Text != "" {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

func usageFromResult(raw json.RawMessage) llm.Usage {
	if len(raw) == 0 {
		return llm.Usage{}
	}
	var u struct {
		InputTokens      uint64 `json:"inputTokens"`
		OutputTokens     uint64 `json:"outputTokens"`
		CacheReadTokens  uint64 `json:"cacheReadTokens"`
		CacheWriteTokens uint64 `json:"cacheWriteTokens"`
	}
	if json.Unmarshal(raw, &u) != nil {
		return llm.Usage{}
	}
	return llm.Usage{
		InputTokens:              u.InputTokens,
		OutputTokens:             u.OutputTokens,
		CacheReadInputTokens:     u.CacheReadTokens,
		CacheCreationInputTokens: u.CacheWriteTokens,
	}
}

func rawString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

func latestUserText(messages []llm.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.ExcludedFromContext || m.Role != llm.MessageRoleUser {
			continue
		}
		var b strings.Builder
		for _, c := range m.Content {
			if c.Type == llm.ContentTypeText && c.Text != "" {
				b.WriteString(c.Text)
			}
		}
		if s := strings.TrimSpace(b.String()); s != "" {
			return s
		}
	}
	return ""
}

// Available reports whether cursor-agent is on PATH.
func Available() bool {
	_, err := exec.LookPath(defaultBin)
	return err == nil
}
