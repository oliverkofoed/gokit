package crawlkit

import "net/http"

type WebCrawler struct {
}

func NewWebCrawler() *WebCrawler {
	return &WebCrawler{}
}

func (c *WebCrawler) Crawl(url string) (*http.Response, error) {
	// download
	response, err := http.Get(url) // TODO: Use Crawler
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return nil, err
	}

	return response, nil
}
