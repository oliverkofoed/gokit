package mailerkit_test

import (
	"context"

	"github.com/oliverkofoed/gokit/mailerkit"
	// Import the providers you want to use - they will auto-register
	_ "github.com/oliverkofoed/gokit/mailerkit/amazonses"
	_ "github.com/oliverkofoed/gokit/mailerkit/mailgun"
	_ "github.com/oliverkofoed/gokit/mailerkit/smtp"
)

func ExampleNew_smtp() {
	// Create an SMTP mailer
	mailer := mailerkit.New("smtp://username:password@smtp.example.com:587?from=noreply@example.com&secure=true")

	// Send an email
	err := mailer.Send(context.Background(), mailerkit.Mail{
		To:       "user@example.com",
		Subject:  "Hello",
		BodyHTML: "<h1>Hello World</h1>",
		BodyText: "Hello World",
	})
	_ = err
}

func ExampleNew_amazonses() {
	// Create an Amazon SES mailer
	mailer := mailerkit.New("amazonses://AKIAIOSFODNN7EXAMPLE:wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY@us-east-1?from=noreply@example.com")

	// Send an email
	err := mailer.Send(context.Background(), mailerkit.Mail{
		To:       "user@example.com",
		Subject:  "Hello",
		BodyHTML: "<h1>Hello World</h1>",
		BodyText: "Hello World",
	})
	_ = err
}

func ExampleNew_selectiveImport() {
	// If you only import smtp package:
	// import _ "github.com/oliverkofoed/gokit/mailerkit/smtp"
	//
	// Then only smtp:// URLs will work:
	// mailerkit.New("smtp://...") // works
	// mailerkit.New("amazonses://...") // panics with "unsupported mailer scheme"
	//
	// This allows you to only include the providers you need in your binary
}

func ExampleNew_withAttachments() {
	mailer := mailerkit.New("smtp://username:password@smtp.example.com:587?from=noreply@example.com&secure=true")

	// Send an email with attachments
	err := mailer.Send(context.Background(), mailerkit.Mail{
		To:       "user@example.com",
		Subject:  "Invoice for your order",
		BodyHTML: "<h1>Thanks for your order!</h1><p>Please find your invoice attached.</p>",
		BodyText: "Thanks for your order! Please find your invoice attached.",
		Attachments: []mailerkit.Attachment{
			{
				Filename:    "invoice.pdf",
				ContentType: "application/pdf",
				Content:     []byte("PDF content here..."),
			},
			{
				Filename:    "receipt.txt",
				ContentType: "text/plain",
				Content:     []byte("Receipt details..."),
			},
		},
	})
	_ = err
}

func ExampleNew_withEmbeddedImages() {
	mailer := mailerkit.New("smtp://username:password@smtp.example.com:587?from=noreply@example.com&secure=true")

	// Send an email with embedded images using CID
	err := mailer.Send(context.Background(), mailerkit.Mail{
		To:      "user@example.com",
		Subject: "Welcome!",
		BodyHTML: `
			<html>
				<body>
					<h1>Welcome to our service!</h1>
					<img src="cid:logo" alt="Logo" />
					<p>We're excited to have you on board.</p>
				</body>
			</html>
		`,
		BodyText: "Welcome to our service! We're excited to have you on board.",
		Attachments: []mailerkit.Attachment{
			{
				Filename:    "logo.png",
				ContentType: "image/png",
				Content:     []byte("PNG image data here..."),
				CID:         "logo", // Reference this with <img src="cid:logo">
			},
		},
	})
	_ = err
}

func ExampleNew_mailgun() {
	// Create a Mailgun mailer
	mailer := mailerkit.New("mailgun://your-api-key@mg.yourdomain.com?from=noreply@yourdomain.com")

	// Send an email
	err := mailer.Send(context.Background(), mailerkit.Mail{
		To:       "user@example.com",
		Subject:  "Hello from Mailgun",
		BodyHTML: "<h1>Hello World</h1>",
		BodyText: "Hello World",
	})
	_ = err
}
