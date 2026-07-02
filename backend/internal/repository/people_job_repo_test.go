package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPeopleJobRepository_ClaimNextJob(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPeopleJobRepository(db)
	now := time.Now()

	require.NoError(t, repo.Create(&model.PeopleJob{
		PhotoID:  1,
		FilePath: "/photos/1.jpg",
		Status:   model.PeopleJobStatusQueued,
		Source:   model.PeopleJobSourceScan,
		Priority: 1,
		QueuedAt: now,
	}))
	require.NoError(t, repo.Create(&model.PeopleJob{
		PhotoID:  2,
		FilePath: "/photos/2.jpg",
		Status:   model.PeopleJobStatusPending,
		Source:   model.PeopleJobSourceManual,
		Priority: 10,
		QueuedAt: now.Add(time.Minute),
	}))

	claimed, err := repo.ClaimNextJob()
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, uint(2), claimed.PhotoID)
	assert.Equal(t, model.PeopleJobStatusProcessing, claimed.Status)
	assert.Equal(t, 1, claimed.AttemptCount)
}

// setJobUpdatedAt 用原生 SQL 覆盖 updated_at，绕过 GORM 自动时间戳，便于构造历史记录。
func setJobUpdatedAt(t *testing.T, db *gorm.DB, id uint, ts time.Time) {
	t.Helper()
	require.NoError(t, db.Exec("UPDATE people_jobs SET updated_at = ? WHERE id = ?", ts, id).Error)
}

func TestPeopleJobRepository_ListTerminalIDsBefore(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewPeopleJobRepository(db)

	now := time.Now()
	old := now.Add(-8 * 24 * time.Hour) // 早于 7 天 cutoff
	cutoff := now.Add(-7 * 24 * time.Hour)

	// 应被选中的终态历史记录
	mustCreateJob(t, repo, 1, model.PeopleJobStatusCompleted, old)
	mustCreateJob(t, repo, 2, model.PeopleJobStatusFailed, old)
	mustCreateJob(t, repo, 3, model.PeopleJobStatusCancelled, old)
	// 保留期内的终态记录：不选中
	mustCreateJob(t, repo, 4, model.PeopleJobStatusCompleted, now)
	// 非终态历史记录：绝不选中
	mustCreateJob(t, repo, 5, model.PeopleJobStatusPending, old)
	mustCreateJob(t, repo, 6, model.PeopleJobStatusQueued, old)
	mustCreateJob(t, repo, 7, model.PeopleJobStatusProcessing, old)

	// 收集所有 ID 并回填 updated_at
	var jobs []model.PeopleJob
	require.NoError(t, db.Find(&jobs).Error)
	for _, j := range jobs {
		switch j.PhotoID {
		case 1, 2, 3, 5, 6, 7:
			setJobUpdatedAt(t, db, j.ID, old)
		case 4:
			setJobUpdatedAt(t, db, j.ID, now)
		}
	}

	ids, err := repo.ListTerminalIDsBefore(cutoff, 100)
	require.NoError(t, err)
	// 仅 1/2/3 命中：终态 + 早于 cutoff
	assert.Len(t, ids, 3)
	for _, id := range ids {
		assert.NotContains(t, []uint{photoIDToJobID(t, db, 4), photoIDToJobID(t, db, 5), photoIDToJobID(t, db, 6), photoIDToJobID(t, db, 7)}, id)
	}

	// limit 截断
	limited, err := repo.ListTerminalIDsBefore(cutoff, 2)
	require.NoError(t, err)
	assert.Len(t, limited, 2)

	// 空/非正 limit 不报错
	zero, err := repo.ListTerminalIDsBefore(cutoff, 0)
	require.NoError(t, err)
	assert.Empty(t, zero)
}

func TestPeopleJobRepository_DeleteByIDs(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewPeopleJobRepository(db)

	now := time.Now()
	j1 := mustCreateJob(t, repo, 1, model.PeopleJobStatusCompleted, now)
	j2 := mustCreateJob(t, repo, 2, model.PeopleJobStatusPending, now) // 非终态
	j3 := mustCreateJob(t, repo, 3, model.PeopleJobStatusCompleted, now)

	// 空列表：不删除、不报错
	n, err := repo.DeleteByIDs(nil)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)

	// 按 ID 删除：命中 j1、j3，j2 因不在列表保留
	n, err = repo.DeleteByIDs([]uint{j1.ID, j3.ID})
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)

	remaining, err := repo.ListTerminalIDsBefore(now.Add(time.Hour), 100)
	require.NoError(t, err)
	assert.Empty(t, remaining) // j2 是 pending，不会被 ListTerminalIDsBefore 选中
	exists, err := repo.GetByID(j2.ID)
	require.NoError(t, err)
	assert.NotNil(t, exists) // j2 仍在
}

// TestPeopleJobRepository_BatchedDeleteFlow 模拟调度器分批删除流程：
// 仅清理终态历史记录，保留非终态与保留期内记录。
func TestPeopleJobRepository_BatchedDeleteFlow(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewPeopleJobRepository(db)

	now := time.Now()
	old := now.Add(-8 * 24 * time.Hour)
	cutoff := now.Add(-7 * 24 * time.Hour)

	oldCompleted := mustCreateJob(t, repo, 1, model.PeopleJobStatusCompleted, old)
	mustCreateJob(t, repo, 2, model.PeopleJobStatusFailed, old)
	mustCreateJob(t, repo, 3, model.PeopleJobStatusCancelled, old)
	recentCompleted := mustCreateJob(t, repo, 4, model.PeopleJobStatusCompleted, now)
	queued := mustCreateJob(t, repo, 5, model.PeopleJobStatusQueued, old)

	var jobs []model.PeopleJob
	require.NoError(t, db.Find(&jobs).Error)
	for _, j := range jobs {
		ts := now
		if j.PhotoID != 4 {
			ts = old
		}
		setJobUpdatedAt(t, db, j.ID, ts)
	}

	batchSize := 2
	var totalDeleted int64
	for {
		ids, err := repo.ListTerminalIDsBefore(cutoff, batchSize)
		require.NoError(t, err)
		if len(ids) == 0 {
			break
		}
		n, err := repo.DeleteByIDs(ids)
		require.NoError(t, err)
		totalDeleted += n
		if len(ids) < batchSize {
			break
		}
	}

	assert.Equal(t, int64(3), totalDeleted)

	// 历史终态已删
	deleted, err := repo.GetByID(oldCompleted.ID)
	require.NoError(t, err)
	assert.Nil(t, deleted)
	// 保留期内终态保留
	kept, err := repo.GetByID(recentCompleted.ID)
	require.NoError(t, err)
	assert.NotNil(t, kept)
	// 非终态保留
	keptQueued, err := repo.GetByID(queued.ID)
	require.NoError(t, err)
	assert.NotNil(t, keptQueued)
}

func mustCreateJob(t *testing.T, repo PeopleJobRepository, photoID uint, status string, ts time.Time) *model.PeopleJob {
	t.Helper()
	job := &model.PeopleJob{
		PhotoID:  photoID,
		FilePath: fmt.Sprintf("/photos/%d.jpg", photoID),
		Status:   status,
		Source:   model.PeopleJobSourceScan,
		Priority: 0,
		QueuedAt: ts,
	}
	require.NoError(t, repo.Create(job))
	return job
}

func photoIDToJobID(t *testing.T, db *gorm.DB, photoID uint) uint {
	t.Helper()
	var j model.PeopleJob
	require.NoError(t, db.Where("photo_id = ?", photoID).First(&j).Error)
	return j.ID
}
