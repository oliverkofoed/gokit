package crawlkit

import (
	"context"
	"net/http"
)

func NewBasicCrawler() WebCrawler {
	return &BasicCrawler{}
}

type BasicCrawler struct {
}

func (c *BasicCrawler) Crawl(ctx context.Context, url string) (*http.Response, error) {
	// download
	response, err := http.Get(url)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return nil, err
	}

	return response, nil
}

func (c *BasicCrawler) QueueSize() int {
	return 0
}
