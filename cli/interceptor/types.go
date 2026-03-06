package interceptor

import (
	"github.com/stewartpark/private-llm/cli/common"
)

// LogCallback is called by interceptors when they perform notable actions.
type LogCallback func(msg string)

// Interceptor processes response chunks and determines if the stream should continue.
type Interceptor interface {
	Feed(chunk []byte, logCb LogCallback) ([]byte, error)
	ShouldContinue() bool
	Reset()
}

// Re-export common types for convenience
type APIStyle = common.APIStyle
type ContentType = common.ContentType

const (
	contentTypeText     = string(common.ContentTypeText)
	contentTypeToolCall = string(common.ContentTypeToolCall)
	contentTypeThinking = string(common.ContentTypeThinking)
	contentTypeEmpty    = string(common.ContentTypeEmpty)
)

// Style constants re-exported
const (
	StyleOllama          = common.StyleOllama
	StyleOpenAIChat      = common.StyleOpenAIChat
	StyleAnthropic       = common.StyleAnthropic
	StyleOpenAIResponses = common.StyleOpenAIResponses
)
