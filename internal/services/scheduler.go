package services

import (
	"context"
	"log"
	"time"
)

// calls fn every interval until ctx is cancelled. 
// it is the one periodic loop behind the stress controller and the reconciler
func runEvery(ctx context.Context, name string, interval time.Duration, fn func(context.Context)) {
	if interval <= 0 {
		log.Printf("%s: interval must be > 0, got %v; loop not started", name, interval)
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fn(ctx)
		}
	}
}
