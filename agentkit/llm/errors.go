package llm

import (
	"errors"
	"fmt"
)

// ErrNoAPIKey indicates key resolution (R11) found nothing for the provider.
// Errors returned from auth failures wrap it.
var ErrNoAPIKey = errors.New("llm: no API key configured")

// HTTPError is a non-2xx, non-retryable (or retry-exhausted) provider
// response. Body holds up to 2 KiB of the response body verbatim (R8) —
// provider error bodies are the single most useful debugging artifact.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("http %d", e.Status)
	}
	return fmt.Sprintf("http %d: %s", e.Status, e.Body)
}

const maxErrorBody = 2 << 10 // 2 KiB of error body kept verbatim (R8)
