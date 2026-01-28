package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type ChatCompletionRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`

	ReasoningEffort *string `json:"reasoning_effort,omitempty"` // "none", "minimal", "low", "medium", "high"
	Verbosity       *string `json:"verbosity,omitempty"`        // "low", "medium", "high"

	TopP *float32 `json:"top_p,omitempty"`
	N    *int     `json:"n,omitempty"`

	Store *bool `json:"store,omitempty"`
}

type ChatCompletionResponse struct {
	ID                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	SystemFingerprint string   `json:"system_fingerprint"`
	Choices           []Choice `json:"choices"`
	Usage             Usage    `json:"usage"`
}

type Message struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // can be string or []{type,text/image_url}
}

type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	Logprobs     any     `json:"logprobs"`
	FinishReason string  `json:"finish_reason"`
}

type Usage struct {
	PromptTokens            int                     `json:"prompt_tokens"`
	CompletionTokens        int                     `json:"completion_tokens"`
	TotalTokens             int                     `json:"total_tokens"`
	CompletionTokensDetails CompletionTokensDetails `json:"completion_tokens_details"`
}

type CompletionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// ChatOptions wraps optional chat completion parameters to keep call sites tidy.
type ChatOptions struct {
	Reasoning *string
	Verbosity *string
	TopP      *float32
	N         *int
	Store     *bool
}

func GetChatCompletion(apiKey string, requestBody ChatCompletionRequest) (*ChatCompletionResponse, error) {
	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("error marshalling request body: %v", err)
	}

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("error creating HTTP request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error sending HTTP request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("non-OK HTTP status: %s\nResponse body: %s", resp.Status, string(body))
	}

	var completionResponse ChatCompletionResponse
	err = json.Unmarshal(body, &completionResponse)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling response body: %v", err)
	}

	return &completionResponse, nil
}

// CallChatCompletion builds the request and invokes GetChatCompletion.
func CallChatCompletion(apiKey string, model string, messages []Message, opts ChatOptions) (*ChatCompletionResponse, error) {
	requestBody := ChatCompletionRequest{
		Model:           model,
		Messages:        messages,
		ReasoningEffort: opts.Reasoning,
		Verbosity:       opts.Verbosity,
		TopP:            opts.TopP,
		N:               opts.N,
		Store:           opts.Store,
	}

	return GetChatCompletion(apiKey, requestBody)
}
