package filestorekit

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
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
	acl        string
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
		acl:        "public-read",
	}
}

func NewS3FromUrl(rawurl string) *S3Store {
	u, err := url.Parse(rawurl)
	if err != nil {
		panic("Could not parse S3 filestore url: " + err.Error())
	}

	switch strings.ToLower(u.Scheme) {
	case "s3":
		q := u.Query()

		// Extract parameters (simple, non-provider specific names).
		bucket := u.Host
		if bucket == "" {
			panic("s3 url must include bucket in host")
			//return nil, errors.New("s3 url must include bucket in host")
		}

		prefix := strings.TrimPrefix(u.Path, "/")
		if prefix != "" && !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}

		region := q.Get("region")
		accessKey := q.Get("accesskey")
		secretKey := q.Get("secretkey")
		endpoint := q.Get("endpoint")
		acl := q.Get("acl")
		if acl == "" {
			acl = "private"
		}

		var endpointPtr *string
		if endpoint != "" {
			endpointPtr = &endpoint
		}

		if region == "" || accessKey == "" || secretKey == "" {
			panic("s3 url missing required params: region, accesskey, secretkey")
		}

		s := NewS3(region, bucket, prefix, accessKey, secretKey, endpointPtr)
		// Override ACL based on URL (default private as requested).
		s.acl = acl
		return s
	default:
		panic("unsupported filestore scheme: " + u.Scheme)
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
		ACL:         aws.String(s.acl),
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

// WithPrefix returns a new S3Store that is identical to the receiver,
// except it uses the provided prefix (replacing any existing prefix).
// The prefix is normalized to have no leading slash and to end with a trailing slash when non-empty.
func (s *S3Store) WithPrefix(prefix string) *S3Store {
	p := normalizeS3Prefix(prefix)
	return &S3Store{
		s3:         s.s3,
		s3Bucket:   s.s3Bucket,
		s3Prefix:   p,
		httpClient: s.httpClient,
		acl:        s.acl,
	}
}

func normalizeS3Prefix(p string) string {
	if p == "" {
		return ""
	}
	for len(p) > 0 && p[0] == '/' {
		p = p[1:]
	}
	if p != "" && p[len(p)-1] != '/' {
		p += "/"
	}
	return p
}
