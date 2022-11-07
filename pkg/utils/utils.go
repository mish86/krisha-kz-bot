package utils

import (
	"log"
	"os"
	"strconv"
	"time"
)

// Validates given config value against allowed default one.
func GraterOrEqDefOr[T int | time.Duration](val T, defaultVal T) T {
	if val <= defaultVal {
		return defaultVal
	}

	return val
}

func ParseEnvOrPanic[T string | int | time.Duration | time.Location](key string) T {
	return ParseOrPanic[T](os.Getenv(key))
}

func ParseOrPanic[T string | int | time.Duration | time.Location](value string) T {
	tmp := new(T)
	switch any(*tmp).(type) {
	case string:
		return any(value).(T)
	case int:
		if val, err := strconv.Atoi(value); err != nil {
			log.Panic(err)
		} else {
			return any(val).(T)
		}
	case time.Duration:
		if val, err := time.ParseDuration(value); err != nil {
			log.Panic(err)
		} else {
			return any(val).(T)
		}
	case time.Location:
		if val, err := time.LoadLocation(value); err != nil {
			log.Panic(err)
		} else {
			return any(*val).(T)
		}
	default:
		log.Panicf("unsupported type of value %s", value)
	}

	return *tmp
}
