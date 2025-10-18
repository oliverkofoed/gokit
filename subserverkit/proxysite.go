package subserverkit

import (
	"crypto/tls"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

// ProxySite configuration for proxying requests to another host
type ProxySite struct {
	Host string // Target host (e.g., "localhost:3000" or ":3000")
}

// Site returns the Site interface for the ProxySite
func (p ProxySite) Site() Site {
	// Ensure host has proper format
	host := p.Host
	if !strings.Contains(host, ":") {
		panic("host must include port (e.g., ':9500' or 'localhost:9500')")
	}

	// Add localhost if only port is specified
	if strings.HasPrefix(host, ":") {
		host = "localhost" + host
	}

	return &proxySiteImpl{
		host: host,
	}
}

// proxySiteImpl proxies HTTP requests to another host
type proxySiteImpl struct {
	host  string
	proxy *httputil.ReverseProxy
	mu    sync.Mutex
}

// ServeHTTP handles HTTP requests by proxying to the target host
func (p *proxySiteImpl) ServeHTTP(c *web.Context) {
	// Initialize proxy on first request
	if p.proxy == nil {
		p.mu.Lock()
		if p.proxy == nil {
			target, err := url.Parse("https://" + p.host)
			if err != nil {
				panic("proxysite: invalid host: " + err.Error())
			}
			p.proxy = httputil.NewSingleHostReverseProxy(target)
			p.proxy.Transport = &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			}
		}
		p.mu.Unlock()
	}

	p.proxy.ServeHTTP(c, c.Request)
}
