package main

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"unicode/utf8"

	"pet.outbid.goapp/api"
)

func messageContentToString(content interface{}) string {
	// Most common case – plain string
	if s, ok := content.(string); ok {
		return s
	}

	// If model ever sends multi-part content (array of segments)
	if parts, ok := content.([]interface{}); ok {
		var sb strings.Builder
		for _, p := range parts {
			m, ok := p.(map[string]interface{})
			if !ok {
				continue
			}
			t, _ := m["type"].(string)
			switch t {
			case "text":
				if txt, ok := m["text"].(string); ok {
					sb.WriteString(txt)
				}
			}
		}
		if sb.Len() > 0 {
			return sb.String()
		}
		// Fallback: dump as %v so хоть что-то вернём
		return fmt.Sprintf("%v", content)
	}

	// Fallback for any other unexpected shape
	return fmt.Sprintf("%v", content)
}

func containsTrigger(s string) bool {
	if s == "" {
		return false
	}

	lower := strings.ToLower(s)

	triggers := []string{
		"загугли",
		"погугли",
		"гугли",
		"найди",
		"поищи",
		"google",
		"search",
	}
	for _, t := range triggers {
		if t == "" {
			continue
		}

		tLower := strings.ToLower(t)
		if strings.Contains(lower, tLower) {
			return true
		}
	}

	urlPattern := `(?i)\b(?:https?://|www\.)\S+`
	matched, _ := regexp.MatchString(urlPattern, s)
	return matched
}

func shouldUseWebSearch(userText, replyText string) bool {
	combined := strings.TrimSpace(userText)
	if replyText != "" {
		combined = strings.TrimSpace(combined + "\n" + replyText)
	}

	if containsTrigger(combined) {
		return true
	}
	fmt.Println("gptModelForRouting: %s\n", gptModelForRouting)

	if combined == "" || gptModelForRouting == "" {
		return false
	}

	requestBody := api.ChatCompletionRequest{
		Model: gptModelForRouting,
		Messages: []api.Message{
			{
				Role: "system",
				Content: "You are a router. Decide if the user text requires live web search. " +
					"Return exactly one token: SEARCH (if web search is needed) or NO_SEARCH (if not). " +
					"Use SEARCH for queries asking to search, containing URLs, or requesting fresh info; otherwise NO_SEARCH.",
			},
			{
				Role:    "user",
				Content: fmt.Sprintf("Message to classify:\n%s", combined),
			},
		},
	}

	resp, err := api.CallChatCompletion(openAIToken, requestBody.Model, requestBody.Messages, api.ChatOptions{})
	fmt.Println("resp: %v\n", resp)
	if err != nil {
		log.Println("Routing model error: %v", err)
		return false
	}

	if len(resp.Choices) == 0 {
		return false
	}

	decision := strings.ToUpper(strings.TrimSpace(messageContentToString(resp.Choices[0].Message.Content)))

	// Use only the first token to avoid substring collisions (e.g., NO_SEARCH contains SEARCH).
	if fields := strings.Fields(decision); len(fields) > 0 {
		decision = fields[0]
	}
	decision = strings.Trim(decision, " .!,")

	if strings.HasPrefix(decision, "NO_SEARCH") {
		return false
	}
	if strings.HasPrefix(decision, "SEARCH") {
		return true
	}
	return false
}

func aggregateBotMessage(text string) (*string, error) {
	messages := []api.Message{
		{
			Role: "system",
			Content: "You are a summarizer. Create a concise summary of the assistant's reply in 150-300 characters. " +
				"Keep key facts, names, and numbers. Return plain text without markdown, lists, or introductions.",
		},
		{
			Role:    "user",
			Content: fmt.Sprintf("Summarize this reply:\n%s", text),
		},
	}

	resp, err := api.CallChatCompletion(openAIToken, gptModelForChatting, messages, api.ChatOptions{})
	if err != nil {
		return nil, err
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in aggregation response")
	}

	summary := strings.TrimSpace(messageContentToString(resp.Choices[0].Message.Content))
	if summary == "" {
		return nil, fmt.Errorf("empty aggregation response")
	}

	const maxSummaryLength = 300
	if utf8.RuneCountInString(summary) > maxSummaryLength {
		summaryRunes := []rune(summary)
		summary = string(summaryRunes[:maxSummaryLength])
	}

	return &summary, nil
}
