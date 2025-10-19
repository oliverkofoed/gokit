package multiserverkit

import (
	"crypto/tls"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
)

// ProxySite configuration for proxying requests to another host
type ProxyHandler struct {
	Host  string // Target host (e.g., "localhost:3000" or ":3000")
	proxy *httputil.ReverseProxy
	mu    sync.Mutex
}

// ServeHTTP handles HTTP requests by proxying to the target host
func (p *ProxyHandler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	// Initialize proxy on first request
	if p.proxy == nil {
		p.mu.Lock()
		if p.proxy == nil {
			target, err := url.Parse(p.Host)
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

	p.proxy.ServeHTTP(rw, r)
}
