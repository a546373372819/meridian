package services

import (
	"context"
	"testing"
	"time"
)

// a non positive interval must not panic
func TestRunEveryRefusesNonPositiveInterval(t *testing.T) {
	done := make(chan struct{})
	go func() {
		runEvery(context.Background(), "test", 0, func(context.Context) {
			t.Error("fn must not run with interval 0")
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runEvery with interval 0 should return immediately")
	}
}
