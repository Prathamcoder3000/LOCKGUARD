package audit

import (
	"context"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"lockguard/backend/internal/metrics"
	"lockguard/backend/internal/persistence"
)

// PipelineConfig configures the asynchronous audit queue and batch flusher.
type PipelineConfig struct {
	QueueCapacity  int           `json:"queue_capacity"`
	BatchSize      int           `json:"batch_size"`
	FlushInterval  time.Duration `json:"flush_interval"`
	MaxRetries     int           `json:"max_retries"`
	RetryBaseDelay time.Duration `json:"retry_base_delay"`
}

// DefaultPipelineConfig provides production-tuned defaults for non-blocking audit delivery.
func DefaultPipelineConfig() PipelineConfig {
	return PipelineConfig{
		QueueCapacity:  10000,
		BatchSize:      50,
		FlushInterval:  50 * time.Millisecond,
		MaxRetries:     3,
		RetryBaseDelay: 100 * time.Millisecond,
	}
}

// PipelineStats exposes live health metrics for the audit subsystem.
type PipelineStats struct {
	QueueSize      int    `json:"queue_size"`
	QueueCapacity  int    `json:"queue_capacity"`
	CreatedTotal   uint64 `json:"created_total"`
	PersistedTotal uint64 `json:"persisted_total"`
	FailedTotal    uint64 `json:"failed_total"`
}

// Pipeline asynchronously delivers audit events in batches to PostgreSQL without blocking Raft or HTTP requests.
type Pipeline struct {
	repo    persistence.AuditRepository
	metrics *metrics.Registry
	cfg     PipelineConfig

	queue chan *Event

	createdCount   atomic.Uint64
	persistedCount atomic.Uint64
	failedCount    atomic.Uint64

	ctx      context.Context
	cancel   context.CancelFunc
	workerWg sync.WaitGroup

	mu     sync.RWMutex
	closed bool
}

// NewPipeline creates a new audit Pipeline instance.
func NewPipeline(repo persistence.AuditRepository, reg *metrics.Registry, cfg PipelineConfig) *Pipeline {
	if cfg.QueueCapacity <= 0 {
		cfg.QueueCapacity = 10000
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 50 * time.Millisecond
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 3
	}
	if cfg.RetryBaseDelay <= 0 {
		cfg.RetryBaseDelay = 100 * time.Millisecond
	}

	ctx, cancel := context.WithCancel(context.Background())

	p := &Pipeline{
		repo:    repo,
		metrics: reg,
		cfg:     cfg,
		queue:   make(chan *Event, cfg.QueueCapacity),
		ctx:     ctx,
		cancel:  cancel,
	}

	return p
}

// Start spawns the background batch flush worker.
func (p *Pipeline) Start() {
	p.workerWg.Add(1)
	go p.workerLoop()
}

// RecordEvent enqueues an event for asynchronous batch persistence.
// It is non-blocking: if the queue is full, the event is safely dropped and failure counters incremented.
func (p *Pipeline) RecordEvent(ev *Event) bool {
	if ev == nil {
		return false
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.closed {
		p.failedCount.Add(1)
		p.incMetricFailed("pipeline_closed")
		return false
	}

	p.createdCount.Add(1)
	p.incMetricCreated(ev.EventType, ev.OperationStatus)

	select {
	case p.queue <- ev:
		p.updateQueueSizeGauge()
		return true
	default:
		// Queue is full: apply backpressure by dropping event and recording failure
		p.failedCount.Add(1)
		p.incMetricFailed("queue_full")
		return false
	}
}

func (p *Pipeline) workerLoop() {
	defer p.workerWg.Done()

	batch := make([]*Event, 0, p.cfg.BatchSize)
	ticker := time.NewTicker(p.cfg.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case ev, ok := <-p.queue:
			if !ok {
				// Queue was closed, flush any remaining events
				if len(batch) > 0 {
					p.flushBatch(batch)
				}
				return
			}
			batch = append(batch, ev)
			if len(batch) >= p.cfg.BatchSize {
				p.flushBatch(batch)
				batch = make([]*Event, 0, p.cfg.BatchSize)
			}

		case <-ticker.C:
			if len(batch) > 0 {
				p.flushBatch(batch)
				batch = make([]*Event, 0, p.cfg.BatchSize)
			}
		}
	}
}

// SetRepository dynamically updates the audit repository under safe concurrency controls.
func (p *Pipeline) SetRepository(repo persistence.AuditRepository) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.repo = repo
}

func (p *Pipeline) getRepo() persistence.AuditRepository {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.repo
}

func (p *Pipeline) flushBatch(batch []*Event) {
	repo := p.getRepo()
	if len(batch) == 0 || repo == nil {
		return
	}

	start := time.Now()
	records := make([]*persistence.AuditEventRecord, 0, len(batch))
	for _, ev := range batch {
		rec, err := ev.ToPersistenceRecord()
		if err == nil {
			records = append(records, rec)
		} else {
			p.failedCount.Add(1)
			p.incMetricFailed("validation_error")
		}
	}

	if len(records) == 0 {
		return
	}

	var persistedCount int
	var err error

	for attempt := 0; attempt <= p.cfg.MaxRetries; attempt++ {
		// Context with timeout for batch write
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		persistedCount, err = repo.InsertBatch(ctx, records)
		cancel()

		if err == nil {
			break
		}

		if attempt < p.cfg.MaxRetries {
			// Exponential backoff with random jitter
			jitter := time.Duration(rand.Int63n(int64(p.cfg.RetryBaseDelay)))
			backoff := (p.cfg.RetryBaseDelay * time.Duration(1<<attempt)) + jitter
			time.Sleep(backoff)
		}
	}

	latency := time.Since(start).Seconds()
	p.recordLatency(latency)
	p.updateQueueSizeGauge()

	if err != nil {
		// Outage persists after retries: increment failed count
		p.failedCount.Add(uint64(len(records)))
		p.incMetricFailed("db_insert_failed")
	} else {
		p.persistedCount.Add(uint64(persistedCount))
		p.incMetricPersisted(persistedCount)
	}
}

// Shutdown gracefully drains remaining queued audit events and stops workers within context timeout.
func (p *Pipeline) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	close(p.queue)
	p.mu.Unlock()

	done := make(chan struct{})
	go func() {
		p.workerWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		p.cancel()
		return ctx.Err()
	}
}

// Stats returns a snapshot of pipeline delivery counters.
func (p *Pipeline) Stats() PipelineStats {
	return PipelineStats{
		QueueSize:      len(p.queue),
		QueueCapacity:  p.cfg.QueueCapacity,
		CreatedTotal:   p.createdCount.Load(),
		PersistedTotal: p.persistedCount.Load(),
		FailedTotal:    p.failedCount.Load(),
	}
}

func (p *Pipeline) incMetricCreated(eventType, status string) {
	if p.metrics != nil {
		p.metrics.IncCounter("lockguard_audit_events_created_total", map[string]string{
			"event_type": eventType,
			"status":     status,
		})
	}
}

func (p *Pipeline) incMetricPersisted(count int) {
	if p.metrics != nil {
		for i := 0; i < count; i++ {
			p.metrics.IncCounter("lockguard_audit_events_persisted_total", map[string]string{
				"status": "success",
			})
		}
	}
}

func (p *Pipeline) incMetricFailed(reason string) {
	if p.metrics != nil {
		p.metrics.IncCounter("lockguard_audit_events_failed_total", map[string]string{
			"reason": reason,
		})
	}
}

func (p *Pipeline) recordLatency(seconds float64) {
	if p.metrics != nil {
		p.metrics.SetGauge("lockguard_audit_delivery_latency_seconds", nil, int64(seconds*1000))
	}
}

func (p *Pipeline) updateQueueSizeGauge() {
	if p.metrics != nil {
		p.metrics.SetGauge("lockguard_audit_queue_size", nil, int64(len(p.queue)))
	}
}
