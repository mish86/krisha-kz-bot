package cleaner

import (
	"context"
	"time"
)

type Cleaner interface {
	Clean()
}

type Cleansinger interface {
	Start(ctx context.Context, interval time.Duration, onTimer func())
}

type CleansingFnc func(ctx context.Context, interval time.Duration, onTimer func())

func (fnc CleansingFnc) Start(ctx context.Context, interval time.Duration, onTimer func()) {
	fnc(ctx, interval, onTimer)
}
