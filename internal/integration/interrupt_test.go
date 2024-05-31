package integration_test

import (
	"os"
	"os/signal"
	"sync"
	"testing"
)

var cf struct {
	o sync.Once
	m sync.RWMutex
	f []func()
}

func init() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go cleanupOnInterrupt(c)
}

func cleanupOnInterrupt(c chan os.Signal) {
	for range c {
		cf.o.Do(func() {
			cf.m.RLock()
			defer cf.m.RUnlock()
			for i := len(cf.f) - 1; i >= 0; i-- {
				cf.f[i]()
			}
			os.Exit(1)
		})
	}
}

func CleanupOnInterrupt(cleanup func()) {
	cf.m.Lock()
	defer cf.m.Unlock()
	cf.f = append(cf.f, cleanup)
}

// EnsureCleanup will run the provided cleanup function when the test ends,
// either via t.Cleanup or on interrupt via CleanupOnInterrupt.
// But this can't cover test timeout case.
func EnsureCleanup(t *testing.T, cleanup func()) {
	t.Cleanup(cleanup)
	CleanupOnInterrupt(cleanup)
}
