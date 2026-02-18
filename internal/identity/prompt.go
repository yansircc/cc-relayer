package identity

import (
	"strings"
)

// ClaudeCodeSystemPrompt is the primary system prompt marker.
const ClaudeCodeSystemPrompt = "You are Claude Code, Anthropic's official CLI for Claude."

// promptTemplates contains known Claude Code system prompt fragments for similarity matching.
// 8 templates matching spec Layer 6: claudeOtherSystemPrompt1-5, exploreAgent, haiku, compact.
var promptTemplates = []string{
	"You are Claude Code, Anthropic's official CLI for Claude.",                             // claudeOtherSystemPrompt1
	"You are an interactive agent that helps users with software engineering tasks",         // claudeOtherSystemPrompt2 (full CC prompt)
	"You are an interactive CLI tool that helps users",                                     // claudeOtherSystemPromptCompact
	"You are a helpful AI assistant built by Anthropic",                                    // claudeOtherSystemPrompt3 (Agent SDK)
	"You are an AI agent created using the Anthropic Agent SDK",                            // claudeOtherSystemPrompt4 (Agent SDK2)
	"Generate a concise, informative title",                                                // claudeOtherSystemPrompt5 (billing/title)
	"You are a fast file search and codebase exploration specialist",                       // exploreAgentSystemPrompt
	"You are a concise, helpful assistant that provides brief, direct answers",             // haikuSystemPrompt
}

// IsClaudeCodePrompt checks if the given system prompt text matches a known Claude Code template.
// Uses substring matching as a simpler, dependency-free alternative to string-similarity.
func IsClaudeCodePrompt(text string) bool {
	normalized := normalizeWhitespace(text)
	for _, template := range promptTemplates {
		if strings.Contains(normalized, normalizeWhitespace(template)) {
			return true
		}
	}
	return false
}

// IsClaudeCodeRequest checks if the request body appears to be from a real Claude Code client.
// Examines the system prompt entries in the body.
func IsClaudeCodeRequest(system interface{}) bool {
	switch s := system.(type) {
	case string:
		return IsClaudeCodePrompt(s)
	case []interface{}:
		for _, entry := range s {
			if m, ok := entry.(map[string]interface{}); ok {
				if text, ok := m["text"].(string); ok {
					if IsClaudeCodePrompt(text) {
						return true
					}
				}
			}
		}
	}
	return false
}

// InjectClaudeCodePrompt prepends the Claude Code system prompt to the request body.
// Returns the modified system field.
func InjectClaudeCodePrompt(system interface{}) interface{} {
	ccPrompt := map[string]interface{}{
		"type": "text",
		"text": ClaudeCodeSystemPrompt,
		"cache_control": map[string]interface{}{
			"type": "ephemeral",
		},
	}

	switch s := system.(type) {
	case nil:
		return []interface{}{ccPrompt}

	case string:
		if s == "" {
			return []interface{}{ccPrompt}
		}
		if strings.TrimSpace(s) == ClaudeCodeSystemPrompt {
			return []interface{}{ccPrompt}
		}
		userPrompt := map[string]interface{}{
			"type": "text",
			"text": s,
		}
		return []interface{}{ccPrompt, userPrompt}

	case []interface{}:
		// Check if CC prompt already at position 0
		if len(s) > 0 {
			if m, ok := s[0].(map[string]interface{}); ok {
				if text, ok := m["text"].(string); ok {
					if text == ClaudeCodeSystemPrompt {
						return s // Already injected
					}
				}
			}
		}
		// Filter out any existing CC prompts, then prepend
		filtered := make([]interface{}, 0, len(s)+1)
		filtered = append(filtered, ccPrompt)
		for _, entry := range s {
			if m, ok := entry.(map[string]interface{}); ok {
				if text, ok := m["text"].(string); ok {
					if text == ClaudeCodeSystemPrompt {
						continue // Skip duplicate
					}
				}
			}
			filtered = append(filtered, entry)
		}
		return filtered
	}

	return []interface{}{ccPrompt}
}

func normalizeWhitespace(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}
