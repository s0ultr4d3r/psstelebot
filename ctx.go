package main

import (
	"context"
	"time"
)

func withTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil { parent = context.Background() }
	return context.WithTimeout(parent, d)
}
