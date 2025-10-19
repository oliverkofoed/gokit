package multiserverkit

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/tls"
	"net/http"

	"golang.org/x/crypto/acme/autocert"
)

func SPAHandler(buildPath string, notFoundPath string, devProxyHost string, development bool) http.Handler {
	if development {
		return &ProxyHandler{Host: devProxyHost}
	}

	return &StaticHandler{Path: buildPath, NotFound: notFoundPath}
}

type Setup struct {
	AutoCert  autocert.Cache
	Cert      string
	CertKey   string
	HttpPort  string
	HttpsPort string
	GetSite   func(domain string) http.Handler
}

func (q Setup) Run(ctx context.Context) {
	s := NewWithAutocert(q.AutoCert)

	// Set up TLS config if cert files are provided
	if q.Cert != "" && q.CertKey != "" {
		certificate, err := tls.LoadX509KeyPair(q.Cert, q.CertKey)
		if err == nil {
			s.TlsConfig = &tls.Config{
				Certificates: []tls.Certificate{certificate},
				Rand:         cryptorand.Reader,
			}
		}
	}

	// Set the GetSite function
	if q.GetSite != nil {
		s.GetSite = q.GetSite
	}

	// Start HTTPS server in background if port is provided
	if q.HttpsPort != "" {
		go s.Listen(ctx, q.HttpsPort, true)
	}

	// Start HTTP server (blocks)
	if q.HttpPort != "" {
		s.Listen(ctx, q.HttpPort, false)
	}
}
