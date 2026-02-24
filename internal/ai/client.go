package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// Client wraps the OpenAI API for Nimbus AI features.
// It handles chat completions with function calling for the compose endpoint,
// and plain text generation for content enrichment in the worker.
type Client struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
	logger     *zap.Logger
}

// Config holds the AI client configuration.
type Config struct {
	APIKey  string // OpenAI API key (required)
	Model   string // Model to use (default: gpt-4o-mini)
	BaseURL string // API base URL (default: https://api.openai.com/v1)
	Timeout time.Duration
}

// NewClient creates a new OpenAI API client.
func NewClient(cfg Config, logger *zap.Logger) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is required for AI features")
	}
	if cfg.Model == "" {
		cfg.Model = "gpt-4o-mini"
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}

	return &Client{
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		baseURL: cfg.BaseURL,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		logger: logger,
	}, nil
}

// ---- OpenAI API types ----

// ChatMessage represents a message in the chat completion API.
type ChatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall represents a function call requested by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall holds the function name and arguments.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool defines a function the model can call.
type Tool struct {
	Type     string         `json:"type"`
	Function ToolDefinition `json:"function"`
}

// ToolDefinition describes a callable function.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type chatRequest struct {
	Model      string        `json:"model"`
	Messages   []ChatMessage `json:"messages"`
	Tools      []Tool        `json:"tools,omitempty"`
	ToolChoice interface{}   `json:"tool_choice,omitempty"`
	MaxTokens  int           `json:"max_tokens,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message      ChatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// ChatCompletion sends a chat completion request to the OpenAI API.
func (c *Client) ChatCompletion(ctx context.Context, messages []ChatMessage, tools []Tool, toolChoice interface{}) (*ChatMessage, error) {
	req := chatRequest{
		Model:    c.model,
		Messages: messages,
	}
	if len(tools) > 0 {
		req.Tools = tools
		if toolChoice != nil {
			req.ToolChoice = toolChoice
		}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if chatResp.Error != nil {
		return nil, fmt.Errorf("OpenAI API error: %s (%s)", chatResp.Error.Message, chatResp.Error.Type)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices returned from API")
	}

	c.logger.Debug("chat completion",
		zap.Int("prompt_tokens", chatResp.Usage.PromptTokens),
		zap.Int("completion_tokens", chatResp.Usage.CompletionTokens),
		zap.String("finish_reason", chatResp.Choices[0].FinishReason),
	)

	return &chatResp.Choices[0].Message, nil
}

// GenerateText is a convenience method for simple text generation (no tools).
func (c *Client) GenerateText(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	messages := []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	msg, err := c.ChatCompletion(ctx, messages, nil, nil)
	if err != nil {
		return "", err
	}
	return msg.Content, nil
}
