package mailerkit

import (
	"context"
	"fmt"
	"net/url"
	"sync"
)

// Attachment represents a file attachment
type Attachment struct {
	Filename    string // Name of the file (e.g., "document.pdf")
	ContentType string // MIME type (e.g., "application/pdf")
	Content     []byte // File contents
	CID         string // Content-ID for inline images (optional, e.g., "logo" to reference as <img src="cid:logo">)
}

// Mail represents an email message
type Mail struct {
	To          string
	Subject     string
	BodyHTML    string
	BodyText    string
	Attachments []Attachment
}

// Mailer handles sending emails
type Mailer interface {
	Send(ctx context.Context, mail Mail) error
}

// ProviderFunc creates a new Mailer from a config URL
type ProviderFunc func(*url.URL) Mailer

var (
	providersMu sync.RWMutex
	providers   = make(map[string]ProviderFunc)
)

// Register registers a mailer provider for a given URL scheme
// This is typically called in init() functions of sub-packages
func Register(scheme string, provider ProviderFunc) {
	providersMu.Lock()
	defer providersMu.Unlock()
	if provider == nil {
		panic("mailerkit: Register provider is nil")
	}
	if _, dup := providers[scheme]; dup {
		panic("mailerkit: Register called twice for scheme " + scheme)
	}
	providers[scheme] = provider
}

// New creates a new mailer from a config URL
// Example: smtp://username:password@host:port?from=noreply@example.com&secure=true
// Example: amazonses://access_key:secret_key@region?from=noreply@example.com
// Example: mailgun://api_key@domain.com?from=noreply@example.com
// Will panic if the scheme is not registered or if anything is wrong with the config
func New(configURL string) Mailer {
	u, err := url.Parse(configURL)
	if err != nil {
		panic(fmt.Sprintf("invalid mailer config URL: %v", err))
	}

	providersMu.RLock()
	provider, ok := providers[u.Scheme]
	providersMu.RUnlock()

	if !ok {
		panic(fmt.Sprintf("unsupported mailer scheme: %s (did you forget to import the provider package?)", u.Scheme))
	}

	return provider(u)
}
