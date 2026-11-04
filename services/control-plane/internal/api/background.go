package api

import (
	"context"
	"sync"
)

// Background tracks work that outlives the request that started it (an asynchronous recording
// start), so a shutdown waits for it instead of stopping the process mid-call. Every tracked task
// is bounded in time by its own timeouts.
type Background struct{ wg sync.WaitGroup }

// Go runs f in a tracked goroutine; on a nil Background it is an untracked goroutine (tests).
func (b *Background) Go(f func()) {
	if b == nil {
		go f()
		return
	}
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		f()
	}()
}

// Wait returns once every tracked task has finished, or ctx's error if ctx ends first.
func (b *Background) Wait(ctx context.Context) error {
	if b == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
