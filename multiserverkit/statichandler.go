package multiserverkit

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type cacheEntry struct {
	data    []byte
	modTime time.Time
}

// StaticHandler serves static files from a directory with in-memory caching
type StaticHandler struct {
	Path     string
	NotFound string
	cache    map[string]*cacheEntry // URL path -> cached file (nil = cached 404)
	mu       sync.RWMutex
}

func (s *StaticHandler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	// Initialize cache
	s.mu.Lock()
	if s.cache == nil {
		s.cache = make(map[string]*cacheEntry)
	}
	s.mu.Unlock()

	cleanPath := filepath.Clean(r.URL.Path)

	// Check cache first
	s.mu.RLock()
	entry, cached := s.cache[cleanPath]
	s.mu.RUnlock()

	if cached {
		if entry == nil {
			// Cached 404
			http.NotFound(rw, r)
			return
		}
		http.ServeContent(rw, r, filepath.Base(cleanPath), entry.modTime, bytes.NewReader(entry.data))
		return
	}

	// Cache miss - resolve file
	filePath := filepath.Join(s.Path, cleanPath)

	// Try direct file
	if data, modTime, ok := s.tryLoadFile(filePath); ok {
		s.cacheAndServe(cleanPath, data, modTime, rw, r)
		return
	}

	// Try index.html in directory
	if data, modTime, ok := s.tryLoadFile(filepath.Join(filePath, "index.html")); ok {
		s.cacheAndServe(cleanPath, data, modTime, rw, r)
		return
	}

	// Try NotFound file
	if s.NotFound != "" {
		if data, modTime, ok := s.tryLoadFile(filepath.Join(s.Path, s.NotFound)); ok {
			s.cacheAndServe(cleanPath, data, modTime, rw, r)
			return
		}
	}

	// Cache the 404
	s.mu.Lock()
	s.cache[cleanPath] = nil
	s.mu.Unlock()

	http.NotFound(rw, r)
}

func (s *StaticHandler) tryLoadFile(filePath string) ([]byte, time.Time, bool) {
	info, err := os.Stat(filePath)
	if err != nil || info.IsDir() {
		return nil, time.Time{}, false
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, time.Time{}, false
	}

	return data, info.ModTime(), true
}

func (s *StaticHandler) cacheAndServe(urlPath string, data []byte, modTime time.Time, rw http.ResponseWriter, r *http.Request) {
	entry := &cacheEntry{data: data, modTime: modTime}

	s.mu.Lock()
	s.cache[urlPath] = entry
	s.mu.Unlock()

	http.ServeContent(rw, r, filepath.Base(urlPath), modTime, bytes.NewReader(data))
}
