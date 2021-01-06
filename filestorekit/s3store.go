package filestorekit

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

type S3Store struct {
	s3         *s3.S3
	s3Bucket   string
	s3Prefix   string
	httpClient *http.Client
}

func NewS3(region string, s3bucket string, s3prefix string, s3accessKey string, s3secretkey string, endpoint *string) *S3Store {
	awsSession := session.New(&aws.Config{
		Region:      aws.String(region), //"us-east-2"),
		Credentials: credentials.NewStaticCredentials(s3accessKey, s3secretkey, ""),
		Endpoint:    endpoint,
	})

	return &S3Store{
		s3:         s3.New(awsSession),
		s3Bucket:   s3bucket,
		s3Prefix:   s3prefix,
		httpClient: &http.Client{Timeout: time.Second * 30},
	}
}

func (s *S3Store) Get(ctx context.Context, path string) (content []byte, contentType string, err error) {
	result, err := s.s3.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(s.s3Bucket),
		Key:    aws.String(fmt.Sprintf("%v%v", s.s3Prefix, path)),
	})
	if err != nil {
		return nil, "", err
	}
	content, err = ioutil.ReadAll(result.Body)
	if err != nil {
		return nil, "", err
	}
	return content, *result.ContentType, nil
}

func (s *S3Store) Put(ctx context.Context, path string, contentType string, content []byte) error {
	// upload to s3
	_, err := s.s3.PutObject(&s3.PutObjectInput{
		Bucket:      aws.String(s.s3Bucket),
		Key:         aws.String(fmt.Sprintf("%v%v", s.s3Prefix, path)),
		ACL:         aws.String("public-read"),
		Body:        bytes.NewReader(content),
		ContentType: aws.String(contentType),
	})
	return err
}

func (s *S3Store) Remove(ctx context.Context, path string) error {
	_, err := s.s3.DeleteObject(&s3.DeleteObjectInput{
		Bucket: aws.String(s.s3Bucket),
		Key:    aws.String(fmt.Sprintf("%v%v", s.s3Prefix, path)),
	})
	return err
}

func (s *S3Store) GetURL(path string, expire time.Duration) (string, error) {
	req, _ := s.s3.GetObjectRequest(&s3.GetObjectInput{
		Bucket: aws.String(s.s3Bucket),
		Key:    aws.String(fmt.Sprintf("%v%v", s.s3Prefix, path)),
	})

	return req.Presign(expire)
}
