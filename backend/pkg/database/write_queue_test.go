package database

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWriteQueue_Execute_SerializesWrites(t *testing.T) {
	wq := NewWriteQueue(nil)
	defer wq.Stop()

	var maxConcurrent int32
	var currentConcurrent int32

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = wq.Execute(func() error {
				cur := atomic.AddInt32(&currentConcurrent, 1)
				// Track max concurrent executions
				for {
					old := atomic.LoadInt32(&maxConcurrent)
					if cur <= old || atomic.CompareAndSwapInt32(&maxConcurrent, old, cur) {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
				atomic.AddInt32(&currentConcurrent, -1)
				return nil
			})
		}()
	}
	wg.Wait()

	if maxConcurrent != 1 {
		t.Errorf("expected maxConcurrent == 1, got %d", maxConcurrent)
	}
}

func TestWriteQueue_Enqueue_BatchFlush(t *testing.T) {
	wq := NewWriteQueue(&WriteQueueConfig{
		BatchSize:     3,
		FlushInterval: 10 * time.Second, // long interval so batch size triggers flush
	})
	defer wq.Stop()

	var flushCount int32
	wq.SetBatchFlushFn(func(ops []WriteOp) error {
		atomic.AddInt32(&flushCount, 1)
		for _, op := range ops {
			if err := op.Fn(); err != nil {
				return err
			}
		}
		return nil
	})

	for i := 0; i < 3; i++ {
		wq.Enqueue(func() error {
			return nil
		})
	}

	// Wait for batch writer to process
	time.Sleep(200 * time.Millisecond)

	if atomic.LoadInt32(&flushCount) != 1 {
		t.Errorf("expected batchFlushFn called once, got %d", atomic.LoadInt32(&flushCount))
	}
}

func TestWriteQueue_Enqueue_TimeFlush(t *testing.T) {
	wq := NewWriteQueue(&WriteQueueConfig{
		BatchSize:     100, // high batch size so it won't trigger by count
		FlushInterval: 50 * time.Millisecond,
	})
	defer wq.Stop()

	var flushed int32
	wq.SetBatchFlushFn(func(ops []WriteOp) error {
		atomic.AddInt32(&flushed, 1)
		for _, op := range ops {
			if err := op.Fn(); err != nil {
				return err
			}
		}
		return nil
	})

	wq.Enqueue(func() error {
		return nil
	})

	// Should not be flushed immediately
	time.Sleep(10 * time.Millisecond)
	if atomic.LoadInt32(&flushed) != 0 {
		t.Errorf("expected no flush before interval, got %d", atomic.LoadInt32(&flushed))
	}

	// Should be flushed after interval
	time.Sleep(100 * time.Millisecond)
	if atomic.LoadInt32(&flushed) != 1 {
		t.Errorf("expected flush after interval, got %d", atomic.LoadInt32(&flushed))
	}
}

func TestWriteQueue_Stats(t *testing.T) {
	wq := NewWriteQueue(nil)
	defer wq.Stop()

	_ = wq.Execute(func() error { return nil })
	_ = wq.Execute(func() error { return nil })

	stats := wq.Stats()
	if stats["executed"] != uint64(2) {
		t.Errorf("expected stats[executed] == 2, got %v", stats["executed"])
	}
}
