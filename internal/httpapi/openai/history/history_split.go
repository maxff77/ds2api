package history

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"ds2api/internal/auth"
	dsclient "ds2api/internal/deepseek/client"
	"ds2api/internal/httpapi/openai/shared"
	"ds2api/internal/promptcompat"
)

const (
	historySplitFilename    = "HISTORY.txt"
	historySplitContentType = "text/plain; charset=utf-8"
	historySplitPurpose     = "assistants"
)

type Service struct {
	Store shared.ConfigReader
	DS    shared.DeepSeekCaller
}

func (s Service) Apply(ctx context.Context, a *auth.RequestAuth, stdReq promptcompat.StandardRequest) (promptcompat.StandardRequest, error) {
	if s.DS == nil || s.Store == nil || a == nil || !s.Store.HistorySplitEnabled() {
		return stdReq, nil
	}

	promptMessages, historyMessages := SplitOpenAIHistoryMessages(stdReq.Messages, s.Store.HistorySplitTriggerAfterTurns())
	if len(historyMessages) == 0 {
		return stdReq, nil
	}

	historyText := promptcompat.BuildOpenAIHistoryTranscript(historyMessages)
	if strings.TrimSpace(historyText) == "" {
		return stdReq, errors.New("history split produced empty transcript")
	}

	result, err := s.DS.UploadFile(ctx, a, dsclient.UploadFileRequest{
		Filename:    historySplitFilename,
		ContentType: historySplitContentType,
		Purpose:     historySplitPurpose,
		Data:        []byte(historyText),
	}, 3)
	if err != nil {
		return stdReq, fmt.Errorf("upload history file: %w", err)
	}
	fileID := strings.TrimSpace(result.ID)
	if fileID == "" {
		return stdReq, errors.New("upload history file returned empty file id")
	}

	stdReq.Messages = promptMessages
	stdReq.HistoryText = historyText
	stdReq.RefFileIDs = prependUniqueRefFileID(stdReq.RefFileIDs, fileID)
	stdReq.FinalPrompt, stdReq.ToolNames = promptcompat.BuildOpenAIPrompt(promptMessages, stdReq.ToolsRaw, "", stdReq.ToolChoice, stdReq.Thinking)
	return stdReq, nil
}

func SplitOpenAIHistoryMessages(messages []any, triggerAfterTurns int) ([]any, []any) {
	if triggerAfterTurns <= 0 {
		triggerAfterTurns = 1
	}
	lastUserIndex := -1
	userTurns := 0
	for i, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(shared.AsString(msg["role"])))
		if role != "user" {
			continue
		}
		userTurns++
		lastUserIndex = i
	}
	if userTurns <= triggerAfterTurns || lastUserIndex < 0 {
		return messages, nil
	}

	promptMessages := make([]any, 0, len(messages)-lastUserIndex)
	historyMessages := make([]any, 0, lastUserIndex)
	for i, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			if i >= lastUserIndex {
				promptMessages = append(promptMessages, raw)
			} else {
				historyMessages = append(historyMessages, raw)
			}
			continue
		}
		role := strings.ToLower(strings.TrimSpace(shared.AsString(msg["role"])))
		switch role {
		case "system", "developer":
			promptMessages = append(promptMessages, raw)
		default:
			if i >= lastUserIndex {
				promptMessages = append(promptMessages, raw)
			} else {
				historyMessages = append(historyMessages, raw)
			}
		}
	}
	if len(promptMessages) == 0 {
		return messages, nil
	}
	return promptMessages, historyMessages
}

func prependUniqueRefFileID(existing []string, fileID string) []string {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return existing
	}
	out := make([]string, 0, len(existing)+1)
	out = append(out, fileID)
	for _, id := range existing {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" || strings.EqualFold(trimmed, fileID) {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}
