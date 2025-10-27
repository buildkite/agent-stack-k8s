package api

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/buildkite/stacksapi"
)

const (
	// Extreme situation, we don't anticipate people to encounter.
	// This will consume ~1GB memory
	// (The number is from: 1000 jobs per second, 100 notifications per jobs, 30 seconds)
	maxBufferSize = 3_000_000
	// Combining maxNotificationsPerInterval and maxNotificationsPerBatch
	// We limited the max concurrent requests to our backend to: maxNotificationsPerInterval / maxNotificationsPerBatch = 100.
	// It also limits the max throughput to maxNotificationsPerInterval / notificationBatchInterval.
	maxNotificationsPerBatch    = 1000
	maxNotificationsPerInterval = 100_000
	notificationBatchInterval   = 100 * time.Millisecond
)

var ErrAlreadyRunning = errors.New("notification batcher is already running")

type notificationBatcher struct {
	mu            sync.Mutex
	notifications chan stacksapi.StackNotification
	stackKey      string
	client        *stacksapi.Client
	logger        *slog.Logger
	done          chan struct{}
	running       bool
}

func newNotificationBatcher(stackKey string, client *stacksapi.Client, logger *slog.Logger) *notificationBatcher {
	nb := &notificationBatcher{
		notifications: make(chan stacksapi.StackNotification, maxBufferSize),
		stackKey:      stackKey,
		client:        client,
		logger:        logger,
	}
	return nb
}

func (nb *notificationBatcher) add(ctx context.Context, notification stacksapi.StackNotification) error {
	select {
	case nb.notifications <- notification:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (nb *notificationBatcher) start(ctx context.Context) error {
	nb.mu.Lock()
	if nb.running {
		nb.mu.Unlock()
		return ErrAlreadyRunning
	}
	nb.running = true
	nb.done = make(chan struct{})
	nb.mu.Unlock()

	go func() {
		defer func() {
			nb.mu.Lock()
			nb.running = false
			nb.mu.Unlock()
			close(nb.done)
		}()

		ticker := time.NewTicker(notificationBatchInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				// We could do a final flush here.
				// But it still would be an incomplete graceful shutdown without connection tracking.
				// So we decided not to do it.
				return
			case <-ticker.C:
				nb.flush(ctx)
			}
		}
	}()

	return nil
}

func (nb *notificationBatcher) flush(ctx context.Context) {
	if len(nb.notifications) == 0 {
		return
	}

	count := min(len(nb.notifications), maxNotificationsPerInterval)
	notifications := make([]stacksapi.StackNotification, count)
	for i := range count {
		notifications[i] = <-nb.notifications
	}

	var wg sync.WaitGroup
	for batch := range slices.Chunk(notifications, maxNotificationsPerBatch) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			nb.sendBatch(ctx, batch)
		}()
	}
	wg.Wait()
}

func (nb *notificationBatcher) sendBatch(ctx context.Context, notifications []stacksapi.StackNotification) {
	req := stacksapi.CreateStackNotificationsRequest{
		StackKey:      nb.stackKey,
		Notifications: notifications,
	}

	resp, _, err := nb.client.CreateStackNotifications(ctx, req, stacksapi.WithNoRetry())
	if err != nil {
		nb.logger.Warn("Failed sending batch stack notifications",
			"error", err,
			"count", len(notifications))
		return
	}

	if len(resp.Errors) > 0 {
		for _, e := range resp.Errors {
			nb.logger.Warn("Stack notification errors for some notifications",
				"error", e.Error,
				"count", len(e.Indexes))
		}
	}
}

func (nb *notificationBatcher) waitDone() {
	<-nb.done
}
