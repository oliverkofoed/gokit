package multiserverkit

import (
	"net/http"
	"strings"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

// Prefixes maps URL path prefixes to their corresponding Site handlers
type Prefixes map[string]http.Handler

// Mux creates a multiplexer that routes requests based on path prefixes
// It tries each prefix in order and falls back to the fallback route if no prefix matches
func MuxRoute(prefixes Prefixes, fallback *web.Route) web.Route {
	action := func(c *web.Context) {
		path := c.Request.URL.Path

		// Try to match each prefix
		for prefix, handler := range prefixes {
			if strings.HasPrefix(path, prefix) {
				// Serve the request
				handler.ServeHTTP(c, c.Request)
				return
			}
		}

		// No prefix matched, use fallback
		if fallback != nil && fallback.Action != nil {
			fallback.Action(c)
		} else {
			http.NotFound(c, c.Request)
		}
	}

	return web.Route{Action: action, NoGZip: true}
}
