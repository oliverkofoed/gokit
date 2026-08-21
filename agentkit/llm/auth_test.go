package llm

// Tests for API key resolution (SPEC R11).

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// authResolve runs resolveKey for a provider against a client built from opts.
func authResolve(t *testing.T, provider, reqKey string, opts ...Option) (string, error) {
	t.Helper()
	c := New(opts...)
	return c.resolveKey(context.Background(), Request{
		Model:  Model{Provider: provider},
		APIKey: reqKey,
	})
}

func TestKeyResolution(t *testing.T) {
	t.Run("request APIKey wins over everything", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "env-key")
		key, err := authResolve(t, "anthropic", "req-key",
			WithAPIKey("anthropic", "static-key"),
			WithKeyFunc(func(ctx context.Context, provider string) (string, error) {
				return "kf-key", nil
			}),
		)
		if err != nil || key != "req-key" {
			t.Fatalf("key = %q, err = %v; want req-key", key, err)
		}
	})

	t.Run("static key beats key func and env", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "env-key")
		key, err := authResolve(t, "anthropic", "",
			WithAPIKey("anthropic", "static-key"),
			WithKeyFunc(func(ctx context.Context, provider string) (string, error) {
				return "kf-key", nil
			}),
		)
		if err != nil || key != "static-key" {
			t.Fatalf("key = %q, err = %v; want static-key", key, err)
		}
	})

	t.Run("static key is per provider", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "env-key")
		key, err := authResolve(t, "openai", "", WithAPIKey("anthropic", "static-key"))
		if err != nil || key != "env-key" {
			t.Fatalf("key = %q, err = %v; want env-key (other provider's static key must not apply)", key, err)
		}
	})

	t.Run("key func used and receives the provider", func(t *testing.T) {
		var sawProvider string
		key, err := authResolve(t, "anthropic", "",
			WithKeyFunc(func(ctx context.Context, provider string) (string, error) {
				sawProvider = provider
				return "kf-key", nil
			}),
		)
		if err != nil || key != "kf-key" {
			t.Fatalf("key = %q, err = %v; want kf-key", key, err)
		}
		if sawProvider != "anthropic" {
			t.Errorf("key func got provider %q, want anthropic", sawProvider)
		}
	})

	t.Run("key func empty result falls through to env", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "env-key")
		key, err := authResolve(t, "anthropic", "",
			WithKeyFunc(func(ctx context.Context, provider string) (string, error) {
				return "", nil // "not found, keep going"
			}),
		)
		if err != nil || key != "env-key" {
			t.Fatalf("key = %q, err = %v; want env-key", key, err)
		}
	})

	t.Run("key func error propagates", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "env-key") // must NOT be consulted
		sentinel := errors.New("vault down")
		key, err := authResolve(t, "anthropic", "",
			WithKeyFunc(func(ctx context.Context, provider string) (string, error) {
				return "", sentinel
			}),
		)
		if key != "" || !errors.Is(err, sentinel) {
			t.Fatalf("key = %q, err = %v; want wrapped sentinel", key, err)
		}
		if !containsAuth(err.Error(), "anthropic") {
			t.Errorf("error %q should name the provider", err)
		}
	})

	t.Run("env fallback fixed table", func(t *testing.T) {
		for provider, envVar := range map[string]string{
			"anthropic":  "ANTHROPIC_API_KEY",
			"openai":     "OPENAI_API_KEY",
			"google":     "GEMINI_API_KEY",
			"openrouter": "OPENROUTER_API_KEY",
		} {
			t.Run(provider, func(t *testing.T) {
				t.Setenv(envVar, "env-key-"+provider)
				key, err := authResolve(t, provider, "")
				if err != nil || key != "env-key-"+provider {
					t.Fatalf("key = %q, err = %v", key, err)
				}
			})
		}
	})

	t.Run("env fallback generic transform", func(t *testing.T) {
		t.Setenv("MY_PROXY_API_KEY", "proxy-key")
		key, err := authResolve(t, "my-proxy", "")
		if err != nil || key != "proxy-key" {
			t.Fatalf("key = %q, err = %v; want proxy-key via MY_PROXY_API_KEY", key, err)
		}
	})

	t.Run("no key resolves to ErrNoAPIKey", func(t *testing.T) {
		t.Setenv("ZZ_UNKNOWN_PROV_API_KEY", "") // guard against ambient env
		key, err := authResolve(t, "zz-unknown-prov", "")
		if key != "" {
			t.Fatalf("key = %q, want empty", key)
		}
		if !errors.Is(err, ErrNoAPIKey) {
			t.Fatalf("err = %v, want errors.Is(err, ErrNoAPIKey)", err)
		}
		if !containsAuth(err.Error(), "zz-unknown-prov") || !containsAuth(err.Error(), "ZZ_UNKNOWN_PROV_API_KEY") {
			t.Errorf("error %q should name the provider and env var", err)
		}
	})

	t.Run("NoAuth sentinel passes through", func(t *testing.T) {
		key, err := authResolve(t, "ollama", "", WithAPIKey("ollama", NoAuth))
		if err != nil || key != NoAuth {
			t.Fatalf("key = %q, err = %v; want the literal %q", key, err, NoAuth)
		}
	})
}

func TestEnvVarForProvider(t *testing.T) {
	cases := map[string]string{
		"anthropic":    "ANTHROPIC_API_KEY",
		"openai":       "OPENAI_API_KEY",
		"google":       "GEMINI_API_KEY", // fixed table, not GOOGLE_API_KEY
		"openrouter":   "OPENROUTER_API_KEY",
		"my-proxy":     "MY_PROXY_API_KEY",
		"fireworks.ai": "FIREWORKS_AI_API_KEY",
		"x":            "X_API_KEY",
	}
	for provider, want := range cases {
		if got := envVarForProvider(provider); got != want {
			t.Errorf("envVarForProvider(%q) = %q, want %q", provider, got, want)
		}
	}
}

func containsAuth(s, sub string) bool { return strings.Contains(s, sub) }

// TestInvalidAPIKey covers what a caller sees when a key is present but the
// provider rejects it. This is distinct from a *missing* key, which fails
// locally with ErrNoAPIKey before any request goes out (R11, above) — here the
// request is made and comes back 401.
//
// Three properties matter to a caller, on every protocol:
//   - the failure arrives as a *HTTPError carrying the status, so auth can be
//     told apart from a bad request or an outage (R21);
//   - the provider's explanation survives in ErrorText (R8), since that is
//     what a human needs to see to fix the key;
//   - it is not retried (R12) — a rejected credential will be rejected again,
//     and hammering it wastes time and can trip provider lockouts.
func TestInvalidAPIKey(t *testing.T) {
	cases := []struct {
		name   string
		model  Model
		body   string
		wantIn string
	}{
		{
			name:  "anthropic-messages",
			model: ClaudeHaiku45,
			body: `{"type":"error","error":{"type":"authentication_error",` +
				`"message":"invalid x-api-key"}}`,
			wantIn: "invalid x-api-key",
		},
		{
			name:  "openai-responses",
			model: GPT5Mini,
			body: `{"error":{"message":"Incorrect API key provided.",` +
				`"type":"invalid_request_error","code":"invalid_api_key"}}`,
			wantIn: "Incorrect API key provided",
		},
		{
			name:  "openai-chat",
			model: oaicCassetteModel,
			body: `{"error":{"message":"Incorrect API key provided.",` +
				`"type":"invalid_request_error","code":"invalid_api_key"}}`,
			wantIn: "invalid_api_key",
		},
		{
			name:  "google-gemini",
			model: Gemini37Flash,
			body: `{"error":{"code":401,"message":"API key not valid. Please pass a valid API key.",` +
				`"status":"UNAUTHENTICATED"}}`,
			wantIn: "API key not valid",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := &captureTransport{status: 401, chunks: []string{tc.body}}
			c := New(WithTransport(tr), WithAPIKey(tc.model.Provider, "sk-invalid-key"))

			s := c.Stream(context.Background(), Request{
				Model:     tc.model,
				Messages:  []Message{UserText("hi")},
				MaxTokens: 64,
			})
			events := collectEvents(s)
			msg, err := s.Message()

			if err == nil {
				t.Fatal("want an error")
			}
			var httpErr *HTTPError
			if !errors.As(err, &httpErr) {
				t.Fatalf("err = %v (%T), want *HTTPError so callers can branch on the status", err, err)
			}
			if httpErr.Status != 401 {
				t.Errorf("status = %d, want 401", httpErr.Status)
			}
			if !strings.Contains(msg.ErrorText, tc.wantIn) {
				t.Errorf("ErrorText = %q, want it to carry the provider's message %q", msg.ErrorText, tc.wantIn)
			}
			if msg.StopReason != StopError {
				t.Errorf("stop = %q, want %q", msg.StopReason, StopError)
			}
			if len(events) == 0 || events[len(events)-1].Type != EventError {
				t.Errorf("event types = %v, want a terminal %v", eventTypes(events), EventError)
			}
			// R12: 401 is not in the retryable set, so exactly one attempt.
			// The client defaults to 2 retries, so a regression here shows up
			// as 3 requests rather than 1.
			if len(tr.reqs) != 1 {
				t.Errorf("attempts = %d, want 1 (a rejected key must not be retried)", len(tr.reqs))
			}
		})
	}
}
