package vitekit

import (
	"crypto/tls"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

type devHandler struct {
	viteHost string
	proxy    *httputil.ReverseProxy
}

func (h *devHandler) Action(c *web.Context) {
	if h.proxy == nil {
		target, err := url.Parse("https://" + h.viteHost)
		if err != nil {
			panic("vitekit: invalid vite host: " + err.Error())
		}
		h.proxy = httputil.NewSingleHostReverseProxy(target)
		h.proxy.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	h.proxy.ServeHTTP(c, c.Request)
}
