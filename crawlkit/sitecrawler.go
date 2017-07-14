package crawlkit

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"

	"golang.org/x/time/rate"
)

func NewSiteCrawler(userAgent string, globalMaxRPS int, siteMaxRPS int) *SiteCrawler {
	return &SiteCrawler{
		userAgent:     userAgent,
		globalLimiter: rate.NewLimiter(rate.Limit(globalMaxRPS), globalMaxRPS),
		siteMaxRPS:    siteMaxRPS,
		configs:       make(map[string]*HostConfig),
	}
}

type SiteCrawler struct {
	sync.RWMutex
	userAgent     string
	queueSize     int64
	configs       map[string]*HostConfig
	globalLimiter *rate.Limiter
	siteMaxRPS    int
	unittests     bool
}

type HostConfig struct {
	sync.RWMutex
	MaxRPS  int
	limiter *rate.Limiter
}

func (c *SiteCrawler) GetConfig(hostname string) *HostConfig {
	c.Lock()
	config, found := c.configs[hostname]
	if !found {
		config = &HostConfig{
			MaxRPS: c.siteMaxRPS,
		}
		c.configs[hostname] = config
	}
	c.Unlock()
	return config
}

func (c *SiteCrawler) Crawl(ctx context.Context, rawUrl string) (*http.Response, error) {
	u, err := url.Parse(rawUrl)
	if err != nil {
		return nil, fmt.Errorf("bad url, could not parse. err: %v", err)
	}

	// count up an down the queue size
	atomic.AddInt64(&c.queueSize, 1)
	defer atomic.AddInt64(&c.queueSize, -1)

	// get the config
	cfg := c.GetConfig(u.Hostname())

	// pass the limit for global
	c.globalLimiter.Wait(ctx)

	// wait for the hostname specific limiter to allow
	if cfg.limiter == nil {
		cfg.Lock()
		if cfg.limiter == nil {
			cfg.limiter = rate.NewLimiter(rate.Limit(cfg.MaxRPS), cfg.MaxRPS)
		}
		cfg.Unlock()
	}
	cfg.limiter.Wait(ctx)

	// test exit
	if c.unittests {
		return &http.Response{Status: rawUrl}, nil
	}

	// download url
	response, err := http.Get(rawUrl)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return nil, err
	}

	return response, nil
}

func (c *SiteCrawler) QueueSize() int {
	return int(c.queueSize)
}
