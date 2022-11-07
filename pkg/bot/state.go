package bot

import (
	"context"
	"fmt"
	"krisha_kz_bot/pkg/id"
	"log"
	"net/url"
	"strings"

	"github.com/go-redis/redis/v9"
	"github.com/pkg/errors"
)

// TODO review redis storing (context)

type State int

const (
	Default State = iota
	Subscribed
)

func (state State) String() string {
	return [...]string{"Default", "Subscribed"}[state]
}

// Implements encoding.BinaryMarshaler.
func (state State) MarshalBinary() ([]byte, error) {
	return []byte{byte(state)}, nil
}

// Implements encoding.BinaryUnmarshaler.
func (state *State) UnmarshalBinary(data []byte) error {
	if len(data) != 1 {
		return errors.WithMessagef(ErrRedisUnmarshal, "%v unsupported", data)
	}

	*state = State(data[0])

	return nil
}

type getter interface {
	getState() (State, bool)
	getSubsInChat() ([]id.Key, bool)
}

type setter interface {
	setState(State)
	// addSubscriber()
	setURL(string)
}

type deleter interface {
	delState()
	// deleteSubscriber()
}

type stater interface {
	getter
	setter
	deleter
}

// State wrapper to secure api in handlers.
type wrapper struct {
	s   *Service
	key id.Key
	ctx context.Context
}

// Wraps bot service with context given at bot service Start.
func (s *Service) wrap(key id.Key) *wrapper {
	return s.wrapCtx(s.ctx, key)
}

// Wraps bot service with given context.
func (s *Service) wrapCtx(ctx context.Context, key id.Key) *wrapper {
	return &wrapper{
		s:   s,
		key: key,
		ctx: ctx,
	}
}

type botID id.Key

func (bid botID) String() string {
	return fmt.Sprintf("bot;%s", id.Key(bid))
}

// Implements encoding.BinaryMarshaler.
func (bid botID) MarshalBinary() ([]byte, error) {
	return []byte(bid.String()), nil
}

// Implements encoding.BinaryUnmarshaler.
func (bid *botID) UnmarshalBinary(data []byte) error {
	const (
		fieldsNum = 3
	)

	values := strings.Split(string(data), ";")
	if len(values) != fieldsNum {
		return id.UnsupportedData(data)
	}

	key := (*id.Key)(bid)
	return key.UnmarshalBinary([]byte(strings.Join(values[1:], ";")))
}

func (w *wrapper) getState() (State, bool) {
	state, found := w.s.states[w.key]
	return state, found
}

func (w *wrapper) setState(state State) {
	botKey := botID(w.key)

	// store chat member state
	w.s.states[w.key] = state

	switch state {
	case Subscribed:
		// store state in permanent storage
		if status := w.s.rdb.HSet(w.ctx, botKey.String(), "state", state); status.Err() != nil {
			log.Printf("failed redis:hset %s state %s, error %v\n", botKey, state, status.Err())
		} else {
			log.Printf("success redis:hset %s %s\n", botKey, state)
		}

		// store subscriber in chats hash
		w.addSub()

	case Default:
		// remove subscriber state and url from permanent storage
		if status := w.s.rdb.Del(w.ctx, botKey.String()); status.Err() != nil {
			log.Printf("failed redis:del %s, error %v\n", botKey, status.Err())
		} else {
			log.Printf("success redis:del %s\n", botKey)
		}

		// remove subscriber from chats hash
		w.delSub()
	}
}

func (w *wrapper) addSub() {
	// add subscribed member in subscribers hash
	if subscribers, found := w.s.chats[w.key.ChatID]; found {
		w.s.chats[w.key.ChatID] = append(subscribers, w.key)
	} else {
		w.s.chats[w.key.ChatID] = []id.Key{w.key}
	}
}

func (w *wrapper) setURL(url string) {
	botKey := botID(w.key)

	// store url in hash
	w.s.urls[w.key] = url

	// store url in permanent storage
	if status := w.s.rdb.HSet(w.ctx, botKey.String(), "url", url); status.Err() != nil {
		log.Printf("failed redis:hset %s url %s, error %v\n", botKey, url, status.Err())
	} else {
		log.Printf("success redis:hset %s %s\n", botKey, url)
	}
}

func (w *wrapper) delSub() {
	if subscribers, found := w.s.chats[w.key.ChatID]; found {
		index := 0
		for _, v := range subscribers {
			if v != w.key {
				subscribers[index] = v
				index++
			}
		}
		subscribers = subscribers[:index]

		w.s.chats[w.key.ChatID] = subscribers

		if len(subscribers) == 0 {
			delete(w.s.chats, w.key.ChatID)
		}
	}
}

func (w *wrapper) delState() {
	botKey := botID(w.key)

	delete(w.s.states, w.key)

	// remove subscriber state and url from permanent storage
	if status := w.s.rdb.Del(w.ctx, botKey.String()); status.Err() != nil {
		log.Printf("failed redis:del %s, error %v\n", botKey, status.Err())
	} else {
		log.Printf("success redis:del %s\n", botKey)
	}

	// remove subscriber from chats hash
	w.delSub()
}

func (w *wrapper) getSubsInChat() ([]id.Key, bool) {
	key, found := w.s.chats[w.key.ChatID]
	return key, found
}

// Loads cache from redis in the wraper context.
// Invoke only before bot service start.
func (s *Service) load(ctx context.Context) {
	keysCh := loadKeys(ctx, s.rdb)

	for key := range keysCh {
		var (
			state State
			url   *url.URL
			err   error
		)
		if state, url, err = s.wrapCtx(ctx, key).loadState(); err != nil {
			log.Printf("failed to load data, error %v\n", err)
			continue
		}

		if _, err = s.config.OnSubscribe(nil, key, *url); err != nil {
			log.Printf("failed to load data, error %v\n", err)
			continue
		}

		s.mx.Lock()

		s.states[key] = state
		s.urls[key] = url.String()

		// add subscribed member in subscribers hash
		if subscribers, found := s.chats[key.ChatID]; found {
			s.chats[key.ChatID] = append(subscribers, key)
		} else {
			s.chats[key.ChatID] = []id.Key{key}
		}

		s.mx.Unlock()
	}
}

func loadKeys(ctx context.Context, rdb *redis.Client) <-chan id.Key {
	keysCh := make(chan id.Key)

	go func(ctx context.Context, rdb *redis.Client, keysCh chan<- id.Key) {
		defer close(keysCh)

		const (
			match = "bot;usr:*;chat:*"
			count = 10
			typ   = "hash"
		)

		cursor := uint64(0)
		for {
			var keys []string
			status := rdb.ScanType(ctx, cursor, match, count, typ)
			if err := status.Err(); err != nil {
				log.Printf("failed to redis:scan %d match %s count %d type %s, error %v\n", cursor, match, count, typ, err)
				break
			}

			var err error
			if keys, cursor, err = status.Result(); err != nil {
				log.Printf("failed to redis:scan %d match %s count %d type %s, error %v\n", cursor, match, count, typ, err)
				break
			}

			for _, keyRaw := range keys {
				var bid botID
				if err = bid.UnmarshalBinary([]byte(keyRaw)); err != nil {
					log.Printf("failed to parse %s, error %v\n", keyRaw, err)
					continue
				}

				keysCh <- id.Key(bid)
			}

			if cursor == 0 {
				break
			}
		}
	}(ctx, rdb, keysCh)

	return keysCh
}

func (w *wrapper) loadState() (State, *url.URL, error) {
	var (
		state State
		url   *url.URL
		err   error
	)

	botKey := botID(w.key)
	status := w.s.rdb.HGetAll(w.ctx, botKey.String())

	if err = status.Err(); err != nil {
		return state, nil, fmt.Errorf("failed redis:hgetall %s, error %w", botKey, err)
	}

	var values map[string]string
	if values, err = status.Result(); err != nil {
		return state, nil, fmt.Errorf("failed redis:hgetall %s, error %w", botKey, err)
	}

	stateRaw, found1 := values["state"]
	urlRaw, found2 := values["url"]
	if !found1 || !found2 {
		return state, nil, fmt.Errorf("failed parse key %s values %v", botKey, values)
	}

	if err = state.UnmarshalBinary([]byte(stateRaw)); err != nil {
		return state, nil, fmt.Errorf("failed unmarshal key %s state %s, error %w", botKey, stateRaw, err)
	}

	if url, err = url.Parse(urlRaw); err != nil {
		return state, nil, fmt.Errorf("failed parse key %s url %s, error %w", botKey, urlRaw, err)
	}

	return state, url, nil
}
