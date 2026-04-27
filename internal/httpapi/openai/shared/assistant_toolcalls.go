package shared

import (
	"strings"

	"ds2api/internal/toolcall"
)

func DetectAssistantToolCalls(text, exposedThinking, detectionThinking string, toolNames []string) toolcall.ToolCallParseResult {
	textParsed := toolcall.ParseStandaloneToolCallsDetailed(text, toolNames)
	if len(textParsed.Calls) > 0 {
		return textParsed
	}
	if strings.TrimSpace(text) != "" {
		return textParsed
	}
	thinking := detectionThinking
	if strings.TrimSpace(thinking) == "" {
		thinking = exposedThinking
	}
	thinkingParsed := toolcall.ParseStandaloneToolCallsDetailed(thinking, toolNames)
	if len(thinkingParsed.Calls) > 0 {
		return thinkingParsed
	}
	return textParsed
}
