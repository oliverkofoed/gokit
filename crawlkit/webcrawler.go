package crawlkit

import (
	"context"
	"net/http"
)

type WebCrawler interface {
	Crawl(ctx context.Context, url string) (*http.Response, error)
	QueueSize() int
}
