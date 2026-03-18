package common

// APIStyle represents the API format being used for response processing.
type APIStyle int

const (
	StyleUnknown         APIStyle = iota
	StyleOllama                      // Deprecated: kept for backward compat in tests
	StyleOpenAIChat                  // /v1/chat/completions
	StyleAnthropic                   // /v1/messages
	StyleOpenAIResponses             // /v1/responses
)

// ContentType represents the type of content in a response stream.
type ContentType string

const (
	ContentTypeText     ContentType = "text"
	ContentTypeToolCall ContentType = "tool_call"
	ContentTypeThinking ContentType = "thinking"
	ContentTypeEmpty    ContentType = "empty"
)
