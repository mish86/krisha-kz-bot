package scanner

import (
	"context"
	"fmt"
	"krisha_kz_bot/pkg/id"
	"log"
	"strings"

	"github.com/go-redis/redis/v9"
)

type scanID id.Key

func (sid scanID) String() string {
	return fmt.Sprintf("scan;%s", id.Key(sid))
}

// Implements encoding.BinaryMarshaler.
func (sid scanID) MarshalBinary() ([]byte, error) {
	return []byte(sid.String()), nil
}

// Implements encoding.BinaryUnmarshaler.
func (sid *scanID) UnmarshalBinary(data []byte) error {
	const (
		fieldsNum = 3
	)

	values := strings.Split(string(data), ";")
	if len(values) != fieldsNum {
		return id.UnsupportedData(data)
	}

	key := (*id.Key)(sid)
	return key.UnmarshalBinary([]byte(strings.Join(values[1:], ";")))
}

func (s *Service[Result]) addValue(ctx context.Context, key id.Key, value Result) {
	scanKey := scanID(key)

	if status := s.rdb.SAdd(ctx, scanKey.String(), value); status.Err() != nil {
		log.Printf("failed redis:sadd %s value %s, error %v\n", scanKey, value, status.Err())
	} else {
		log.Printf("success redis:sadd %s %s\n", scanKey, value)
	}
}

func (s *Service[Result]) delValue(ctx context.Context, key id.Key, values ...Result) {
	scanKey := scanID(key)

	if status := s.rdb.SRem(ctx, scanKey.String(), values); status.Err() != nil {
		log.Printf("failed redis:srem %s values %s, error %v\n", scanKey, values, status.Err())
	} else {
		log.Printf("success redis:srem %s %s\n", scanKey, values)
	}
}

func (s *Service[Result]) delKey(ctx context.Context, key id.Key) {
	scanKey := scanID(key)

	if status := s.rdb.Del(ctx, scanKey.String()); status.Err() != nil {
		log.Printf("failed redis:del %s, error %v\n", scanKey, status.Err())
	} else {
		log.Printf("success redis:del %s\n", scanKey)
	}
}

func loadValues(ctx context.Context, rdb *redis.Client, key id.Key) ([]string, error) {
	var err error

	scanKey := scanID(key)
	status := rdb.SMembers(ctx, scanKey.String())

	if err = status.Err(); err != nil {
		return nil, fmt.Errorf("failed redis:smembers %s, error %w", scanKey, err)
	}

	var values []string
	if values, err = status.Result(); err != nil {
		return nil, fmt.Errorf("failed redis:smembers %s, error %w", scanKey, err)
	}

	return values, nil
}
