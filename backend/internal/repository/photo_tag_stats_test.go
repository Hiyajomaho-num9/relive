package repository

import (
	"fmt"
	"sync"
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// statsCount 读取某标签的统计计数（不存在返回 -1）
func statsCount(t *testing.T, db *gorm.DB, tag string) int64 {
	t.Helper()
	var stats model.PhotoTagStats
	err := db.Where("tag = ?", tag).First(&stats).Error
	if err == gorm.ErrRecordNotFound {
		return -1
	}
	require.NoError(t, err)
	return stats.PhotoCount
}

// statsRows 返回统计表行数
func statsRows(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Table("photo_tag_stats").Count(&n).Error)
	return n
}

// TestPhotoTagStats_SyncIncrementDecrement 验证 SyncTags 的增减计数：
// 新增标签 +1、替换标签增减、清空标签归零删除、重复同步幂等。
func TestPhotoTagStats_SyncIncrementDecrement(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewPhotoTagRepository(db)
	photoRepo := NewPhotoRepository(db)

	require.NoError(t, photoRepo.Create(&model.Photo{FilePath: "/a.jpg", FileHash: "h1"}))

	// 新增两个标签 → 各 +1
	require.NoError(t, repo.SyncTags(1, "nature,sky"))
	assert.Equal(t, int64(1), statsCount(t, db, "nature"))
	assert.Equal(t, int64(1), statsCount(t, db, "sky"))
	assert.Equal(t, int64(2), statsRows(t, db))

	// 另一张照片加 nature → nature +1，sky 不变
	require.NoError(t, photoRepo.Create(&model.Photo{FilePath: "/b.jpg", FileHash: "h2"}))
	require.NoError(t, repo.SyncTags(2, "nature,city"))
	assert.Equal(t, int64(2), statsCount(t, db, "nature"))
	assert.Equal(t, int64(1), statsCount(t, db, "sky"))
	assert.Equal(t, int64(1), statsCount(t, db, "city"))

	// 替换照片1的标签：sky → city，nature 保留
	require.NoError(t, repo.SyncTags(1, "nature,city"))
	assert.Equal(t, int64(2), statsCount(t, db, "nature")) // 仍 2
	assert.Equal(t, int64(2), statsCount(t, db, "city"))   // 1→2
	assert.Equal(t, int64(-1), statsCount(t, db, "sky"))   // 归零删除

	// 幂等：再次同步相同标签，计数不变
	require.NoError(t, repo.SyncTags(1, "nature,city"))
	assert.Equal(t, int64(2), statsCount(t, db, "nature"))
	assert.Equal(t, int64(2), statsCount(t, db, "city"))

	// 清空标签
	require.NoError(t, repo.SyncTags(1, ""))
	assert.Equal(t, int64(1), statsCount(t, db, "nature")) // 仅照片2
	assert.Equal(t, int64(1), statsCount(t, db, "city"))
}

// TestPhotoTagStats_DeleteByPhotoID 验证删除照片时统计正确递减并清理零计数行。
func TestPhotoTagStats_DeleteByPhotoID(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewPhotoTagRepository(db)
	photoRepo := NewPhotoRepository(db)

	require.NoError(t, photoRepo.Create(&model.Photo{FilePath: "/a.jpg", FileHash: "h1"}))
	require.NoError(t, photoRepo.Create(&model.Photo{FilePath: "/b.jpg", FileHash: "h2"}))
	require.NoError(t, repo.SyncTags(1, "nature,sky"))
	require.NoError(t, repo.SyncTags(2, "nature,city"))
	// nature=2, sky=1, city=1

	require.NoError(t, repo.DeleteTagsByPhotoID(1))
	assert.Equal(t, int64(1), statsCount(t, db, "nature")) // 2→1
	assert.Equal(t, int64(-1), statsCount(t, db, "sky"))   // 归零删除
	assert.Equal(t, int64(1), statsCount(t, db, "city"))   // 不变

	// 重复删除幂等
	require.NoError(t, repo.DeleteTagsByPhotoID(1))
	assert.Equal(t, int64(1), statsCount(t, db, "nature"))
}

// TestPhotoTagStats_BatchMigrate 验证批量迁移后统计与明细表一致（含重复行 OnConflict 跳过）。
func TestPhotoTagStats_BatchMigrate(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewPhotoTagRepository(db)
	photoRepo := NewPhotoRepository(db)

	for i := 1; i <= 3; i++ {
		require.NoError(t, photoRepo.Create(&model.Photo{
			FilePath: fmt.Sprintf("/%d.jpg", i), FileHash: fmt.Sprintf("h%d", i),
		}))
	}

	items := []struct {
		ID   uint
		Tags string
	}{
		{1, "nature,sky"},
		{2, "nature,city"},
		{3, "nature,sky"},
	}
	require.NoError(t, repo.BatchMigrate(items))

	assert.Equal(t, int64(3), statsCount(t, db, "nature"))
	assert.Equal(t, int64(2), statsCount(t, db, "sky"))
	assert.Equal(t, int64(1), statsCount(t, db, "city"))

	// 重复迁移同一批（OnConflict 跳过），统计不应翻倍
	require.NoError(t, repo.BatchMigrate(items))
	assert.Equal(t, int64(3), statsCount(t, db, "nature"))
	assert.Equal(t, int64(2), statsCount(t, db, "sky"))
	assert.Equal(t, int64(1), statsCount(t, db, "city"))
}

// TestPhotoTagStats_GetTagsFromStats 验证 GetTags/CountTags 走统计表，排序与去重正确。
func TestPhotoTagStats_GetTagsFromStats(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewPhotoTagRepository(db)
	photoRepo := NewPhotoRepository(db)

	for i := 1; i <= 3; i++ {
		require.NoError(t, photoRepo.Create(&model.Photo{
			FilePath: fmt.Sprintf("/%d.jpg", i), FileHash: fmt.Sprintf("h%d", i),
		}))
	}
	require.NoError(t, repo.SyncTags(1, "nature,sky"))
	require.NoError(t, repo.SyncTags(2, "nature,city"))
	require.NoError(t, repo.SyncTags(3, "nature"))

	tags, err := photoRepo.GetTags("", 50)
	require.NoError(t, err)
	// nature=3 排首位
	assert.Equal(t, "nature", tags[0].Tag)
	assert.Equal(t, 3, tags[0].Count)

	total, err := photoRepo.CountTags()
	require.NoError(t, err)
	assert.Equal(t, int64(3), total) // nature, sky, city

	// 搜索
	filtered, err := photoRepo.GetTags("nat", 50)
	require.NoError(t, err)
	assert.Len(t, filtered, 1)
	assert.Equal(t, "nature", filtered[0].Tag)
}

// TestPhotoTagStats_ConcurrentUpdates 验证并发同步不会造成计数丢失或负数。
// SQLite 串行写入（MaxOpenConns=1），多 goroutine 通过 SyncTags 各自开事务竞争。
func TestPhotoTagStats_ConcurrentUpdates(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	// 强制单连接，模拟生产 SQLite 串行写入
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)

	repo := NewPhotoTagRepository(db)
	photoRepo := NewPhotoRepository(db)

	const N = 20
	for i := 1; i <= N; i++ {
		require.NoError(t, photoRepo.Create(&model.Photo{
			FilePath: fmt.Sprintf("/%d.jpg", i), FileHash: fmt.Sprintf("h%d", i),
		}))
	}

	var wg sync.WaitGroup
	for i := 1; i <= N; i++ {
		wg.Add(1)
		go func(id uint) {
			defer wg.Done()
			// 每张照片都带 "shared" 标签，外加一个独有标签
			_ = repo.SyncTags(id, fmt.Sprintf("shared,private%d", id))
		}(uint(i))
	}
	wg.Wait()

	// shared 应为 N，无丢失
	assert.Equal(t, int64(N), statsCount(t, db, "shared"))
	// 每个 privateX 应为 1，无负数
	for i := 1; i <= N; i++ {
		assert.Equal(t, int64(1), statsCount(t, db, fmt.Sprintf("private%d", i)))
	}
	// 统计表无负数行
	var negCount int64
	require.NoError(t, db.Table("photo_tag_stats").Where("photo_count < 0").Count(&negCount).Error)
	assert.Equal(t, int64(0), negCount)
}
