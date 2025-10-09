package vitekit

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

// prodHandler serves static files from a build directory
type prodHandler struct {
	buildDir         string
	pathPrefix       string
	originalNotFound web.Action
}

func (h *prodHandler) Action(c *web.Context) {
	// only handle requests under our path prefix
	if strings.HasPrefix(c.Request.URL.Path, h.pathPrefix) {
		// Serve out index.html
		http.ServeFile(c, c.Request, filepath.Join(h.buildDir, filepath.Clean("index.html")))
		return
	}

	// strip the path prefix to get the relative file path
	relPath := strings.TrimPrefix(c.Request.URL.Path, h.pathPrefix)
	relPath = strings.TrimPrefix(relPath, "/")
	filePath := filepath.Join(h.buildDir, filepath.Clean(relPath))

	// check if the file exists, and serve it if it does
	info, err := os.Stat(filePath)
	if err == nil && !info.IsDir() {
		http.ServeFile(c, c.Request, filePath)
	} else {
		// not found
		if h.originalNotFound != nil {
			h.originalNotFound(c)
		} else {
			http.NotFound(c, c.Request)
		}
	}
}
