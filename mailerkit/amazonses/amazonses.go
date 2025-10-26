package amazonses

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/url"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ses"
	"github.com/oliverkofoed/gokit/mailerkit"
)

func init() {
	mailerkit.Register("amazonses", New)
}

type amazonSESMailer struct {
	ses  *ses.SES
	from string
}

// New creates a new Amazon SES mailer from a config URL
// Example: amazonses://access_key:secret_key@region?from=noreply@example.com
func New(u *url.URL) mailerkit.Mailer {
	// Parse AWS credentials from URL
	if u.User == nil {
		panic("amazonses config missing AWS credentials (use amazonses://access_key:secret_key@region)")
	}

	accessKey := u.User.Username()
	secretKey, _ := u.User.Password()
	if accessKey == "" || secretKey == "" {
		panic("amazonses config missing AWS access key or secret key")
	}

	// Parse region from hostname
	region := u.Hostname()
	if region == "" {
		panic("amazonses config missing AWS region (use amazonses://access_key:secret_key@region)")
	}

	// Parse query parameters
	query := u.Query()
	from := query.Get("from")
	if from == "" {
		panic("amazonses config missing required 'from' parameter")
	}

	// Create AWS session
	sess, err := session.NewSession(&aws.Config{
		Credentials: credentials.NewStaticCredentials(accessKey, secretKey, ""),
		Region:      aws.String(region),
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create AWS session: %v", err))
	}

	return &amazonSESMailer{
		ses:  ses.New(sess),
		from: from,
	}
}

func (a *amazonSESMailer) Send(ctx context.Context, mail mailerkit.Mail) error {
	hasAttachments := len(mail.Attachments) > 0

	if hasAttachments {
		// Use SendRawEmail for attachments
		return a.sendRaw(ctx, mail)
	}

	// Use simple SendEmail for no attachments
	_, err := a.ses.SendEmail(&ses.SendEmailInput{
		Destination: &ses.Destination{
			ToAddresses: []*string{aws.String(mail.To)},
		},
		Message: &ses.Message{
			Body: &ses.Body{
				Html: &ses.Content{
					Charset: aws.String("UTF-8"),
					Data:    aws.String(mail.BodyHTML),
				},
				Text: &ses.Content{
					Charset: aws.String("UTF-8"),
					Data:    aws.String(mail.BodyText),
				},
			},
			Subject: &ses.Content{
				Charset: aws.String("UTF-8"),
				Data:    aws.String(mail.Subject),
			},
		},
		Source: aws.String(a.from),
	})

	return err
}

func (a *amazonSESMailer) sendRaw(_ context.Context, mail mailerkit.Mail) error {
	var buf bytes.Buffer

	// Build MIME message
	buf.WriteString(fmt.Sprintf("From: %s\r\n", a.from))
	buf.WriteString(fmt.Sprintf("To: %s\r\n", mail.To))
	buf.WriteString(fmt.Sprintf("Subject: %s\r\n", mail.Subject))
	buf.WriteString("MIME-Version: 1.0\r\n")
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

	// Send raw email
	_, err := a.ses.SendRawEmail(&ses.SendRawEmailInput{
		RawMessage: &ses.RawMessage{
			Data: buf.Bytes(),
		},
	})

	return err
}
