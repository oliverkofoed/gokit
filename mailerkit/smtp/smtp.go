package smtp

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"

	"github.com/oliverkofoed/gokit/logkit"
	"github.com/oliverkofoed/gokit/mailerkit"
)

func init() {
	mailerkit.Register("smtp", New)
}

type smtpMailer struct {
	host     string
	port     int
	username string
	password string
	from     string
	secure   bool
}

func New(u *url.URL) mailerkit.Mailer {
	// Parse host and port
	host := u.Hostname()
	port := 587 // default SMTP submission port
	if u.Port() != "" {
		var err error
		port, err = strconv.Atoi(u.Port())
		if err != nil {
			panic(fmt.Sprintf("invalid port in SMTP config: %v", err))
		}
	}

	// Parse username and password
	username := ""
	password := ""
	if u.User != nil {
		username = u.User.Username()
		password, _ = u.User.Password()
	}

	// Parse query parameters
	query := u.Query()
	from := query.Get("from")
	if from == "" {
		panic("SMTP config missing required 'from' parameter")
	}

	secure := true // default to secure
	if query.Get("secure") == "false" {
		secure = false
	}

	return &smtpMailer{
		host:     host,
		port:     port,
		username: username,
		password: password,
		from:     from,
		secure:   secure,
	}
}

func (m *smtpMailer) Send(ctx context.Context, mail mailerkit.Mail) error {
	// Build email headers and body
	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("From: %s\r\n", m.from))
	buf.WriteString(fmt.Sprintf("To: %s\r\n", mail.To))
	buf.WriteString(fmt.Sprintf("Subject: %s\r\n", mail.Subject))
	buf.WriteString("MIME-Version: 1.0\r\n")

	hasAttachments := len(mail.Attachments) > 0

	if hasAttachments {
		// Use multipart/mixed for attachments
		buf.WriteString("Content-Type: multipart/mixed; boundary=\"mixed-boundary\"\r\n")
		buf.WriteString("\r\n")

		// Body part (wrapped in multipart/alternative)
		buf.WriteString("--mixed-boundary\r\n")
		buf.WriteString("Content-Type: multipart/alternative; boundary=\"alt-boundary\"\r\n")
		buf.WriteString("\r\n")

		// Text part
		if mail.BodyText != "" {
			buf.WriteString("--alt-boundary\r\n")
			buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
			buf.WriteString("\r\n")
			buf.WriteString(mail.BodyText)
			buf.WriteString("\r\n")
		}

		// HTML part
		if mail.BodyHTML != "" {
			buf.WriteString("--alt-boundary\r\n")
			buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
			buf.WriteString("\r\n")
			buf.WriteString(mail.BodyHTML)
			buf.WriteString("\r\n")
		}

		buf.WriteString("--alt-boundary--\r\n")

		// Attachment parts
		for _, att := range mail.Attachments {
			buf.WriteString("--mixed-boundary\r\n")
			buf.WriteString(fmt.Sprintf("Content-Type: %s; name=\"%s\"\r\n", att.ContentType, att.Filename))
			buf.WriteString("Content-Transfer-Encoding: base64\r\n")

			// Check if this is an inline image (has CID)
			if att.CID != "" {
				buf.WriteString(fmt.Sprintf("Content-ID: <%s>\r\n", att.CID))
				buf.WriteString(fmt.Sprintf("Content-Disposition: inline; filename=\"%s\"\r\n", att.Filename))
			} else {
				buf.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"\r\n", att.Filename))
			}
			buf.WriteString("\r\n")

			// Encode attachment content as base64
			encoded := base64.StdEncoding.EncodeToString(att.Content)
			// Split into 76-character lines as per RFC 2045
			for i := 0; i < len(encoded); i += 76 {
				end := i + 76
				if end > len(encoded) {
					end = len(encoded)
				}
				buf.WriteString(encoded[i:end])
				buf.WriteString("\r\n")
			}
		}

		buf.WriteString("--mixed-boundary--\r\n")
	} else {
		// No attachments, use simple multipart/alternative
		buf.WriteString("Content-Type: multipart/alternative; boundary=\"alt-boundary\"\r\n")
		buf.WriteString("\r\n")

		// Text part
		if mail.BodyText != "" {
			buf.WriteString("--alt-boundary\r\n")
			buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
			buf.WriteString("\r\n")
			buf.WriteString(mail.BodyText)
			buf.WriteString("\r\n")
		}

		// HTML part
		if mail.BodyHTML != "" {
			buf.WriteString("--alt-boundary\r\n")
			buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
			buf.WriteString("\r\n")
			buf.WriteString(mail.BodyHTML)
			buf.WriteString("\r\n")
		}

		buf.WriteString("--alt-boundary--\r\n")
	}

	// Connect to SMTP server
	addr := fmt.Sprintf("%s:%d", m.host, m.port)

	if m.secure {
		// Use TLS connection
		return m.sendWithTLS(ctx, addr, mail.To, buf.Bytes())
	}

	// Use plain connection (for local dev like Mailpit)
	return m.sendPlain(ctx, addr, mail.To, buf.Bytes())
}

func (m *smtpMailer) sendWithTLS(ctx context.Context, addr string, to string, body []byte) error {
	// Setup TLS config
	tlsConfig := &tls.Config{
		ServerName: m.host,
	}

	// Connect with TLS
	conn, err := tls.Dial("tcp", addr, tlsConfig)
	if err != nil {
		logkit.Error(ctx, "failed to connect to SMTP server", logkit.Err(err))
		return fmt.Errorf("failed to connect to SMTP server: %w", err)
	}
	defer conn.Close()

	// Create SMTP client
	client, err := smtp.NewClient(conn, m.host)
	if err != nil {
		logkit.Error(ctx, "failed to create SMTP client", logkit.Err(err))
		return fmt.Errorf("failed to create SMTP client: %w", err)
	}
	defer client.Close()

	// Authenticate if credentials provided
	if m.username != "" {
		auth := smtp.PlainAuth("", m.username, m.password, m.host)
		if err := client.Auth(auth); err != nil {
			logkit.Error(ctx, "SMTP authentication failed", logkit.Err(err))
			return fmt.Errorf("SMTP authentication failed: %w", err)
		}
	}

	// Send mail
	if err := client.Mail(m.from); err != nil {
		logkit.Error(ctx, "failed to set sender", logkit.Err(err))
		return fmt.Errorf("failed to set sender: %w", err)
	}

	if err := client.Rcpt(to); err != nil {
		logkit.Error(ctx, "failed to set recipient", logkit.Err(err))
		return fmt.Errorf("failed to set recipient: %w", err)
	}

	w, err := client.Data()
	if err != nil {
		logkit.Error(ctx, "failed to get data writer", logkit.Err(err))
		return fmt.Errorf("failed to get data writer: %w", err)
	}

	if _, err := w.Write(body); err != nil {
		logkit.Error(ctx, "failed to write email body", logkit.Err(err))
		return fmt.Errorf("failed to write email body: %w", err)
	}

	if err := w.Close(); err != nil {
		logkit.Error(ctx, "failed to close data writer", logkit.Err(err))
		return fmt.Errorf("failed to close data writer: %w", err)
	}

	logkit.Info(ctx, "email sent successfully", logkit.String("to", to), logkit.String("subject", extractSubject(body)))
	return nil
}

func (m *smtpMailer) sendPlain(ctx context.Context, addr string, to string, body []byte) error {
	// Connect to SMTP server
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		logkit.Error(ctx, "failed to connect to SMTP server", logkit.Err(err))
		return fmt.Errorf("failed to connect to SMTP server: %w", err)
	}
	defer conn.Close()

	// Create SMTP client
	client, err := smtp.NewClient(conn, m.host)
	if err != nil {
		logkit.Error(ctx, "failed to create SMTP client", logkit.Err(err))
		return fmt.Errorf("failed to create SMTP client: %w", err)
	}
	defer client.Close()

	// Try STARTTLS if available
	if ok, _ := client.Extension("STARTTLS"); ok {
		tlsConfig := &tls.Config{
			ServerName:         m.host,
			InsecureSkipVerify: true, // For local dev environments
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			logkit.Warn(ctx, "STARTTLS failed, continuing without TLS", logkit.Err(err))
		}
	}

	// Authenticate if credentials provided
	if m.username != "" {
		auth := smtp.PlainAuth("", m.username, m.password, m.host)
		if err := client.Auth(auth); err != nil {
			logkit.Error(ctx, "SMTP authentication failed", logkit.Err(err))
			return fmt.Errorf("SMTP authentication failed: %w", err)
		}
	}

	// Send mail
	if err := client.Mail(m.from); err != nil {
		logkit.Error(ctx, "failed to set sender", logkit.Err(err))
		return fmt.Errorf("failed to set sender: %w", err)
	}

	if err := client.Rcpt(to); err != nil {
		logkit.Error(ctx, "failed to set recipient", logkit.Err(err))
		return fmt.Errorf("failed to set recipient: %w", err)
	}

	w, err := client.Data()
	if err != nil {
		logkit.Error(ctx, "failed to get data writer", logkit.Err(err))
		return fmt.Errorf("failed to get data writer: %w", err)
	}

	if _, err := w.Write(body); err != nil {
		logkit.Error(ctx, "failed to write email body", logkit.Err(err))
		return fmt.Errorf("failed to write email body: %w", err)
	}

	if err := w.Close(); err != nil {
		logkit.Error(ctx, "failed to close data writer", logkit.Err(err))
		return fmt.Errorf("failed to close data writer: %w", err)
	}

	logkit.Info(ctx, "email sent successfully", logkit.String("to", to), logkit.String("subject", extractSubject(body)))
	return nil
}

func extractSubject(body []byte) string {
	lines := strings.Split(string(body), "\r\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "Subject: ") {
			return strings.TrimPrefix(line, "Subject: ")
		}
	}
	return ""
}
