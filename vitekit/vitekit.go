package vitekit

import (
	"github.com/oliverkofoed/gokit/sitekit/web"
)

func Serve(site *web.Site, buildDir, viteHost, pathPrefix string, development bool) {
	if development {
		handler := &devHandler{
			viteHost: viteHost,
		}
		site.NotFound = web.Route{Action: handler.Action, NoGZip: true}
	} else {
		handler := &prodHandler{
			buildDir:         buildDir,
			pathPrefix:       pathPrefix,
			originalNotFound: site.NotFound.Action,
		}
		site.NotFound = web.Route{Action: handler.Action}
	}
}
