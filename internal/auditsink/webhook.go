package auditsink

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Runewardd/runeward/internal/ledger"
)

const (
	// webhookQueueSize bounds how many events may be buffered before Emit
	// starts dropping to avoid blocking the ledger append path.
	webhookQueueSize = 1024
	// webhookPostTimeout caps a single POST attempt.
	webhookPostTimeout = 5 * time.Second
	// webhookMaxTries is the number of attempts per event (initial + retries).
	webhookMaxTries = 3
	// webhookBaseBackoff is the delay before the first retry; it doubles.
	webhookBaseBackoff = 200 * time.Millisecond
	// webhookCloseTimeout bounds how long Close waits to flush the queue.
	webhookCloseTimeout = 5 * time.Second
	// dropLogInterval throttles the "dropping events" warning.
	dropLogInterval = 10 * time.Second
)

// WebhookConfig configures a webhook sink. Only URL is required.
type WebhookConfig struct {
	URL         string
	HeaderKey   string
	HeaderValue string
	// Client is the HTTP client to use; nil uses a client with a sane
	// per-request timeout.
	Client *http.Client
	// Logger receives drop and failure warnings; nil uses slog.Default.
	Logger *slog.Logger
	// QueueSize overrides the default bounded queue size when > 0.
	QueueSize int
}

// webhookSink POSTs each event as JSON to a URL. A single background worker
// drains a bounded queue so Emit never blocks the caller.
type webhookSink struct {
	url         string
	headerKey   string
	headerValue string
	client      *http.Client
	logger      *slog.Logger

	queue chan ledger.Event
	done  chan struct{}
	wg    sync.WaitGroup

	dropped     atomic.Int64
	lastDropLog atomic.Int64 // unix nanos of last drop warning

	closeOnce sync.Once
}

// NewWebhookSink builds a webhook Sink and starts its worker goroutine.
func NewWebhookSink(cfg WebhookConfig) Sink {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: webhookPostTimeout}
	}
	size := cfg.QueueSize
	if size <= 0 {
		size = webhookQueueSize
	}

	s := &webhookSink{
		url:         cfg.URL,
		headerKey:   cfg.HeaderKey,
		headerValue: cfg.HeaderValue,
		client:      client,
		logger:      logger,
		queue:       make(chan ledger.Event, size),
		done:        make(chan struct{}),
	}
	s.wg.Add(1)
	go s.worker()
	return s
}

// Emit enqueues ev without blocking. If the queue is full the oldest event
// is discarded to make room, keeping the newest data flowing.
func (s *webhookSink) Emit(ev ledger.Event) {
	for {
		select {
		case s.queue <- ev:
			return
		default:
			// Queue full: drop the oldest event, then retry the send.
			select {
			case <-s.queue:
				s.recordDrop()
			default:
				// Someone else drained it; loop and try to enqueue again.
			}
		}
	}
}

// recordDrop increments the drop counter and logs a throttled warning.
func (s *webhookSink) recordDrop() {
	n := s.dropped.Add(1)
	now := time.Now().UnixNano()
	last := s.lastDropLog.Load()
	if now-last >= int64(dropLogInterval) && s.lastDropLog.CompareAndSwap(last, now) {
		s.logger.Warn("auditsink: webhook queue full, dropping events",
			"url", s.url, "dropped_total", n)
	}
}

func (s *webhookSink) worker() {
	defer s.wg.Done()
	for {
		select {
		case ev := <-s.queue:
			s.deliver(ev)
		case <-s.done:
			// Drain whatever is queued, then exit.
			for {
				select {
				case ev := <-s.queue:
					s.deliver(ev)
				default:
					return
				}
			}
		}
	}
}

// deliver POSTs a single event with retries and backoff. Failures are logged
// but never surface to the producer.
func (s *webhookSink) deliver(ev ledger.Event) {
	body, err := json.Marshal(ev)
	if err != nil {
		s.logger.Warn("auditsink: marshal event failed", "err", err, "seq", ev.Seq)
		return
	}

	backoff := webhookBaseBackoff
	for attempt := 1; attempt <= webhookMaxTries; attempt++ {
		if s.post(body) {
			return
		}
		if attempt < webhookMaxTries {
			select {
			case <-s.done:
				return
			case <-time.After(backoff):
			}
			backoff *= 2
		}
	}
	s.logger.Warn("auditsink: webhook delivery failed after retries",
		"url", s.url, "seq", ev.Seq, "tries", webhookMaxTries)
}

// post performs a single POST attempt, returning true on a 2xx response.
func (s *webhookSink) post(body []byte) bool {
	ctx, cancel := context.WithTimeout(context.Background(), webhookPostTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	if s.headerKey != "" {
		req.Header.Set(s.headerKey, s.headerValue)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return false
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// Close signals the worker to drain and stop, waiting up to a short timeout.
func (s *webhookSink) Close() error {
	s.closeOnce.Do(func() {
		close(s.done)
	})

	stopped := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(webhookCloseTimeout):
		s.logger.Warn("auditsink: webhook close timed out flushing queue", "url", s.url)
	}
	return nil
}
