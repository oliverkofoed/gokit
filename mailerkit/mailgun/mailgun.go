package mailgun

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"time"

	"github.com/mailgun/mailgun-go/v4"
	"github.com/oliverkofoed/gokit/mailerkit"
)

func init() {
	mailerkit.Register("mailgun", New)
}

type mailgunMailer struct {
	mg   *mailgun.MailgunImpl
	from string
}

// New creates a new Mailgun mailer from a config URL
// Example: mailgun://api_key@domain.com?from=noreply@example.com
// The domain is your Mailgun domain (e.g., mg.yourdomain.com)
func New(u *url.URL) mailerkit.Mailer {
	// Parse API key from URL
	if u.User == nil {
		panic("mailgun config missing API key (use mailgun://api_key@domain)")
	}

	apiKey := u.User.Username()
	if apiKey == "" {
		panic("mailgun config missing API key")
	}

	// Parse domain from hostname
	domain := u.Hostname()
	if domain == "" {
		panic("mailgun config missing domain (use mailgun://api_key@domain)")
	}

	// Parse query parameters
	query := u.Query()
	from := query.Get("from")
	if from == "" {
		panic("mailgun config missing required 'from' parameter")
	}

	// Create Mailgun client
	mg := mailgun.NewMailgun(domain, apiKey)

	return &mailgunMailer{
		mg:   mg,
		from: from,
	}
}

func (m *mailgunMailer) Send(ctx context.Context, mail mailerkit.Mail) error {
	// Create message
	message := m.mg.NewMessage(m.from, mail.Subject, mail.BodyText, mail.To)

	// Set HTML body if provided
	if mail.BodyHTML != "" {
		message.SetHtml(mail.BodyHTML)
	}

	// Add attachments
	for _, att := range mail.Attachments {
		if att.CID != "" {
			// Inline attachment (embedded image)
			message.AddReaderInline(att.Filename, io.NopCloser(bytes.NewReader(att.Content)))
		} else {
			// Regular attachment
			message.AddReaderAttachment(att.Filename, io.NopCloser(bytes.NewReader(att.Content)))
		}
	}

	// Send with timeout
	sendCtx, cancel := context.WithTimeout(ctx, time.Second*30)
	defer cancel()

	_, _, err := m.mg.Send(sendCtx, message)
	return err
}
