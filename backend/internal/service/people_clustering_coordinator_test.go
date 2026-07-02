package service

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCoordinatorSerializesBackgroundAndFeedback verifies requirement #1: when
// background clustering and feedback recluster are submitted concurrently, the
// maximum execution concurrency is 1 (they never overlap).
func TestCoordinatorSerializesBackgroundAndFeedback(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	feedbackStarted := make(chan struct{}, 1)
	releaseFeedback := make(chan struct{})
	svc.setFeedbackReclusterHookForTest(func() model.ReclusterResult {
		select {
		case feedbackStarted <- struct{}{}:
		default:
		}
		<-releaseFeedback
		return model.ReclusterResult{Evaluated: 1, Reassigned: 1}
	})
	t.Cleanup(func() {
		svc.setFeedbackReclusterHookForTest(nil)
		select {
		case <-releaseFeedback:
		default:
			close(releaseFeedback)
		}
	})

	// Start a feedback recluster (occupies the single worker).
	svc.scheduleFeedbackRecluster()
	waitForPeopleCondition(t, time.Second, func() bool {
		select {
		case <-feedbackStarted:
			return true
		default:
			return false
		}
	})

	// Submit a background batch from another goroutine. It must block until the
	// worker is free (i.e. until the feedback hook releases).
	bgDone := make(chan struct{})
	var bgRes backgroundClusterResult
	go func() {
		bgRes = svc.clusteringCoordinator.submitBackground()
		close(bgDone)
	}()

	// While feedback is running, background must not complete.
	select {
	case <-bgDone:
		t.Fatal("background clustering completed concurrently with feedback; expected serialization")
	case <-time.After(50 * time.Millisecond):
	}

	// Release feedback; background should now run and complete.
	close(releaseFeedback)
	select {
	case <-bgDone:
	case <-time.After(time.Second):
		t.Fatal("background clustering did not complete after feedback released")
	}
	assert.NoError(t, bgRes.err)
}

// TestCoordinatorForegroundPriorityBlocksNextBatch verifies requirements #4 and
// #5: when a foreground mutation arrives while a clustering batch is running,
// the coordinator does not start the next batch once the running one finishes;
// the foreground mutation runs first, and only after it ends does the next
// clustering batch run.
func TestCoordinatorForegroundPriorityBlocksNextBatch(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	releaseBatch := make(chan struct{})
	var runs atomic.Int32
	// The hook holds the write gate (like a real clustering batch) so a
	// foreground mutation blocks on writeGate.Lock() until the batch ends.
	svc.setFeedbackReclusterHookForTest(func() model.ReclusterResult {
		runs.Add(1)
		svc.writeGate.RLock()
		defer svc.writeGate.RUnlock()
		<-releaseBatch
		return model.ReclusterResult{Evaluated: 1, Reassigned: 1}
	})
	t.Cleanup(func() {
		svc.setFeedbackReclusterHookForTest(nil)
		select {
		case <-releaseBatch:
		default:
			close(releaseBatch)
		}
	})

	// Start the first feedback batch.
	svc.scheduleFeedbackRecluster()
	waitForPeopleCondition(t, time.Second, func() bool { return runs.Load() >= 1 })

	// A foreground mutation arrives and waits for the running batch to release
	// the write gate. Queue a second feedback (the "next batch") so there is
	// pending work the coordinator must NOT start while foreground is waiting.
	svc.scheduleFeedbackRecluster()

	// Register the foreground intent BEFORE releasing the batch so the worker
	// observes foregroundWaiters>0 when it re-checks after the batch ends.
	svc.clusteringCoordinator.addForegroundWaiter()

	fgAcquired := make(chan struct{})
	fgRelease := make(chan struct{})
	go func() {
		svc.writeGate.Lock()
		close(fgAcquired)
		<-fgRelease
		svc.writeGate.Unlock()
		svc.clusteringCoordinator.removeForegroundWaiter()
	}()

	// End the running batch. The foreground mutation should acquire the gate
	// next; the second feedback batch must NOT start.
	close(releaseBatch)
	waitForPeopleCondition(t, time.Second, func() bool {
		select {
		case <-fgAcquired:
			return true
		default:
			return false
		}
	})

	// Give the coordinator a moment to (incorrectly) start the next batch.
	// The runs counter (not the buffered batchStarted channel) is the guard:
	// it must stay at 1 while the foreground mutation is active.
	time.Sleep(80 * time.Millisecond)
	assert.Equal(t, int32(1), runs.Load(), "next clustering batch started while foreground mutation was waiting; priority violated")

	// End the foreground mutation; the pending feedback batch may now run.
	close(fgRelease)
	waitForPeopleCondition(t, time.Second, func() bool { return runs.Load() >= 2 })
}

// TestCoordinatorPendingFeedbackResumesAfterForeground verifies requirement #6:
// after a foreground operation ends, a feedback recluster that was pending
// during the foreground op runs automatically.
func TestCoordinatorPendingFeedbackResumesAfterForeground(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	var runs atomic.Int32
	firstStarted := make(chan struct{}, 1)
	releaseFirst := make(chan struct{})
	svc.setFeedbackReclusterHookForTest(func() model.ReclusterResult {
		n := runs.Add(1)
		if n == 1 {
			select {
			case firstStarted <- struct{}{}:
			default:
			}
			<-releaseFirst
		}
		return model.ReclusterResult{Evaluated: 1, Reassigned: 1}
	})
	t.Cleanup(func() {
		svc.setFeedbackReclusterHookForTest(nil)
		select {
		case <-releaseFirst:
		default:
			close(releaseFirst)
		}
	})

	// First feedback run occupies the worker.
	svc.scheduleFeedbackRecluster()
	waitForPeopleCondition(t, time.Second, func() bool { return runs.Load() >= 1 })

	// While the first run is in progress, a foreground mutation begins (this
	// also schedules a feedback recluster at the end, like MergePeople does).
	svc.beginForegroundMutation()
	// Schedule a feedback request during the foreground window + first run.
	svc.scheduleFeedbackRecluster()
	svc.scheduleFeedbackRecluster()

	// Release the first run. The worker finishes it but must wait for the
	// foreground mutation before starting the makeup run.
	close(releaseFirst)
	time.Sleep(60 * time.Millisecond)
	assert.Equal(t, int32(1), runs.Load(), "makeup feedback must wait for foreground to end")

	// End the foreground mutation; the pending makeup feedback resumes.
	svc.endForegroundMutation()
	waitForPeopleCondition(t, time.Second, func() bool { return runs.Load() >= 2 })
}

// TestCoordinatorForegroundCountRecoveryOnError verifies requirement #7: when
// MergePeople, SplitPerson, MoveFaces, and DissolvePerson exit on error, the
// foreground waiter count is correctly restored to zero.
func TestCoordinatorForegroundCountRecoveryOnError(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})
	_ = db

	// MergePeople with a non-existent target: MergeInto returns ErrRecordNotFound.
	_, err := svc.MergePeople(999999, []uint{1})
	require.Error(t, err)
	assert.Equal(t, 0, svc.clusteringCoordinator.foregroundWaiterCount())

	// SplitPerson with non-existent face IDs.
	_, _, err = svc.SplitPerson([]uint{999999})
	require.Error(t, err)
	assert.Equal(t, 0, svc.clusteringCoordinator.foregroundWaiterCount())

	// MoveFaces with non-existent face IDs.
	_, err = svc.MoveFaces([]uint{999999}, 1)
	require.Error(t, err)
	assert.Equal(t, 0, svc.clusteringCoordinator.foregroundWaiterCount())

	// DissolvePerson with non-existent person.
	_, err = svc.DissolvePerson(999999)
	require.Error(t, err)
	assert.Equal(t, 0, svc.clusteringCoordinator.foregroundWaiterCount())
}

// TestCoordinatorPanicRecovery verifies requirement #8: if a clustering
// (feedback) job panics, the coordinator recovers, releases its running state,
// and can execute the next task.
func TestCoordinatorPanicRecovery(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	var runs atomic.Int32
	panicOnce := sync.Once{}
	svc.setFeedbackCooldownForTest(5 * time.Millisecond)
	svc.setFeedbackReclusterHookForTest(func() (r model.ReclusterResult) {
		runs.Add(1)
		panicOnce.Do(func() {
			panic("simulated clustering panic")
		})
		return model.ReclusterResult{Evaluated: 1, Reassigned: 1}
	})
	t.Cleanup(func() { svc.setFeedbackReclusterHookForTest(nil) })

	// First schedule: panics. The coordinator must recover and not stay in
	// running=true forever.
	svc.scheduleFeedbackRecluster()
	waitForPeopleCondition(t, time.Second, func() bool { return runs.Load() >= 1 })

	// running must be false after the panic was recovered.
	waitForPeopleCondition(t, time.Second, func() bool {
		return !svc.clusteringCoordinator.isRunning()
	})

	// Second schedule: should execute normally, proving the coordinator
	// continued after the panic.
	svc.scheduleFeedbackRecluster()
	waitForPeopleCondition(t, time.Second, func() bool { return runs.Load() >= 2 })
}

// TestCoordinatorBackgroundDoesNotHoldWriteGateWhileWaiting verifies requirement
// #3: a background caller blocked in submitBackground (waiting for the
// coordinator worker) does not hold writeGate.RLock, so a foreground mutation
// can still acquire writeGate.Lock().
func TestCoordinatorBackgroundDoesNotHoldWriteGateWhileWaiting(t *testing.T) {
	svc, _ := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	// Occupy the worker with a feedback hook that does NOT hold the write gate.
	feedbackStarted := make(chan struct{}, 1)
	releaseFeedback := make(chan struct{})
	svc.setFeedbackReclusterHookForTest(func() model.ReclusterResult {
		select {
		case feedbackStarted <- struct{}{}:
		default:
		}
		<-releaseFeedback
		return model.ReclusterResult{Evaluated: 1, Reassigned: 1}
	})
	t.Cleanup(func() {
		svc.setFeedbackReclusterHookForTest(nil)
		select {
		case <-releaseFeedback:
		default:
			close(releaseFeedback)
		}
	})

	svc.scheduleFeedbackRecluster()
	waitForPeopleCondition(t, time.Second, func() bool {
		select {
		case <-feedbackStarted:
			return true
		default:
			return false
		}
	})

	// A background caller now blocks waiting for the worker.
	bgDone := make(chan struct{})
	go func() {
		_ = svc.clusteringCoordinator.submitBackground()
		close(bgDone)
	}()
	// Let the background caller settle into the wait.
	time.Sleep(30 * time.Millisecond)

	// While it waits, a foreground writeGate.Lock() must succeed immediately —
	// proving the background caller is NOT holding writeGate.RLock.
	acquired := make(chan struct{}, 1)
	go func() {
		svc.writeGate.Lock()
		acquired <- struct{}{}
		svc.writeGate.Unlock()
	}()
	select {
	case <-acquired:
		// good: foreground acquired the gate while background was waiting
	case <-time.After(300 * time.Millisecond):
		t.Fatal("writeGate.Lock blocked while background caller waited for coordinator; background held RLock")
	}

	// Background caller must still be waiting (worker busy).
	select {
	case <-bgDone:
		t.Fatal("background caller completed before worker was freed; serialization broken")
	default:
	}

	close(releaseFeedback)
	select {
	case <-bgDone:
	case <-time.After(time.Second):
		t.Fatal("background caller did not complete after worker was freed")
	}
}

// TestCoordinatorClusteringEquivalence verifies requirement #10: clustering a
// synthetic dataset through the coordinator produces the same person assignment
// the core algorithm would produce directly (the coordinator does not alter
// clustering semantics). A pending face identical to an existing person's
// prototype must attach to that person.
func TestCoordinatorClusteringEquivalence(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)

	protoPhoto := &model.Photo{FilePath: "/photos/proto.jpg", FileName: "proto.jpg", FileSize: 1, FileHash: "proto", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	pendingPhoto := &model.Photo{FilePath: "/photos/pending.jpg", FileName: "pending.jpg", FileSize: 1, FileHash: "pending", Width: 100, Height: 100, Status: model.PhotoStatusActive}
	require.NoError(t, photoRepo.Create(protoPhoto))
	require.NoError(t, photoRepo.Create(pendingPhoto))

	person := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, personRepo.Create(person))

	// Assigned prototype face for the person.
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       protoPhoto.ID,
		PersonID:      &person.ID,
		BBoxX:         0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence:    0.95,
		QualityScore:  0.80,
		Embedding:     encodeEmbedding(t, []float32{1, 0, 0}),
		ClusterStatus: model.FaceClusterStatusAssigned,
		ClusterScore:  0.95,
	}))
	require.NoError(t, personRepo.RefreshStats(person.ID))

	// Pending face identical to the prototype → similarity 1.0, must attach.
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       pendingPhoto.ID,
		BBoxX:         0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence:    0.99,
		QualityScore:  0.80,
		Embedding:     encodeEmbedding(t, []float32{1, 0, 0}),
		ClusterStatus: model.FaceClusterStatusPending,
	}))

	res := svc.clusteringCoordinator.submitBackground()
	require.NoError(t, res.err)

	pendingFaces, err := faceRepo.ListByPhotoID(pendingPhoto.ID)
	require.NoError(t, err)
	require.Len(t, pendingFaces, 1)
	require.NotNil(t, pendingFaces[0].PersonID)
	assert.Equal(t, person.ID, *pendingFaces[0].PersonID, "pending face should attach to existing person via coordinator")
	assert.Equal(t, model.FaceClusterStatusAssigned, pendingFaces[0].ClusterStatus)
}

// TestCoordinatorProtoCacheNoDataRace verifies requirement #11: concurrent
// clustering batches (which rebuild/reuse protoCache) and foreground mutations
// do not race on protoCache or coordinator state. Run with -race to detect.
func TestCoordinatorProtoCacheNoDataRace(t *testing.T) {
	svc, db := newPeopleServiceForTest(t, &fakePeopleMLClient{})

	photoRepo := repository.NewPhotoRepository(db)
	personRepo := repository.NewPersonRepository(db)
	faceRepo := repository.NewFaceRepository(db)

	// Seed two persons with prototype faces plus several pending faces.
	personA := &model.Person{Category: model.PersonCategoryFamily}
	personB := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(personA))
	require.NoError(t, personRepo.Create(personB))

	seedFace := func(photoID uint, pid *uint, emb []float32, status string) {
		require.NoError(t, faceRepo.Create(&model.Face{
			PhotoID: photoID, PersonID: pid,
			BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
			Confidence: 0.9, QualityScore: 0.8,
			Embedding:     encodeEmbedding(t, emb),
			ClusterStatus: status,
		}))
	}

	for i := 0; i < 4; i++ {
		photo := &model.Photo{
			FilePath: fmt.Sprintf("/photos/race-%d.jpg", i), FileName: fmt.Sprintf("race-%d.jpg", i),
			FileSize: 1, FileHash: fmt.Sprintf("race-%d", i), Width: 100, Height: 100, Status: model.PhotoStatusActive,
		}
		require.NoError(t, photoRepo.Create(photo))
		switch i {
		case 0:
			seedFace(photo.ID, &personA.ID, []float32{1, 0, 0}, model.FaceClusterStatusAssigned)
		case 1:
			seedFace(photo.ID, &personB.ID, []float32{0, 1, 0}, model.FaceClusterStatusAssigned)
		default:
			seedFace(photo.ID, nil, []float32{1, 0, 0}, model.FaceClusterStatusPending)
		}
	}
	require.NoError(t, personRepo.RefreshStats(personA.ID))
	require.NoError(t, personRepo.RefreshStats(personB.ID))

	// Drive concurrent clustering batches and a foreground merge through the
	// coordinator. protoCache is only touched by the worker; -race must be clean.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = svc.clusteringCoordinator.submitBackground()
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Foreground merge of B into A (no-op if B already deleted; ignore error).
		_, _ = svc.MergePeople(personA.ID, []uint{personB.ID})
	}()
	wg.Wait()

	// Final clustering pass to exercise protoCache reuse after the merge.
	_ = svc.clusteringCoordinator.submitBackground()
}
