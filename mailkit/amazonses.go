package mailkit

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ses"
)

func NewAmazonSWSSender(awsAccessKey string, awsSecretKey string, awsRegion string) Sender {
	sess, err := session.NewSession(&aws.Config{
		Credentials: credentials.NewStaticCredentials(awsAccessKey, awsSecretKey, ""),
		Region:      aws.String(awsRegion)},
	)
	if err != nil {
		panic(err)
	}

	return &amazonSESSender{ses: ses.New(sess)}
}

type amazonSESSender struct {
	ses *ses.SES
}

func (a amazonSESSender) Send(mail *Mail) error {
	_, err := a.ses.SendEmail(&ses.SendEmailInput{
		Destination: &ses.Destination{
			CcAddresses: aws.StringSlice(mail.CC),
			ToAddresses: aws.StringSlice(mail.To),
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
		Source: aws.String(mail.From),
	})

	// Display error messages if they occur.
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case ses.ErrCodeMessageRejected:
				//fmt.Println(ses.ErrCodeMessageRejected, aerr.Error())
			case ses.ErrCodeMailFromDomainNotVerifiedException:
				//fmt.Println(ses.ErrCodeMailFromDomainNotVerifiedException, aerr.Error())
			case ses.ErrCodeConfigurationSetDoesNotExistException:
				//fmt.Println(ses.ErrCodeConfigurationSetDoesNotExistException, aerr.Error())
			default:
				//fmt.Println(aerr.Error())
			}
		} else {
			// Print the error, cast err to awserr.Error to get the Code and
			// Message from an error.
			//fmt.Println(err.Error())
		}
	}

	return err
}
