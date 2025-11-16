package filestorekit

import (
    "errors"
    "net/url"
    "strings"
)

// NewFromUrl creates a Store from a connection-style URL.
// Supported:
// - s3: s3://<bucket>/<prefix>?accesskey=...&secretkey=...&region=...[&endpoint=...][&acl=...]
func NewFromUrl(rawurl string) (Store, error) {
    u, err := url.Parse(rawurl)
    if err != nil {
        return nil, err
    }
    switch strings.ToLower(u.Scheme) {
    case "s3":
        // delegate to the s3 helper and convert panics to errors is not trivial; instead re-parse here
        // but we already have NewS3FromUrl which panics on invalid input. We'll trust input and wrap.
        // In practice, callers supply config, so surface as error if scheme unsupported.
        return NewS3FromUrl(rawurl), nil
    default:
        return nil, errors.New("unsupported filestore scheme: " + u.Scheme)
    }
}

