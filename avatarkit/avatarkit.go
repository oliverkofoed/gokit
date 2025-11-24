package avatarkit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oliverkofoed/gokit/cachekit"
	"github.com/oliverkofoed/gokit/filestorekit"
	"github.com/oliverkofoed/gokit/sitekit/web"
)

type EmojiGenerator interface {
	Generate(seed string) []byte
}

func AddRoute(site *web.Site, cache *cachekit.Cache, path string, generator EmojiGenerator) {
	// ensure path contains both ":format" and "*path"
	if !strings.Contains(path, ":format") || !strings.Contains(path, "*path") {
		panic("path must contain both :format and *path")
	}

	// registrer route
	m := filestorekit.NewMedia(cache, filestorekit.NewCache(cache, NewStore(generator)))
	site.AddRoute(web.Route{Path: path, Template: "", NoGZip: true, Action: func(c *web.Context) {
		fmt.Println("path", path)
		m.ServeMedia(c, c.RouteArg("path"), c.RouteArg("format"), c, c.Request, true)
	}})
}

func NewStore(generator EmojiGenerator) filestorekit.Store {
	return &store{generator: generator}
}

type store struct {
	generator EmojiGenerator
}

func (s *store) Get(ctx context.Context, path string) (content []byte, contentType string, err error) {
	seed := strings.Trim(path, "/")
	if seed == "" {
		return nil, "", errors.New("avatar path cannot be empty")
	}
	if slash := strings.LastIndex(seed, "/"); slash >= 0 {
		seed = seed[slash+1:]
	}
	if dot := strings.LastIndex(seed, "."); dot >= 0 {
		seed = seed[:dot]
	}

	content = s.generator.Generate(seed)
	if content == nil {
		return nil, "", errors.New("failed to generate avatar")
	}

	return content, "image/png", nil
}

func (s *store) Put(ctx context.Context, path string, contentType string, content []byte) error {
	return errors.New("avatar store is read-only")
}

func (s *store) Remove(ctx context.Context, path string) error {
	return errors.New("avatar store is read-only")
}

func (s *store) GetURL(path string, _ time.Duration) (string, error) {
	return "", errors.New("avatar store does not implement GetURL")
}
