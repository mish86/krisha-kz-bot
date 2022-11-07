package crawler

import (
	"context"
)

// Crawler daemon to scan, parse and notify about results.
type Crawler[Result any] interface {
	Start(ctx context.Context) <-chan Result
	Stop()
}

type Countable interface {
	GetCount() uint64
}
