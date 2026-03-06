package interceptor

// RequestHandler chains multiple interceptors together for request processing.
type RequestHandler struct {
	interceptors []Interceptor
	style        APIStyle
	logCallback  LogCallback
}

// NewRequestHandler creates a new handler with interceptors for the given API style.
func NewRequestHandler(style APIStyle, opts ...Option) *RequestHandler {
	handler := &RequestHandler{
		style: style,
		interceptors: []Interceptor{
			newPrematureCompletionInterceptor(style),
			newToolCallInterceptor(style),
		},
	}

	for _, opt := range opts {
		opt(handler)
	}

	return handler
}

// Option configures RequestHandler behavior.
type Option func(*RequestHandler)

// WithLogCallback sets a callback for interceptor logging.
func WithLogCallback(cb LogCallback) Option {
	return func(h *RequestHandler) {
		h.logCallback = cb
	}
}

// Feed processes a chunk through all interceptors in the chain.
func (h *RequestHandler) Feed(chunk []byte) ([]byte, error) {
	result := chunk
	for _, interceptor := range h.interceptors {
		var err error
		result, err = interceptor.Feed(result, h.logCallback)
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

// ShouldContinue returns true if any interceptor requests continuation.
func (h *RequestHandler) ShouldContinue() bool {
	for _, interceptor := range h.interceptors {
		if interceptor.ShouldContinue() {
			return true
		}
	}
	return false
}

// Reset resets all interceptors for a new request or continuation attempt.
func (h *RequestHandler) Reset() {
	for _, interceptor := range h.interceptors {
		interceptor.Reset()
	}
}

// GetOutput returns accumulated text output from the completion interceptor.
func (h *RequestHandler) GetOutput() string {
	if pc, ok := h.interceptors[0].(*prematureCompletionInterceptor); ok {
		return pc.GetOutput()
	}
	return ""
}

// ShouldContinueReason returns why continuation is needed, or empty if complete.
func (h *RequestHandler) ShouldContinueReason() string {
	if pc, ok := h.interceptors[0].(*prematureCompletionInterceptor); ok {
		return pc.shouldContinueReason()
	}
	return ""
}
