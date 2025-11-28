package memory

import (
	"context"
	"net/url"
	"sync"

	"github.com/oliverkofoed/gokit/mailerkit"
)

func init() {
	mailerkit.Register("memory", New)
}

// MemoryMailer stores mails in memory for testing purposes.
type Mailer struct {
	mu   sync.Mutex
	sent []mailerkit.Mail
}

// New returns a new in-memory mailer implementation.
func New(u *url.URL) mailerkit.Mailer {
	return &Mailer{}
}

// Send records the mail in memory.
func (m *Mailer) Send(ctx context.Context, mail mailerkit.Mail) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, mail)
	return nil
}

// Sent returns a copy of all mails sent so far.
func (m *Mailer) Sent() []mailerkit.Mail {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]mailerkit.Mail, len(m.sent))
	copy(out, m.sent)
	return out
}

// Reset clears any recorded mails.
func (m *Mailer) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = nil
}
