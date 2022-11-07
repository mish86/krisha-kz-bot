package scanner

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"krisha_kz_bot/pkg/crawler"
	webcrawler "krisha_kz_bot/pkg/crawler/web_crawler"
	"krisha_kz_bot/pkg/holder"
	"krisha_kz_bot/pkg/id"
	"krisha_kz_bot/pkg/utils"

	"github.com/go-redis/redis/v9"
)

const (
	DefaultScanInterval    = 5 * time.Minute
	DefaultRetentionPolicy = 24 * time.Hour // days available in cache
	DefaultVisitedBufSize  = 100
	DefaultRedisTimeout    = 2 * time.Minute
)

var (
	ErrExist    = errors.New("scanner already exists")
	ErrNotExist = errors.New("scanner does not exist")
)

// Handler of WebParser results.
type ResultHandlerFunc[Result ~string] func(key id.Key, val Result)

// Scanner entity with web site.
type scanner[Result ~string] struct {
	key      id.Key
	crawler  crawler.Crawler[holder.WithDT[Result]]
	counter  crawler.Countable
	resultCh <-chan holder.WithDT[Result]
	visited  map[Result]time.Time
}

// Scanner service.
type Service[Result ~string] struct {
	config   *Config[Result]
	entities map[id.Key]*scanner[Result]
	mx       sync.RWMutex

	rdb *redis.Client
}

// Scanner config.
type Config[Result ~string] struct {
	webcrawler.Config[holder.WithDT[Result]]
	TimeZone        time.Location
	VisitedBufSize  int
	RetentionPolicy time.Duration
	OnResult        ResultHandlerFunc[Result]
}

// Creates new scanner service from the given config.
func NewServiceFromConfig[Result ~string](config *Config[Result]) *Service[Result] {
	cfg := &Config[Result]{}
	*cfg = *config

	cfg.Interval = utils.GraterOrEqDefOr(cfg.Interval, DefaultScanInterval)
	cfg.VisitedBufSize = utils.GraterOrEqDefOr(cfg.VisitedBufSize, DefaultVisitedBufSize)
	cfg.RetentionPolicy = utils.GraterOrEqDefOr(cfg.RetentionPolicy, DefaultRetentionPolicy)

	return &Service[Result]{
		config:   cfg,
		entities: make(map[id.Key]*scanner[Result]),
	}
}

func (s *Service[Result]) WithRedis(rdb *redis.Client) *Service[Result] {
	s.rdb = rdb

	return s
}

// Stops all registered scanners. Please use it for graceful shutdown.
// Warning. Scanners remains registered.
func (s *Service[Result]) StopAll() {
	s.mx.Lock()
	defer s.mx.Unlock()

	for key, scanner := range s.entities {
		scanner.crawler.Stop()
		scanner.crawler = nil
		scanner.resultCh = nil

		log.Printf("scanner for @%s stopped\n", key)
	}
}

func (s *Service[Result]) Shutdown() error {
	log.Println("shutting down scanner service")
	// _, done := context.WithCancel(ctx)
	s.StopAll()
	// done()
	return nil
}

// Asynchronously starts scanner for the given user under the provided context.
// And asynchronously subscribes on result channel.
// The user shall be registered first.
func (s *Service[Result]) Start(ctx context.Context, key id.Key) error {
	var resultCh <-chan holder.WithDT[Result]
	scannerFound := false

	// check if scanner exists and start it async
	s.mx.Lock()
	if scanner, found := s.entities[key]; found {
		scanner.resultCh = scanner.crawler.Start(ctx)

		resultCh = scanner.resultCh
		scannerFound = found
	}
	s.mx.Unlock()

	// scanner not found - finish
	if !scannerFound {
		return ErrNotExist
	}

	// load visited from redis with timeout
	var wg sync.WaitGroup
	wg.Add(1)

	go func(ctx context.Context, key id.Key) {
		ctx, stop := context.WithTimeout(ctx, DefaultRedisTimeout)
		defer stop()
		defer wg.Done()

		now := time.Now().In(&s.config.TimeZone)
		log.Printf("Loader scanner, Now in %s %s\n", s.config.TimeZone.String(), now)
		day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, &s.config.TimeZone)

		if values, err := loadValues(ctx, s.rdb, key); err == nil {
			log.Printf("Loader scanner, %s values %v\n", key, values)

			// no need to lock entities if nothing to update
			if len(values) == 0 {
				return
			}

			s.mx.Lock()
			defer s.mx.Unlock()

			visited := s.entities[key].visited
			for _, val := range values {
				visited[Result(val)] = day
			}
			s.entities[key].visited = visited
		} else {
			log.Printf("failed to load data, error %v\n", err)
		}
	}(ctx, key)

	// blocks until visited cache loaded or timeout
	wg.Wait()

	// subscribe on results
	go func(ctx context.Context, key id.Key, resultCh <-chan holder.WithDT[Result]) {
		for val := range resultCh {
			v := val.GetValue()
			dt := val.GetDT()

			// check if link has been visited and notify
			s.mx.Lock()
			if _, found := s.entities[key].visited[v]; !found {
				s.config.OnResult(key, v)

				// store in storage with timeout - only when it is a new link
				go func(ctx context.Context, key id.Key, value Result) {
					ctx, stop := context.WithTimeout(ctx, DefaultRedisTimeout)
					defer stop()

					s.addValue(ctx, key, value)
				}(ctx, key, v)
			} else {
				log.Printf("%s already notified about %v\n", key, v)
			}

			// add to visited
			s.entities[key].visited[v] = dt

			s.mx.Unlock()
		}
	}(ctx, key, resultCh)

	return nil
}

// Registers user with given url for scanning in the service.
// Use @Start to start scanning.
func (s *Service[Result]) Register(key id.Key, urls []string) error {
	s.mx.Lock()
	defer s.mx.Unlock()

	if _, ok := s.entities[key]; ok {
		log.Printf("@%s already registered in chat %d\n", key.UserName, key.ChatID)
		return ErrExist
	}

	crawler := webcrawler.NewCrawler(&s.config.Config, urls, http.DefaultClient)

	// add scanner for a user and url
	scanner := &scanner[Result]{
		key:     key,
		crawler: crawler,
		counter: crawler,
		visited: make(map[Result]time.Time, DefaultVisitedBufSize),
	}
	s.entities[key] = scanner

	log.Printf("@%s subscribed in chat %d on scanning of %s\n", key.UserName, key.ChatID, urls)
	return nil
}

// Returns true if there is registered scanner for the given user.
func (s *Service[Result]) Exists(key id.Key) bool {
	s.mx.RLock()
	defer s.mx.RUnlock()

	_, ok := s.entities[key]

	return ok
}

// Stops and removes scanner for the given user.
func (s *Service[Result]) UnRegister(key id.Key) error {
	s.mx.Lock()
	defer s.mx.Unlock()

	if scanner, ok := s.entities[key]; ok {
		scanner.crawler.Stop()
		delete(s.entities, key)

		// del from storage with timeout
		go func(ctx context.Context, key id.Key) {
			ctx, stop := context.WithTimeout(ctx, DefaultRedisTimeout)
			defer stop()

			s.delKey(ctx, key)
		}(context.Background(), key)

		log.Printf("@%s unsubscribed in chat %d from scanning\n", key.UserName, key.ChatID)
		return nil
	}

	log.Printf("@%s not registered in chat %d\n", key.UserName, key.ChatID)
	return ErrNotExist
}

// Clears links visited less than scanner counter minus ROTATION_POLICY_THRESHOLD.
func (s *Service[Result]) Clean() {
	s.mx.Lock()
	defer s.mx.Unlock()

	now := time.Now().In(&s.config.TimeZone)
	log.Printf("Cleansing scanner, Now in %s %s\n", s.config.TimeZone.String(), now)

	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, &s.config.TimeZone)
	day = day.Add(-s.config.RetentionPolicy)

	for key, scanner := range s.entities {
		log.Printf("Cleansing scanner %s\n", key)
		for v, dt := range scanner.visited {
			if dt.Before(day) {
				// delete from local cache
				delete(scanner.visited, v)

				// del from storage with timeout
				go func(ctx context.Context, key id.Key, value Result) {
					ctx, stop := context.WithTimeout(ctx, DefaultRedisTimeout)
					defer stop()

					s.delValue(ctx, scanner.key, value)
				}(context.Background(), key, v)
			}
		}
	}
}
