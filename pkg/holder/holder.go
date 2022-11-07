package holder

import "time"

type WithValue[T comparable] interface {
	GetValue() T
}

type WithDT[T comparable] interface {
	WithValue[T]
	GetDT() time.Time
}
