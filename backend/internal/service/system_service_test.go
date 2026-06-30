package service

import (
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func newSystemServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Photo{}, &model.Device{}, &model.DisplayRecord{}))
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})
	return db
}

// TestSystemService_GetStats_PhotoCache 验证 /system/stats 走共享缓存并返回正确计数。
func TestSystemService_GetStats_PhotoCache(t *testing.T) {
	db := newSystemServiceTestDB(t)
	photoRepo := repository.NewPhotoRepository(db)
	now := time.Now()
	require.NoError(t, photoRepo.Create(&model.Photo{FilePath: "/a.jpg", FileHash: "h1", FileSize: 1000, AIAnalyzed: true, AnalyzedAt: &now}))
	require.NoError(t, photoRepo.Create(&model.Photo{FilePath: "/b.jpg", FileHash: "h2", FileSize: 2000, AIAnalyzed: false}))

	// 清空共享缓存，确保本次测试从 DB 加载
	invalidatePhotoStatsCache()

	svc := NewSystemService(db).(*systemService)
	stats, _, err := svc.GetStats()
	require.NoError(t, err)
	assert.Equal(t, int64(2), stats.TotalPhotos)
	assert.Equal(t, int64(1), stats.AnalyzedPhotos)
	assert.Equal(t, int64(1), stats.UnanalyzedPhotos)
	assert.Equal(t, int64(3000), stats.StorageSize)

	// GetStats 应填充共享缓存
	sharedPhotoStatsCache.mu.RLock()
	filled := sharedPhotoStatsCache.snapshot != nil
	sharedPhotoStatsCache.mu.RUnlock()
	assert.True(t, filled, "GetStats should populate shared photo stats cache")

	invalidatePhotoStatsCache()
}

// TestSystemService_GetStats_CacheServesStale 验证缓存命中时返回缓存值（短期最终一致）。
func TestSystemService_GetStats_CacheServesStale(t *testing.T) {
	db := newSystemServiceTestDB(t)
	photoRepo := repository.NewPhotoRepository(db)
	require.NoError(t, photoRepo.Create(&model.Photo{FilePath: "/a.jpg", FileHash: "h1", FileSize: 1000}))

	invalidatePhotoStatsCache()
	svc := NewSystemService(db).(*systemService)

	stats, _, err := svc.GetStats()
	require.NoError(t, err)
	assert.Equal(t, int64(1), stats.TotalPhotos)

	// 新增照片但缓存未失效：应返回缓存值 1
	require.NoError(t, photoRepo.Create(&model.Photo{FilePath: "/b.jpg", FileHash: "h2", FileSize: 1000}))
	stats, _, err = svc.GetStats()
	require.NoError(t, err)
	assert.Equal(t, int64(1), stats.TotalPhotos, "should serve cached value within TTL")

	// 失效后刷新
	invalidatePhotoStatsCache()
	stats, _, err = svc.GetStats()
	require.NoError(t, err)
	assert.Equal(t, int64(2), stats.TotalPhotos)

	invalidatePhotoStatsCache()
}
