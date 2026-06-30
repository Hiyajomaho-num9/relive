package service

import (
	"sync"
	"time"
)

// photoStatsCacheTTL 共享照片统计缓存的存活时间。
// 较短 TTL 保证扫描/分析等写入后的最终一致性，无需在所有写入路径显式失效。
const photoStatsCacheTTL = 5 * time.Second

// photoStatsSnapshot 照片统计快照（活跃照片的总数 / 已分析 / 未分析 / 总占用）。
type photoStatsSnapshot struct {
	Total      int64
	Analyzed   int64
	Unanalyzed int64
	Size       int64
	fetchedAt  time.Time
}

// photoStatsCache 跨服务共享的照片统计缓存。
// /system/stats 与 /ai/progress（lite 模式）复用同一份缓存，避免 Dashboard 并发打开时
// 重复对照片表执行全量 COUNT 统计。
type photoStatsCache struct {
	mu       sync.RWMutex
	snapshot *photoStatsSnapshot
	ttl      time.Duration
}

// sharedPhotoStatsCache 进程级共享实例。
var sharedPhotoStatsCache = &photoStatsCache{ttl: photoStatsCacheTTL}

// get 返回缓存的快照；缓存缺失或过期时调用 load 重新加载。
// load 由调用方提供（systemService 使用 db 聚合查询，aiService 使用 photoRepo.GetPhotoStats），
// 两者产出语义相同的快照，因此无论谁填充缓存，另一方读取均正确。
func (c *photoStatsCache) get(load func() (photoStatsSnapshot, error)) (*photoStatsSnapshot, error) {
	c.mu.RLock()
	if c.snapshot != nil && time.Since(c.snapshot.fetchedAt) < c.ttl {
		snap := *c.snapshot
		c.mu.RUnlock()
		return &snap, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	// double-check：等待写锁期间可能已被其他 goroutine 填充
	if c.snapshot != nil && time.Since(c.snapshot.fetchedAt) < c.ttl {
		snap := *c.snapshot
		return &snap, nil
	}

	snap, err := load()
	if err != nil {
		// 加载失败时返回过期快照（若有），避免单次查询失败放大到所有调用方
		if c.snapshot != nil {
			stale := *c.snapshot
			return &stale, nil
		}
		return nil, err
	}
	snap.fetchedAt = time.Now()
	c.snapshot = &snap
	out := *c.snapshot
	return &out, nil
}

// invalidate 清除缓存。
func (c *photoStatsCache) invalidate() {
	c.mu.Lock()
	c.snapshot = nil
	c.mu.Unlock()
}

// invalidatePhotoStatsCache 清除共享照片统计缓存。
// 在照片写入（排除/恢复、批量删除等会立即改变计数的操作）后调用，缩短不一致窗口。
func invalidatePhotoStatsCache() {
	sharedPhotoStatsCache.invalidate()
}
