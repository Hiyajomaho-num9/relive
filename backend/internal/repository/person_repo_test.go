package repository

import (
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersonRepository_Create(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPersonRepository(db)

	person := &model.Person{
		Name:     "Alice",
		Category: model.PersonCategoryFamily,
	}
	require.NoError(t, repo.Create(person))
	assert.NotZero(t, person.ID)

	got, err := repo.GetByID(person.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "Alice", got.Name)
	assert.Equal(t, model.PersonCategoryFamily, got.Category)
}

func TestPersonRepository_MergePeopleUpdatesFaces(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	personRepo := NewPersonRepository(db)
	faceRepo := NewFaceRepository(db)

	target := &model.Person{Name: "Target", Category: model.PersonCategoryFriend}
	source := &model.Person{Name: "Source", Category: model.PersonCategoryStranger}
	require.NoError(t, personRepo.Create(target))
	require.NoError(t, personRepo.Create(source))

	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:      11,
		PersonID:     &target.ID,
		BBoxX:        0.1,
		BBoxY:        0.1,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.9,
		QualityScore: 0.9,
	}))
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:      12,
		PersonID:     &source.ID,
		BBoxX:        0.2,
		BBoxY:        0.2,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.92,
		QualityScore: 0.85,
	}))

	affectedPhotoIDs, err := personRepo.MergeInto(target.ID, []uint{source.ID})
	require.NoError(t, err)
	// 仅来源人物涉及的照片需要重算；仅含目标人物的照片（11）不返回。
	assert.ElementsMatch(t, []uint{12}, affectedPhotoIDs)

	mergedFaces, err := faceRepo.ListByPersonID(target.ID)
	require.NoError(t, err)
	require.Len(t, mergedFaces, 2)

	sourceAfter, err := personRepo.GetByID(source.ID)
	require.NoError(t, err)
	assert.Nil(t, sourceAfter)

	targetAfter, err := personRepo.GetByID(target.ID)
	require.NoError(t, err)
	require.NotNil(t, targetAfter)
	assert.Equal(t, 2, targetAfter.FaceCount)
	assert.Equal(t, 2, targetAfter.PhotoCount)
}

// TestPersonRepository_MergeInto_OnlySourcePhotosAffected 覆盖单来源、多来源与照片重叠场景：
// 仅来源人物涉及的照片计入受影响集合；仅含目标人物的照片不返回；多个来源或来源与目标
// 出现在同一照片时去重。
func TestPersonRepository_MergeInto_OnlySourcePhotosAffected(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	personRepo := NewPersonRepository(db)
	faceRepo := NewFaceRepository(db)

	// 不同分类，验证重算范围与分类无关、仅按归属变化判定。
	target := &model.Person{Name: "Target", Category: model.PersonCategoryFamily}
	source1 := &model.Person{Name: "Source1", Category: model.PersonCategoryFriend}
	source2 := &model.Person{Name: "Source2", Category: model.PersonCategoryAcquaintance}
	require.NoError(t, personRepo.Create(target))
	require.NoError(t, personRepo.Create(source1))
	require.NoError(t, personRepo.Create(source2))

	const (
		photoTargetOnly uint = 100 // 仅目标人物 → 不应返回
		photoS1Only     uint = 101 // 仅来源1
		photoS2Only     uint = 102 // 仅来源2
		photoS1S2       uint = 103 // 来源1+来源2 同照 → 去重后一次
		photoTargetS1   uint = 104 // 目标+来源1 同照 → 含来源人脸，应返回
	)

	mkFace := func(photoID uint, personID uint) *model.Face {
		return &model.Face{
			PhotoID:  photoID,
			PersonID: &personID,
			BBoxX:    0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
			Confidence: 0.9, QualityScore: 0.8,
		}
	}
	require.NoError(t, faceRepo.Create(mkFace(photoTargetOnly, target.ID)))
	require.NoError(t, faceRepo.Create(mkFace(photoS1Only, source1.ID)))
	require.NoError(t, faceRepo.Create(mkFace(photoS2Only, source2.ID)))
	require.NoError(t, faceRepo.Create(mkFace(photoS1S2, source1.ID)))
	require.NoError(t, faceRepo.Create(mkFace(photoS1S2, source2.ID)))
	require.NoError(t, faceRepo.Create(mkFace(photoTargetS1, target.ID)))
	require.NoError(t, faceRepo.Create(mkFace(photoTargetS1, source1.ID)))

	affectedPhotoIDs, err := personRepo.MergeInto(target.ID, []uint{source1.ID, source2.ID})
	require.NoError(t, err)
	// 仅来源人物涉及的照片：101、102、103（去重）、104；100（仅目标）不返回。
	assert.ElementsMatch(t, []uint{photoS1Only, photoS2Only, photoS1S2, photoTargetS1}, affectedPhotoIDs)

	// 合并后所有来源人脸归属目标。目标原有 2 张人脸 + 5 张来源人脸 = 7。
	mergedFaces, err := faceRepo.ListByPersonID(target.ID)
	require.NoError(t, err)
	assert.Len(t, mergedFaces, 7)

	for _, sid := range []uint{source1.ID, source2.ID} {
		gone, err := personRepo.GetByID(sid)
		require.NoError(t, err)
		assert.Nil(t, gone)
	}
}
