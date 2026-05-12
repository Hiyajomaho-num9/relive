package database

import (
	"sync"
	"sync/atomic"
	"time"
)

// WriteOp represents a single write operation to be executed.
type WriteOp struct {
	Fn func() error
}

// BatchFlushFn is a function that executes a batch of write operations.
type BatchFlushFn func(ops []WriteOp) error

// WriteQueueConfig holds configuration for WriteQueue.
type WriteQueueConfig struct {
	BatchSize     int
	FlushInterval time.Duration
}

// WriteQueue serializes all database write operations for SQLite.
// It provides two modes:
//   - Execute(fn): synchronous write for critical path, blocks until done
//   - Enqueue(fn): async write for background ops, batched into one transaction
type WriteQueue struct {
	writeMu       sync.Mutex
	queue         chan WriteOp
	batchSize     int
	flushInterval time.Duration
	batchFlushFn  BatchFlushFn
	stopCh        chan struct{}
	wg            sync.WaitGroup
	executedCount uint64
	enqueuedCount uint64
	batchCount    uint64
}

// globalWriteQueue is the singleton instance.
var globalWriteQueue *WriteQueue

// InitWriteQueue creates the global WriteQueue with default config and starts it.
func InitWriteQueue() *WriteQueue {
	globalWriteQueue = NewWriteQueue(nil)
	return globalWriteQueue
}

// GetWriteQueue returns the global WriteQueue instance.
func GetWriteQueue() *WriteQueue {
	return globalWriteQueue
}

// NewWriteQueue creates a new WriteQueue. If cfg is nil, defaults are used.
func NewWriteQueue(cfg *WriteQueueConfig) *WriteQueue {
	batchSize := 50
	flushInterval := 5 * time.Second

	if cfg != nil {
		if cfg.BatchSize > 0 {
			batchSize = cfg.BatchSize
		}
		if cfg.FlushInterval > 0 {
			flushInterval = cfg.FlushInterval
		}
	}

	wq := &WriteQueue{
		queue:         make(chan WriteOp, batchSize*2),
		batchSize:     batchSize,
		flushInterval: flushInterval,
		stopCh:        make(chan struct{}),
	}

	wq.wg.Add(1)
	go wq.runBatchWriter()

	return wq
}

// SetBatchFlushFn injects the function used to execute batched operations.
func (wq *WriteQueue) SetBatchFlushFn(fn BatchFlushFn) {
	wq.batchFlushFn = fn
}

// Execute runs fn synchronously, serialized with all other writes.
func (wq *WriteQueue) Execute(fn func() error) error {
	wq.writeMu.Lock()
	defer wq.writeMu.Unlock()

	atomic.AddUint64(&wq.executedCount, 1)
	return fn()
}

// Enqueue pushes fn into the async write queue for batched execution.
func (wq *WriteQueue) Enqueue(fn func() error) {
	atomic.AddUint64(&wq.enqueuedCount, 1)
	wq.queue <- WriteOp{Fn: fn}
}

// runBatchWriter consumes from the queue, batching by size or timer.
func (wq *WriteQueue) runBatchWriter() {
	defer wq.wg.Done()

	ticker := time.NewTicker(wq.flushInterval)
	defer ticker.Stop()

	batch := make([]WriteOp, 0, wq.batchSize)

	for {
		select {
		case op := <-wq.queue:
			batch = append(batch, op)
			if len(batch) >= wq.batchSize {
				wq.flushBatch(batch)
				batch = batch[:0]
				ticker.Reset(wq.flushInterval)
			} else {
				// Drain remaining from channel (non-blocking)
				for {
					select {
					case op := <-wq.queue:
						batch = append(batch, op)
						if len(batch) >= wq.batchSize {
							wq.flushBatch(batch)
							batch = batch[:0]
							ticker.Reset(wq.flushInterval)
						}
					default:
						goto doneDraining
					}
				}
			doneDraining:
			}
		case <-ticker.C:
			if len(batch) > 0 {
				wq.flushBatch(batch)
				batch = batch[:0]
			}
		case <-wq.stopCh:
			// Drain remaining from queue
			for {
				select {
				case op := <-wq.queue:
					batch = append(batch, op)
				default:
					goto doneStop
				}
			}
		doneStop:
			if len(batch) > 0 {
				wq.flushBatch(batch)
			}
			return
		}
	}
}

// flushBatch executes a batch of operations, using batchFlushFn if set,
// otherwise executing each op sequentially under writeMu.
func (wq *WriteQueue) flushBatch(ops []WriteOp) {
	if len(ops) == 0 {
		return
	}

	wq.writeMu.Lock()
	defer wq.writeMu.Unlock()

	atomic.AddUint64(&wq.batchCount, 1)

	if wq.batchFlushFn != nil {
		_ = wq.batchFlushFn(ops)
	} else {
		for _, op := range ops {
			_ = op.Fn()
		}
	}
}

// Stop gracefully shuts down the WriteQueue, draining remaining operations.
func (wq *WriteQueue) Stop() {
	close(wq.stopCh)
	wq.wg.Wait()
}

// Stats returns current statistics.
func (wq *WriteQueue) Stats() map[string]interface{} {
	return map[string]interface{}{
		"executed":  atomic.LoadUint64(&wq.executedCount),
		"enqueued":  atomic.LoadUint64(&wq.enqueuedCount),
		"batches":   atomic.LoadUint64(&wq.batchCount),
		"queue_len": len(wq.queue),
	}
}
