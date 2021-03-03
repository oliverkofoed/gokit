package filestorekit

import (
	"bytes"
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

func (s *MediaStore) GetFormattedMedia(ctx context.Context, path string, format string) (content []byte, contentType string, err error) {
	// check cache
	cacheKey := []byte(fmt.Sprintf("format2:%v/%v", path, format))
	if cached := s.cache.Get(ctx, cacheKey); cached != nil {
		contentType = string(cached[:bytes.IndexByte(cached, 0)])
		content := cached[150:]
		return content, contentType, nil
	}

	// get media
	content, contentType, err = s.Get(ctx, path)
	if err != nil {
		return nil, "", err
	}

	// remove whatever comes after @
	formatParts := strings.Split(format, "@")
	format = formatParts[0]

	// format media
	if format != "" && format != "original" {
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
					return nil, "", err
				}
			}
		}

		if unknownFormat {
			return nil, "", errors.New("no format specified")
		}
	}

	// save to cache
	serialized := make([]byte, 150+len(content))
	copy(serialized, []byte(contentType))
	copy(serialized[150:], content)
	s.cache.Set(ctx, cacheKey, serialized, time.Hour*24*30)

	return content, contentType, nil
}

func (s *MediaStore) ServeMedia(ctx context.Context, path string, format string, w http.ResponseWriter, r *http.Request) {
	ctx, done := logkit.Operation(ctx, "servemedia", logkit.String("path", path), logkit.String("format", format))
	defer done()

	// get the media
	var content []byte
	var contentType string
	var err error
	if format == "" || format == "raw" {
		content, contentType, err = s.Get(ctx, path)
		if err != nil {
			logkit.Error(ctx, "Error getting formatted media", logkit.Err(err))
		}
	} else {
		content, contentType, err = s.GetFormattedMedia(ctx, path, format)
		if err != nil {
			logkit.Error(ctx, "Error getting formatted media", logkit.Err(err))
		}
	}

	// Not found?
	if content == nil || len(content) == 0 {
		http.NotFound(w, r)
		return
	}

	// Gzipping
	/*if !strings.Contains(c.Request.Header.Get("Accept-Encoding"), "gzip") {
		in := bytes.NewBuffer(imageBytes)
		zipper, err := gzip.NewReader(in)
		if err != nil {
			c.ServerError(err.Error(), 500)
		}
		defer zipper.Close()

		out := new(bytes.Buffer)
		io.Copy(out, zipper)
		zipper.Close()

		imageBytes = buf.Bytes()
	} else {
		c.Header().Set("Content-Encoding", "gzip")
	}*/

	// Write response
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=31556926")
	w.Header().Set("Expires", time.Now().AddDate(1, 0, 0).Format(http.TimeFormat))
	w.WriteHeader(200)
	w.Write(content)
}

func (s *MediaStore) Get(ctx context.Context, path string) (content []byte, contentType string, err error) {
	return s.underlying.Get(ctx, path)
}

func (s *MediaStore) Put(ctx context.Context, path string, contentType string, content []byte) error {
	return s.underlying.Put(ctx, path, contentType, content)
}

func (s *MediaStore) Remove(ctx context.Context, path string) error {
	return s.underlying.Remove(ctx, path)
}

func (s *MediaStore) GetURL(path string, expire time.Duration) (string, error) {
	return s.underlying.GetURL(path, expire)
}
