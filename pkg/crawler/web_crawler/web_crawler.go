package webcrawler

import (
	"context"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"krisha_kz_bot/pkg/parser"

	"github.com/pkg/errors"
)

const (
	scannerPagesDelay = 30 * time.Second
)

// Web crawler config with parser.
type Config[Result any] struct {
	Interval time.Duration
	Parser   parser.Parser[Result]
}

// Web site crawler daemon.
type WebCrawler[Result any] struct {
	config  *Config[Result]
	urls    []string
	client  *http.Client
	stop    context.CancelFunc
	counter uint64
}

// Returns new Crawler daemon with given interval for crawling.
func NewCrawler[Result any](config *Config[Result], urls []string, client *http.Client) *WebCrawler[Result] {
	if client == nil {
		client = http.DefaultClient
	}
	return &WebCrawler[Result]{
		config: config,
		urls:   urls,
		client: client,
	}
}

// Starts Crawler daemon asynchronously and returns result channel.
func (c *WebCrawler[Result]) Start(ctx context.Context) <-chan Result {
	result := make(chan Result)
	ctx, cancel := context.WithCancel(ctx)
	c.stop = cancel

	go func(ctx context.Context, result chan<- Result) {
		// close result channel after crawler interupped from upstream
		defer close(result)

		doCrawl := func(ctx context.Context, urls []string, result chan<- Result) {
			// send results immediately without waiting of timer
			for _, url := range urls {
				if err := c.DoCrawl(ctx, url, result); err != nil {
					log.Printf("failed to crawl resource %s, error %+v\n", url, err)
				}

				// sleep before next call
				if len(urls) > 1 {
					time.Sleep(scannerPagesDelay)
				}
			}
		}
		// send results immediately without waiting of timer
		doCrawl(ctx, c.urls, result)

		retryTimer := time.NewTimer(c.config.Interval)
		// stop retry timer after crawler interupped from upstream
		defer retryTimer.Stop()
		for {
			select {
			case <-ctx.Done():
				// operation interuppted from upstream
				log.Printf("stopping crawler\n")
				return

			case <-retryTimer.C:
				doCrawl(ctx, c.urls, result)

				// increase counter
				atomic.AddUint64(&c.counter, 1)

				// reset timer
				retryTimer.Reset(c.config.Interval)
			}
		}
	}(ctx, result)

	return result
}

// Stops Crawler daemon.
func (c *WebCrawler[Result]) Stop() {
	c.stop()
}

// Loads resource payload, parses and sends result to the given channel.
func (c *WebCrawler[Result]) DoCrawl(ctx context.Context, url string, resultCh chan<- Result) error {
	// build request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Add("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:106.0) Gecko/20100101 Firefox/106.0")
	req.Header.Add("Accept", "text/html")

	// do request
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return errors.Errorf("Error: %d - %s", resp.StatusCode, resp.Status)
	}

	return c.config.Parser.Parse(resp.Body, func(val Result) {
		resultCh <- val
	})
}

func (c *WebCrawler[Result]) GetCount() uint64 {
	return atomic.LoadUint64(&c.counter)
}
