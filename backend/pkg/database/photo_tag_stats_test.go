package database

import (
	"fmt"
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// seedPhotoTags 直接写入 photo_tags 明细行，用于测试迁移与重建。
func seedPhotoTags(t *testing.T, db *gorm.DB, perTag int, tags ...string) {
	t.Helper()
	records := make([]model.PhotoTag, 0, perTag*len(tags))
	photoID := uint(1)
	for _, tag := range tags {
		for i := 0; i < perTag; i++ {
			records = append(records, model.PhotoTag{PhotoID: photoID, Tag: tag})
			photoID++
		}
	}
	// 每个 photo_id+tag 需唯一，这里 photoID 递增保证不冲突；先建对应 photos 行
	for i := uint(1); i < photoID; i++ {
		require.NoError(t, db.Create(&model.Photo{FilePath: fmt.Sprintf("/%d.jpg", i), FileHash: fmt.Sprintf("h%d", i)}).Error)
	}
	require.NoError(t, db.Create(&records).Error)
}

// TestMigratePhotoTagStatsTable_Idempotent 验证迁移幂等：重复执行不重复计数、不报错。
func TestMigratePhotoTagStatsTable_Idempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Photo{}, &model.PhotoTag{}, &model.PhotoTagStats{}, &model.AppConfig{}))

	seedPhotoTags(t, db, 3, "nature", "sky", "city")

	require.NoError(t, migratePhotoTagStatsTable(db))
	// nature=3, sky=3, city=3
	var n int64
	require.NoError(t, db.Table("photo_tag_stats").Count(&n).Error)
	assert.Equal(t, int64(3), n)
	var nature model.PhotoTagStats
	require.NoError(t, db.Where("tag = ?", "nature").First(&nature).Error)
	assert.Equal(t, int64(3), nature.PhotoCount)

	// 再次执行迁移：应直接跳过（已有 app_config 标记），计数不变
	require.NoError(t, migratePhotoTagStatsTable(db))
	require.NoError(t, db.Table("photo_tag_stats").Count(&n).Error)
	assert.Equal(t, int64(3), n)
	assert.Equal(t, int64(3), nature.PhotoCount)
}

// TestMigratePhotoTagStatsTable_NoRows 验证明细表为空时迁移不产生零计数残留。
func TestMigratePhotoTagStatsTable_NoRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Photo{}, &model.PhotoTag{}, &model.PhotoTagStats{}, &model.AppConfig{}))

	require.NoError(t, migratePhotoTagStatsTable(db))
	var n int64
	require.NoError(t, db.Table("photo_tag_stats").Count(&n).Error)
	assert.Equal(t, int64(0), n)
}

// TestRebuildPhotoTagStats_Consistency 验证重建可修复漂移：人为破坏统计后重建与明细一致，且可重复执行。
func TestRebuildPhotoTagStats_Consistency(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Photo{}, &model.PhotoTag{}, &model.PhotoTagStats{}, &model.AppConfig{}))

	seedPhotoTags(t, db, 2, "nature", "sky")
	// 人为注入漂移：错误计数 + 多余零计数行
	require.NoError(t, db.Exec(`INSERT INTO photo_tag_stats(tag, photo_count) VALUES('nature', 99),('ghost', 0)`).Error)

	require.NoError(t, RebuildPhotoTagStats(db))

	var nature model.PhotoTagStats
	require.NoError(t, db.Where("tag = ?", "nature").First(&nature).Error)
	assert.Equal(t, int64(2), nature.PhotoCount)
	var sky model.PhotoTagStats
	require.NoError(t, db.Where("tag = ?", "sky").First(&sky).Error)
	assert.Equal(t, int64(2), sky.PhotoCount)

	var n int64
	require.NoError(t, db.Table("photo_tag_stats").Count(&n).Error)
	assert.Equal(t, int64(2), n) // ghost 被清除

	// 重复执行结果一致
	require.NoError(t, RebuildPhotoTagStats(db))
	require.NoError(t, db.Table("photo_tag_stats").Count(&n).Error)
	assert.Equal(t, int64(2), n)
}

// TestRebuildPhotoTagStats_MatchesAggregation 验证重建结果与实时 GROUP BY 完全一致。
func TestRebuildPhotoTagStats_MatchesAggregation(t *testing.T) {
	db := openMigratedTestDB(t)
	// openMigratedTestDB 已通过 AutoMigrate 建立 photo_tag_stats 并执行迁移
	repo := newTagRepoForTest(db)

	// 写入不规则分布的标签
	require.NoError(t, repo.SyncTags(1, "a,b,c"))
	require.NoError(t, repo.SyncTags(2, "a,b"))
	require.NoError(t, repo.SyncTags(3, "a"))

	// 人为破坏后重建
	require.NoError(t, db.Exec(`UPDATE photo_tag_stats SET photo_count = photo_count + 1000`).Error)
	require.NoError(t, RebuildPhotoTagStats(db))

	// 对照实时聚合
	type agg struct {
		Tag   string
		Count int64
	}
	var expected []agg
	require.NoError(t, db.Table("photo_tags").Select("tag, COUNT(*) as count").Group("tag").Scan(&expected).Error)
	for _, e := range expected {
		var s model.PhotoTagStats
		require.NoError(t, db.Where("tag = ?", e.Tag).First(&s).Error)
		assert.Equal(t, e.Count, s.PhotoCount, "tag %s mismatch", e.Tag)
	}
}

// newTagRepoForTest 复用 repository 包等价的极简写入路径。
// 为避免跨包循环依赖，这里直接用 SyncTags 等价的 SQL 不便，故通过 AutoMigrate 后的内联写入。
func newTagRepoForTest(db *gorm.DB) tagWriter {
	return tagWriter{db: db}
}

type tagWriter struct{ db *gorm.DB }

func (w tagWriter) SyncTags(photoID uint, commaSeparated string) error {
	tags := model.SplitTags(commaSeparated)
	return w.db.Transaction(func(tx *gorm.DB) error {
		// 先建 photos 行（测试便利）
		var existing model.Photo
		if err := tx.First(&existing, photoID).Error; err == gorm.ErrRecordNotFound {
			if err := tx.Create(&model.Photo{FilePath: fmt.Sprintf("/%d.jpg", photoID), FileHash: fmt.Sprintf("h%d", photoID)}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("photo_id = ?", photoID).Delete(&model.PhotoTag{}).Error; err != nil {
			return err
		}
		for _, t := range tags {
			if err := tx.Create(&model.PhotoTag{PhotoID: photoID, Tag: t}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
