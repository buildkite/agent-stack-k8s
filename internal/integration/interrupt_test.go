package integration_test

import (
	"fmt"
	"os"
	"os/signal"
	"slices"
	"sync"
	"testing"
)

var cleanupper struct {
	o            sync.Once
	mtx          sync.RWMutex
	cleanupFuncs []func()
}

func init() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go cleanupOnInterrupt(c)
}

func cleanupOnInterrupt(c chan os.Signal) {
	wasAlreadySignalled := false

	for range c {
		if wasAlreadySignalled {
			fmt.Println("Interrupt received again, exiting without cleaning up Buildkite resources...")
			os.Exit(1)
		}

		wasAlreadySignalled = true
		go func() {
			cleanupper.o.Do(func() {
				cleanupper.mtx.RLock()
				defer cleanupper.mtx.RUnlock()
				for _, f := range slices.Backward(cleanupper.cleanupFuncs) {
					f()
				}
				os.Exit(1)
			})
		}()
	}
}

// EnsureCleanup will run the provided cleanup function when the test ends,
// either via t.Cleanup or on interrupt via CleanupOnInterrupt.
// But this can't cover test timeout case.
func EnsureCleanup(t *testing.T, cleanup func()) {
	// Register a normal t.Cleanup for when tests finish normally
	t.Cleanup(cleanup)

	// Now, ensure that the cleanup funcs run even if the test is interrupted by a signal
	cleanupper.mtx.Lock()
	defer cleanupper.mtx.Unlock()
	cleanupper.cleanupFuncs = append(cleanupper.cleanupFuncs, cleanup)
}
