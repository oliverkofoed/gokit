package filestorekit

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/oliverkofoed/gokit/cachekit"
	"github.com/oliverkofoed/gokit/imagekit"
	"github.com/oliverkofoed/gokit/logkit"
)

type MediaStore struct {
	underlying Store
	cache      *cachekit.Cache
}

func NewMedia(cache *cachekit.Cache, underlying Store) *MediaStore {
	return &MediaStore{
		cache:      cache,
		underlying: underlying,
	}
}

func (s *MediaStore) GetFormattedMedia(ctx context.Context, path string, format string, gzipContent bool) (content []byte, contentType string, zipped bool, err error) {
	// check cache
	cacheKey := []byte(fmt.Sprintf("format5:%v/%v/%v", path, format, gzipContent))
	if cached := s.cache.Get(ctx, cacheKey); cached != nil {
		zipped := cached[0] == 1
		contentType = string(cached[1:(bytes.IndexByte(cached[1:], 0) + 1)])
		content := cached[150:]
		return content, contentType, zipped, nil
	}

	// get media
	content, contentType, err = s.Get(ctx, path)
	if err != nil {
		return nil, "", false, err
	}

	// remove whatever comes after @
	formatParts := strings.Split(format, "@")
	format = formatParts[0]

	// format media
	if format != "" && format != "original" && format != "raw" {
		unknownFormat := true

		// [width]x[height]
		parts := strings.Split(format, "x")
		maxSize := 250 * 1024
		if len(parts) == 3 {
			if x, err := strconv.ParseInt(parts[2], 10, 32); err == nil {
				maxSize = int(x) * 1024
			}
			parts = parts[0:2]
		}
		if len(parts) == 2 {
			width, errWidth := strconv.ParseInt(parts[0], 10, 32)
			height, errHeight := strconv.ParseInt(parts[1], 10, 32)
			if errWidth == nil && errHeight == nil {
				unknownFormat = false
				content, contentType, err = imagekit.Fit(content, int(width), int(height), maxSize)
				if err != nil {
					return nil, "", false, err
				}
			}
		}

		if unknownFormat {
			return nil, "", false, errors.New("no format specified")
		}
	}

	// check if we should zip
	zipped = false
	if gzipContent {
		w := bytes.NewBuffer(nil)
		compressor := gzip.NewWriter(w)
		compressor.Write(content)
		compressor.Close()
		if w.Len() < len(content) {
			zipped = true
			content = w.Bytes()
		}
	}

	// save to cache
	serialized := make([]byte, 150+len(content))
	if zipped {
		serialized[0] = 1
	} else {
		serialized[0] = 0
	}
	copy(serialized[1:], []byte(contentType))
	copy(serialized[150:], content)
	s.cache.Set(ctx, cacheKey, serialized, time.Hour*24*30)

	return content, contentType, zipped, nil
}

func (s *MediaStore) ServeMedia(ctx context.Context, path string, format string, w http.ResponseWriter, r *http.Request, allowGzipping bool) {
	ctx, done := logkit.Operation(ctx, "servemedia", logkit.String("path", path), logkit.String("format", format))
	defer done()

	var zipped = allowGzipping && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")

	// get the media
	var content []byte
	var contentType string
	var err error
	//content, contentType, zipped, err = s.GetWithZip(ctx, path, zipped)
	//if err != nil {
	//logkit.Error(ctx, "Error getting formatted media", logkit.Err(err))
	//}
	//} else {
	content, contentType, zipped, err = s.GetFormattedMedia(ctx, path, format, zipped)
	if err != nil {
		logkit.Error(ctx, "Error getting formatted media", logkit.Err(err))
	}
	//}

	// Not found?
	if content == nil || len(content) == 0 {
		http.NotFound(w, r)
		return
	}

	// Write response
	if zipped {
		w.Header().Set("Content-Encoding", "gzip")
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%v", len(content)))
	w.Header().Set("Cache-Control", "public, max-age=31556926")
	w.Header().Set("Expires", time.Now().AddDate(1, 0, 0).Format(http.TimeFormat))
	w.WriteHeader(200)
	w.Write(content)
}

func (s *MediaStore) Get(ctx context.Context, path string) (content []byte, contentType string, err error) {
	return s.underlying.Get(ctx, path)
}

/*func (s *MediaStore) GetWithZip(ctx context.Context, path string, gzipContent bool) (content []byte, contentType string, zipped bool, err error) {
	content, contentType, err = s.underlying.Get(ctx, path)
	if err != nil {
		return content, contentType, false, err
	}

	zipped = false
	if gzipContent {
		w := bytes.NewBuffer(nil)
		compressor := gzip.NewWriter(w)
		compressor.Write(content)
		compressor.Close()
		if w.Len() < len(content) {
			zipped = true
			content = w.Bytes()
		}
	}

	return content, contentType, zipped, err
}*/

func (s *MediaStore) Put(ctx context.Context, path string, contentType string, content []byte) error {
	return s.underlying.Put(ctx, path, contentType, content)
}

func (s *MediaStore) Remove(ctx context.Context, path string) error {
	return s.underlying.Remove(ctx, path)
}

func (s *MediaStore) GetURL(path string, expire time.Duration) (string, error) {
	return s.underlying.GetURL(path, expire)
}
