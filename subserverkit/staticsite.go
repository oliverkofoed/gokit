package subserverkit

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/oliverkofoed/gokit/sitekit/web"
)

// StaticSite serves static files from a directory
type StaticSite struct {
	Path string
}

// Site returns the Site interface for the StaticSite
func (s StaticSite) Site() Site {
	return &staticSiteImpl{
		buildDir: s.Path,
	}
}

type staticSiteImpl struct {
	buildDir string
}

// ServeHTTP handles HTTP requests by serving static files
func (s *staticSiteImpl) ServeHTTP(c *web.Context) {
	// Clean the path to prevent directory traversal attacks
	cleanPath := filepath.Clean(c.Request.URL.Path)

	// Build the file path
	filePath := filepath.Join(s.buildDir, cleanPath)

	// Check if file exists and is not a directory
	info, err := os.Stat(filePath)
	if err == nil && !info.IsDir() {
		// Serve the file
		http.ServeFile(c, c.Request, filePath)
		return
	}

	// For SPAs, fall back to index.html if the file doesn't exist
	indexPath := filepath.Join(s.buildDir, "index.html")
	if _, err := os.Stat(indexPath); err == nil {
		http.ServeFile(c, c.Request, indexPath)
		return
	}

	// If no index.html exists, return 404
	http.NotFound(c, c.Request)
}
