package crawlkit

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestSiteCrawler(t *testing.T) {
	crawler := NewSiteCrawler("My UserAgent", 99, 3993)
	crawler.unittests = true

	start := time.Now()
	end := start.Add(time.Second * 5)

	crawler.GetConfig("slowsite.com").MaxRPS = 5
	globalReqCount := 0
	slowSiteReqCount := 0
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		for time.Now().Before(end) {
			crawler.Crawl(context.Background(), "http://google.com")
			globalReqCount++
		}
		wg.Done()
	}()
	wg.Add(1)
	go func() {
		for time.Now().Before(end) {
			crawler.Crawl(context.Background(), "http://google2.com")
			globalReqCount++
		}
		wg.Done()
	}()
	wg.Add(1)
	go func() {
		for time.Now().Before(end) {
			crawler.Crawl(context.Background(), "http://slowsite.com")
			globalReqCount++
			slowSiteReqCount++
		}
		wg.Done()
	}()
	wg.Wait()

	globalRPS := float64(globalReqCount) / float64(end.Sub(start).Seconds())
	slowSiteRPS := float64(slowSiteReqCount) / float64(end.Sub(start).Seconds())

	fmt.Println("GlobalRequests", globalReqCount, ", RPS: ", globalRPS)
	fmt.Println("SlowRequests", slowSiteReqCount, ", RPS: ", slowSiteRPS)
}
func TestRateLimiter(t *testing.T) {
	ctx := context.Background()
	limiter := rate.NewLimiter(rate.Limit(10), 10)
	start := time.Now()
	end := start.Add(time.Second * 5)
	events := int64(0)
	wg := sync.WaitGroup{}
	for i := 0; i != 10; i++ {
		wg.Add(1)
		go func() {
			for time.Now().Before(end) {
				limiter.Wait(ctx)
				atomic.AddInt64(&events, 1)
			}
			wg.Done()
		}()
	}
	wg.Wait()

	eventsPerSecond := float64(events) / float64(end.Sub(start).Seconds())
	fmt.Println("events per second: ", eventsPerSecond)
}
