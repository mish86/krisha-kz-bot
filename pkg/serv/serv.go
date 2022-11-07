package serv

import (
	"context"
)

type Starter interface {
	Start(context.Context) error
}

type Shutdowner interface {
	Shutdown() error
}

type ShutdownerFunc func() error

func (fnc ShutdownerFunc) Shutdown() error {
	return fnc()
}
