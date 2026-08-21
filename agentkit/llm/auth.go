package llm

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// NoAuth is the sentinel API key meaning "send no auth header" — register it
// for keyless local servers: WithAPIKey("ollama", llm.NoAuth) (R11).
const NoAuth = "-"

// envVarTable maps well-known providers to their environment variable (R11
// step 4). Any other provider resolves via the generic transform.
var envVarTable = map[string]string{
	"anthropic":  "ANTHROPIC_API_KEY",
	"openai":     "OPENAI_API_KEY",
	"google":     "GEMINI_API_KEY",
	"openrouter": "OPENROUTER_API_KEY",
}

// envVarForProvider returns the environment variable consulted for a
// provider: the fixed table entry, or UPPER(provider)+"_API_KEY" with
// non-alphanumerics mapped to "_".
func envVarForProvider(provider string) string {
	if v, ok := envVarTable[provider]; ok {
		return v
	}
	var b strings.Builder
	for _, r := range strings.ToUpper(provider) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String() + "_API_KEY"
}

// resolveKey implements the R11 precedence: Request.APIKey, static key,
// key func, environment variable. An empty-string result from the key func
// (with nil error) means "not found, keep going".
func (c *Client) resolveKey(ctx context.Context, req Request) (string, error) {
	if req.APIKey != "" {
		return req.APIKey, nil
	}
	provider := req.Model.Provider
	if key, ok := c.keys[provider]; ok && key != "" {
		return key, nil
	}
	if c.keyFunc != nil {
		key, err := c.keyFunc(ctx, provider)
		if err != nil {
			return "", fmt.Errorf("llm: key func for provider %q: %w", provider, err)
		}
		if key != "" {
			return key, nil
		}
	}
	if key := os.Getenv(envVarForProvider(provider)); key != "" {
		return key, nil
	}
	return "", fmt.Errorf("%w for provider %q (set %s, use WithAPIKey, or WithKeyFunc)",
		ErrNoAPIKey, provider, envVarForProvider(provider))
}
