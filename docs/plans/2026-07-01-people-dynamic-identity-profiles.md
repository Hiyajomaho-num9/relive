# Dynamic People Identity Profiles Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add versioned multi-center identity profiles that improve merge-suggestion recall and safely rescue legacy automatic-clustering misses without changing successful legacy assignments.

**Architecture:** Store derived, generation-versioned person centers and members in SQLite. A background service builds and incrementally refreshes profiles, maintains a center-level HNSW snapshot plus delta, and exposes exact profile scoring. Merge suggestions consume the new matcher first; automatic clustering uses it only as a guarded fallback after the legacy matcher fails.

**Tech Stack:** Go 1.26, Gin, GORM, SQLite, `github.com/coder/hnsw`, existing binary `float32` embedding encoding, testify, Vue 3 only for optional operational status display.

---

## Implementation Rules

- Use @superpowers:test-driven-development for every behavior change.
- Use @superpowers:verification-before-completion before each phase handoff.
- Keep `identity_profile_mode: legacy` as the default until real shadow data is calibrated.
- Do not change the legacy matcher's successful decisions or retry-decay behavior in this implementation. Profile scoring itself must not decay thresholds by retry count.
- Do not change the recognition model or recompute face embeddings.
- Every profile/index lookup must fail closed to legacy behavior.
- Commit after each task; do not mix tasks in one commit.

### Task 1: Add identity-profile configuration and validation

**Files:**
- Modify: `backend/pkg/config/config.go`
- Modify: `backend/pkg/config/config_test.go`
- Modify: `backend/config.dev.yaml`
- Modify: `backend/config.prod.yaml`
- Modify: `backend/config.dev.yaml.example`
- Modify: `backend/config.prod.yaml.example`

**Step 1: Write failing default and validation tests**

Add table-driven tests covering:

```go
func TestPeopleIdentityProfileDefaults(t *testing.T) {
    cfg := loadMinimalConfig(t, "people:\n  ml_endpoint: http://localhost:5050\n")
    assert.Equal(t, "legacy", cfg.People.IdentityProfileMode)
    assert.Equal(t, 6, cfg.People.IdentityProfileMaxCenters)
    assert.Equal(t, 3, cfg.People.IdentityProfileMinCenterFaces)
    assert.Equal(t, 2, cfg.People.IdentityProfileMinCenterPhotos)
    assert.Equal(t, 0.05, cfg.People.IdentityProfileMargin)
}

func TestPeopleIdentityProfileModeValidation(t *testing.T) {
    _, err := loadConfigText("people:\n  identity_profile_mode: unsafe\n")
    require.ErrorContains(t, err, "identity_profile_mode")
}
```

Also reject max centers outside `1..8`, non-positive evidence counts, margin outside `(0,1)`, and a rescue threshold outside `(0,1)`.

**Step 2: Run tests and confirm failure**

Run:

```bash
cd backend && go test ./pkg/config -run 'TestPeopleIdentityProfile' -v
```

Expected: FAIL because fields/defaults do not exist.

**Step 3: Add configuration fields and defaults**

Extend `PeopleConfig`:

```go
IdentityProfileMode             string  `yaml:"identity_profile_mode"`
IdentityProfileMaxCenters       int     `yaml:"identity_profile_max_centers"`
IdentityProfileMinCenterFaces   int     `yaml:"identity_profile_min_center_faces"`
IdentityProfileMinCenterPhotos  int     `yaml:"identity_profile_min_center_photos"`
IdentityProfileMargin           float64 `yaml:"identity_profile_margin"`
IdentityProfileRescueThreshold  float64 `yaml:"identity_profile_rescue_threshold"`
IdentityProfileBatchSize        int     `yaml:"identity_profile_batch_size"`
IdentityProfileCooldownMs       int     `yaml:"identity_profile_cooldown_ms"`
```

Defaults:

```go
identity_profile_mode: legacy
identity_profile_max_centers: 6
identity_profile_min_center_faces: 3
identity_profile_min_center_photos: 2
identity_profile_margin: 0.05
identity_profile_rescue_threshold: 0.65
identity_profile_batch_size: 25
identity_profile_cooldown_ms: 500
```

The threshold is an inactive bootstrap default because rescue mode remains off. Add a comment requiring calibration from shadow data before enabling rescue in production.

**Step 4: Run focused and package tests**

Run:

```bash
cd backend && go test ./pkg/config -v
```

Expected: PASS.

**Step 5: Commit**

```bash
git add backend/pkg/config/config.go backend/pkg/config/config_test.go backend/config*.yaml backend/config*.yaml.example
git commit -m "feat(people): add identity profile configuration"
```

### Task 2: Add versioned profile, center, member, feedback, and decision models

**Files:**
- Create: `backend/internal/model/person_identity_profile.go`
- Create: `backend/internal/model/people_feedback_event.go`
- Create: `backend/internal/model/people_identity_decision.go`
- Modify: `backend/pkg/database/database.go`
- Modify: `backend/pkg/database/database_test.go`
- Modify: `backend/internal/repository/test_helper.go`

**Step 1: Write failing migration tests**

Add tests asserting AutoMigrate creates:

```text
person_identity_profiles
person_identity_centers
person_identity_center_members
people_feedback_events
people_identity_decisions
```

Also assert indexes/constraints:

- unique `person_id` profile;
- unique `(person_id, generation, ordinal)` center;
- unique `(generation, face_id)` member within a person's generated profile;
- index `(status, updated_at)` for dirty profile scanning;
- index `(person_id, generation)` for center loads;
- index `(event_type, created_at)` for feedback calibration;
- index `(mode, created_at)` for decision telemetry cleanup.

**Step 2: Run migration test and confirm failure**

```bash
cd backend && go test ./pkg/database -run 'TestAutoMigrateAddsPersonIdentity' -v
```

Expected: FAIL because models/tables do not exist.

**Step 3: Implement models**

Define constants:

```go
const (
    IdentityProfileStatusDirty    = "dirty"
    IdentityProfileStatusBuilding = "building"
    IdentityProfileStatusReady    = "ready"
    IdentityProfileStatusFailed   = "failed"

    IdentityCenterMemberAccepted  = "accepted"
    IdentityCenterMemberCandidate = "candidate"
    IdentityCenterMemberExcluded  = "excluded"
)
```

Required model fields:

- profile: person ID, active generation, next generation, status, dirty reason, algorithm version, embedding model, face-count snapshot, last error, built/updated timestamps;
- center: person ID, generation, ordinal, centroid blob, sum blob, medoid face ID, support count, total weight, P10/P50, confirmed;
- member: person ID, generation, center ID nullable for candidate/excluded, face ID, photo ID, similarity, weight, state;
- feedback event: type, target person ID, source person IDs JSON, face IDs JSON, algorithm version, similarity snapshot JSON;
- identity decision: mode, component face IDs JSON, legacy target/score, profile best/second target and score, margin, center IDs JSON, decision, reason, elapsed milliseconds, algorithm/index generation.

Do not place face embeddings in telemetry or feedback rows.

**Step 4: Add models to migrations and repository test helper**

Append the five models to the main AutoMigrate model list and test helper migration list.

**Step 5: Run migration and model tests**

```bash
cd backend && go test ./pkg/database ./internal/model -v
```

Expected: PASS.

**Step 6: Commit**

```bash
git add backend/internal/model/person_identity_profile.go backend/internal/model/people_feedback_event.go backend/internal/model/people_identity_decision.go backend/pkg/database/database.go backend/pkg/database/database_test.go backend/internal/repository/test_helper.go
git commit -m "feat(people): add identity profile persistence models"
```

### Task 3: Implement atomic profile repository generations

**Files:**
- Create: `backend/internal/repository/person_identity_profile_repo.go`
- Create: `backend/internal/repository/person_identity_profile_repo_test.go`
- Modify: `backend/internal/repository/repository.go`
- Modify: `backend/internal/repository/face_repo.go`
- Modify: `backend/internal/repository/face_repo_test.go`

**Step 1: Write failing repository tests**

Cover:

1. `MarkDirty(personIDs, reason)` upserts profiles without changing an active generation.
2. `ListDirty(cursor, limit)` is deterministic by person ID.
3. `ReplaceGeneration(personID, build)` writes a new generation and atomically activates it.
4. A forced member insert failure leaves the old generation active.
5. `GetActive(personID)` loads centers and members from only the active generation.
6. `InvalidateDeletedPeople` removes derived rows whose person no longer exists.
7. `ListProfileFaces(personID)` selects only required lightweight fields plus embedding.

Representative assertion:

```go
old := seedActiveGeneration(t, repo, person.ID, 1)
repo.setBeforeActivateHookForTest(func() error { return errors.New("forced") })
require.Error(t, repo.ReplaceGeneration(person.ID, newBuild))
got, err := repo.GetActive(person.ID)
require.NoError(t, err)
assert.Equal(t, old.Generation, got.Profile.ActiveGeneration)
```

**Step 2: Run tests and confirm failure**

```bash
cd backend && go test ./internal/repository -run 'TestPersonIdentityProfile|TestFaceRepository_ListProfileFaces' -v
```

Expected: FAIL because repository methods do not exist.

**Step 3: Define repository interface**

```go
type PersonIdentityProfileRepository interface {
    MarkDirty(personIDs []uint, reason string) error
    ListDirty(cursor uint, limit int) ([]*model.PersonIdentityProfile, error)
    GetActive(personID uint) (*model.PersonIdentityProfileBuild, error)
    ListAllActiveCenters() ([]*model.PersonIdentityCenter, error)
    ReplaceGeneration(personID uint, build *model.PersonIdentityProfileBuild) error
    MarkFailed(personID uint, message string) error
    DeleteByPersonIDs(personIDs []uint) error
    DeleteInactiveGenerations(personID uint, keep int) error
}
```

Add `IdentityProfile` to `Repositories` and construct it in `NewRepositories`.

**Step 4: Add lightweight face query**

Add `ListProfileFaces(personID uint)` selecting:

```text
id, photo_id, person_id, confidence, quality_score, embedding,
cluster_status, cluster_score, manual_locked, manual_lock_reason
```

Order by manual lock, cluster confidence, quality, and ID for deterministic builds.

**Step 5: Implement atomic generation replacement**

Use one SQLite transaction to insert generation rows and update active generation. Validate person existence inside the transaction. Keep only derived state in these tables.

**Step 6: Run repository tests**

```bash
cd backend && go test ./internal/repository -v
```

Expected: PASS.

**Step 7: Commit**

```bash
git add backend/internal/repository/person_identity_profile_repo.go backend/internal/repository/person_identity_profile_repo_test.go backend/internal/repository/repository.go backend/internal/repository/face_repo.go backend/internal/repository/face_repo_test.go
git commit -m "feat(people): add atomic identity profile repository"
```

### Task 4: Implement the pure multi-center profile builder

**Files:**
- Create: `backend/internal/service/person_identity_profile_builder.go`
- Create: `backend/internal/service/person_identity_profile_builder_test.go`

**Step 1: Write deterministic failing vector tests**

Use small normalized synthetic vectors; do not depend on the database. Cover:

- normalization and weighted sum;
- manual/high-confidence evidence ordering;
- medoid selection;
- one coherent mode creates one center;
- two coherent modes create two centers;
- a lone outlier becomes `candidate`, not a center;
- three coherent faces from two photos create a new center;
- three faces from one photo do not create a new automatic center;
- one manual face may create a confirmed retrieval-only center;
- close centers merge only when combined distribution stays compact;
- center count never exceeds configured maximum;
- build output is deterministic regardless of input order;
- invalid/mismatched embeddings are excluded.

Example fixture:

```go
faces := []profileFace{
    pf(1, 101, []float32{1, 0, 0}, 0.9, true),
    pf(2, 102, []float32{.99, .02, 0}, 0.9, false),
    pf(3, 103, []float32{0, 1, 0}, 0.8, false),
}
build := builder.Build(7, faces)
require.Len(t, build.Centers, 1)
assert.Equal(t, IdentityCenterMemberCandidate, build.MemberByFaceID(3).State)
```

**Step 2: Run tests and confirm failure**

```bash
cd backend && go test ./internal/service -run 'TestIdentityProfileBuilder' -v
```

Expected: FAIL because builder does not exist.

**Step 3: Implement pure builder helpers**

Implement:

```go
type identityProfileBuilder struct { cfg identityProfileBuilderConfig }
func (b *identityProfileBuilder) Build(personID uint, faces []*model.Face) (*model.PersonIdentityProfileBuild, error)
func weightedCentroid(members []profileMember) ([]float32, []float32, float64)
func centerMedoid(centroid []float32, members []profileMember) uint
func percentileSimilarity(values []float64, p float64) float64
```

Reuse `model.DecodeEmbedding`, `model.EncodeEmbedding`, and existing cosine helpers. Extract generally useful vector helpers into a small shared file only if duplication is real.

**Step 4: Implement eligibility and weights**

Keep weight functions bounded and named. Manual membership must outrank automatic membership, but no single face may dominate a mature center. Do not update production config thresholds here; builder config is injected for tests.

**Step 5: Implement bounded spherical reassignment**

Maximum five iterations. Sort every map-derived ID slice before processing. Reject NaN and zero-norm vectors. Produce accepted/candidate/excluded memberships for audit.

**Step 6: Run tests and race check**

```bash
cd backend && go test ./internal/service -run 'TestIdentityProfileBuilder' -race -v
```

Expected: PASS.

**Step 7: Commit**

```bash
git add backend/internal/service/person_identity_profile_builder.go backend/internal/service/person_identity_profile_builder_test.go
git commit -m "feat(people): build bounded multi-center identity profiles"
```

### Task 5: Add background profile build and backfill service

**Files:**
- Create: `backend/internal/service/person_identity_profile_service.go`
- Create: `backend/internal/service/person_identity_profile_service_test.go`
- Modify: `backend/internal/service/service.go`
- Modify: `backend/internal/service/scheduler.go`
- Modify: `backend/internal/service/scheduler_test.go`

**Step 1: Write failing service tests**

Cover:

- a dirty person is built and atomically activated;
- build failure marks failed but preserves active generation;
- cursor backfill resumes after restart;
- deleted people are cleaned without error;
- `legacy` mode permits background profile builds but never exposes decisions;
- batch size and cooldown are honored;
- scheduler performs at most one bounded slice per tick.

**Step 2: Run focused tests and confirm failure**

```bash
cd backend && go test ./internal/service -run 'TestPersonIdentityProfileService|TestTaskSchedulerIdentityProfile' -v
```

Expected: FAIL.

**Step 3: Define service interface**

```go
type PersonIdentityProfileService interface {
    MarkDirty(personIDs []uint, reason string) error
    RunBackgroundSlice() error
    GetActive(personID uint) (*model.PersonIdentityProfileBuild, error)
    GetStats() (*model.PersonIdentityProfileStats, error)
    Mode() string
}
```

Persist backfill cursor/state in `app_config`, following the merge-suggestion state pattern. Backfill should mark only existing people with faces as dirty in bounded cursor pages.

**Step 4: Wire repository, builder, and scheduler**

Construct the service in `NewServices`. Give background work the existing dedicated background DB pattern. Add one scheduler slice invocation; do not launch an uncontrolled goroutine per dirty person.

**Step 5: Add bounded cleanup**

After a successful activation, retain the active and previous generation. Delete older generations in bounded writes through `WriteQueue`.

**Step 6: Run focused and complete service tests**

```bash
cd backend && go test ./internal/service -run 'IdentityProfile' -v
cd backend && go test ./internal/service -v
```

Expected: PASS.

**Step 7: Commit**

```bash
git add backend/internal/service/person_identity_profile_service.go backend/internal/service/person_identity_profile_service_test.go backend/internal/service/service.go backend/internal/service/scheduler.go backend/internal/service/scheduler_test.go
git commit -m "feat(people): build identity profiles in background slices"
```

### Task 6: Record durable people feedback events

**Files:**
- Create: `backend/internal/repository/people_feedback_event_repo.go`
- Create: `backend/internal/repository/people_feedback_event_repo_test.go`
- Modify: `backend/internal/repository/repository.go`
- Modify: `backend/internal/service/people_service.go`
- Modify: `backend/internal/service/person_merge_suggestion_service.go`
- Modify: `backend/internal/service/people_service_test.go`
- Modify: `backend/internal/service/person_merge_suggestion_service_test.go`

**Step 1: Write failing repository and service tests**

Assert successful operations emit exactly one event:

- manual merge -> `merge_confirmed`;
- excluded suggestion -> `merge_rejected` plus existing cannot-link;
- move faces -> `face_moved`;
- split -> `person_split`;
- dissolve -> `person_dissolved`.

Assert failed operations do not emit events. Assert JSON ID lists are sorted and deduplicated.

**Step 2: Run tests and confirm failure**

```bash
cd backend && go test ./internal/repository ./internal/service -run 'FeedbackEvent|EmitsFeedback' -v
```

Expected: FAIL.

**Step 3: Implement repository**

```go
type PeopleFeedbackEventRepository interface {
    Create(event *model.PeopleFeedbackEvent) error
    ListForCalibration(afterID uint, limit int) ([]*model.PeopleFeedbackEvent, error)
}
```

Add it to `Repositories`.

**Step 4: Emit events only after successful mutations**

Prefer the same transaction when the repository operation already owns the transaction. Otherwise emit after the mutation succeeds and log event-write failure without rolling back the completed user action. Never log embeddings, API keys, or image paths.

**Step 5: Run tests**

```bash
cd backend && go test ./internal/repository ./internal/service -run 'Feedback|MergePeople|SplitPerson|MoveFaces|ExcludeCandidates' -v
```

Expected: PASS.

**Step 6: Commit**

```bash
git add backend/internal/repository/people_feedback_event_repo.go backend/internal/repository/people_feedback_event_repo_test.go backend/internal/repository/repository.go backend/internal/service/people_service.go backend/internal/service/person_merge_suggestion_service.go backend/internal/service/people_service_test.go backend/internal/service/person_merge_suggestion_service_test.go
git commit -m "feat(people): record identity feedback events"
```

### Task 7: Build center ANN snapshot and delta index

**Files:**
- Create: `backend/internal/service/person_identity_profile_ann.go`
- Create: `backend/internal/service/person_identity_profile_ann_test.go`
- Modify: `backend/internal/service/person_identity_profile_service.go`

**Step 1: Write failing ANN tests**

Cover:

- snapshot returns owning person IDs for nearest centers;
- multiple centers from one person deduplicate to one candidate;
- delta additions are queryable before snapshot rebuild;
- invalidated center IDs are filtered;
- stale profile generations are filtered;
- deleted people are filtered;
- snapshot swap is atomic during concurrent queries;
- missing/failed index returns `ready=false`, never a partial candidate set;
- model-signature mismatch rejects the snapshot.

**Step 2: Run tests and confirm failure**

```bash
cd backend && go test ./internal/service -run 'TestIdentityProfileANN' -race -v
```

Expected: FAIL.

**Step 3: Implement index types**

```go
type identityCenterIndex struct {
    graph       *hnsw.Graph[uint]
    centerOwner map[uint]uint
    generation  map[uint]uint
    model       string
}

type identityProfileANN struct {
    snapshot atomic.Pointer[identityCenterIndex]
    deltaMu  sync.RWMutex
    delta    map[uint]profileCenterVector
    invalid  map[uint]struct{}
}
```

Serialize HNSW searches if the library requires it. Keep delta bounded; trigger a rebuild request above a configured internal limit.

**Step 4: Implement atomic rebuild**

Build outside locks, validate node ownership/model/generation, then atomically swap. Retain old snapshot until all current readers release it through Go reachability.

**Step 5: Connect generation activation to delta update**

After repository activation, invalidate prior centers and add active centers to delta. A failed delta update must not invalidate the profile; mark ANN dirty and allow exact fallback.

**Step 6: Run tests and benchmark smoke test**

```bash
cd backend && go test ./internal/service -run 'IdentityProfileANN' -race -v
cd backend && go test ./internal/service -run '^$' -bench 'BenchmarkIdentityProfileANN' -benchtime=1x
```

Expected: PASS and benchmark completes.

**Step 7: Commit**

```bash
git add backend/internal/service/person_identity_profile_ann.go backend/internal/service/person_identity_profile_ann_test.go backend/internal/service/person_identity_profile_service.go
git commit -m "feat(people): index identity centers with snapshot and delta"
```

### Task 8: Implement robust profile scoring and negative evidence

**Files:**
- Create: `backend/internal/service/person_identity_profile_matcher.go`
- Create: `backend/internal/service/person_identity_profile_matcher_test.go`
- Modify: `backend/internal/repository/face_repo.go`
- Modify: `backend/internal/repository/face_repo_test.go`

**Step 1: Write failing matcher tests**

Cover:

- single query uses best stable center;
- 2-4 faces use quality-weighted median;
- larger components use trimmed weighted mean;
- best/second margin blocks ambiguity;
- score below global rescue threshold blocks attachment;
- fit below center P10 boundary blocks attachment;
- retrieval-only manual singleton center can suggest but cannot auto-rescue;
- cannot-link blocks a candidate;
- same-photo co-occurrence blocks auto-rescue and returns a warning for suggestions;
- retry count does not change profile score or threshold;
- no active profile/index returns unavailable, not a match;
- candidate exact scoring is deterministic.

**Step 2: Run tests and confirm failure**

```bash
cd backend && go test ./internal/service -run 'TestIdentityProfileMatcher' -v
```

Expected: FAIL.

**Step 3: Add co-occurrence repository query**

Add a batched method that checks whether a candidate person has a face in any photo represented by the query component. It must use indexed `faces.photo_id` and `faces.person_id` fields and avoid one query per candidate.

**Step 4: Implement matcher result**

```go
type IdentityProfileMatch struct {
    Available      bool
    PersonID       uint
    Score          float64
    SecondPersonID uint
    SecondScore    float64
    Margin         float64
    CenterIDs      []uint
    AutoEligible   bool
    BlockReason    string
}
```

Keep retrieval and exact scoring separate. Use ANN only to shortlist; always load/validate active centers before the final decision.

**Step 5: Run focused and repository tests**

```bash
cd backend && go test ./internal/service ./internal/repository -run 'IdentityProfileMatcher|Cooccur' -v
```

Expected: PASS.

**Step 6: Commit**

```bash
git add backend/internal/service/person_identity_profile_matcher.go backend/internal/service/person_identity_profile_matcher_test.go backend/internal/repository/face_repo.go backend/internal/repository/face_repo_test.go
git commit -m "feat(people): score identity profiles with conservative guards"
```

### Task 9: Add shadow decision telemetry and bounded cleanup

**Files:**
- Create: `backend/internal/repository/people_identity_decision_repo.go`
- Create: `backend/internal/repository/people_identity_decision_repo_test.go`
- Create: `backend/internal/service/person_identity_profile_telemetry.go`
- Create: `backend/internal/service/person_identity_profile_telemetry_test.go`
- Modify: `backend/internal/repository/repository.go`
- Modify: `backend/internal/service/scheduler.go`

**Step 1: Write failing telemetry tests**

Cover:

- all legacy misses are recorded in shadow/rescue mode;
- successful legacy matches are sampled deterministically;
- disagreements are always recorded;
- face IDs are sorted/deduplicated;
- embeddings and image paths are never serialized;
- retention cleanup deletes old rows in bounded batches;
- telemetry-write failure does not change assignment behavior.

**Step 2: Run tests and confirm failure**

```bash
cd backend && go test ./internal/repository ./internal/service -run 'IdentityDecision|IdentityTelemetry' -v
```

Expected: FAIL.

**Step 3: Implement repository and recorder**

Add `IdentityDecision` to `Repositories`. Recorder accepts legacy and profile results and produces one sanitized row. Use deterministic sampling by component hash so repeated retries do not flood the table.

**Step 4: Add scheduler cleanup**

Delete old decisions with a configurable/internal retention default in batches. Do not perform an unbounded `DELETE` on startup.

**Step 5: Run tests**

```bash
cd backend && go test ./internal/repository ./internal/service -run 'IdentityDecision|IdentityTelemetry|Scheduler' -v
```

Expected: PASS.

**Step 6: Commit**

```bash
git add backend/internal/repository/people_identity_decision_repo.go backend/internal/repository/people_identity_decision_repo_test.go backend/internal/repository/repository.go backend/internal/service/person_identity_profile_telemetry.go backend/internal/service/person_identity_profile_telemetry_test.go backend/internal/service/scheduler.go
git commit -m "feat(people): record identity profile shadow decisions"
```

### Task 10: Use profiles for merge-suggestion retrieval and exact scoring

**Files:**
- Modify: `backend/internal/service/person_merge_suggestion_service.go`
- Modify: `backend/internal/service/person_merge_suggestion_service_test.go`
- Modify: `backend/internal/service/service.go`

**Step 1: Write failing merge-suggestion tests**

Cover:

- a fragment missed by legacy five-prototype scoring is retrieved through a matching secondary center;
- candidate still requires exact supporting-face validation;
- cannot-link still suppresses the suggestion;
- same-photo co-occurrence is surfaced as a warning/block according to review policy;
- profile unavailable falls back to current prototype ANN and scoring;
- each candidate belongs to only its best target suggestion;
- applying/excluding suggestions preserves current semantics.

**Step 2: Run tests and confirm failure**

```bash
cd backend && go test ./internal/service -run 'TestPersonMergeSuggestionService_.*IdentityProfile' -v
```

Expected: FAIL.

**Step 3: Inject profile matcher hooks**

Add explicit setters/interfaces rather than importing concrete service state:

```go
type PersonProfileSimilarityProvider interface {
    SimilarPeople(personID uint, k int) ([]IdentityProfileMatch, bool)
    ComparePeople(a, b uint) (IdentityProfileMatch, bool)
}
```

Use profile candidates when ready. Preserve the current ANN/scorer as fallback.

**Step 4: Keep manual review boundary**

Do not auto-apply any center-derived suggestion. Persist score, rank, and optional warning metadata only.

**Step 5: Run merge-suggestion suite**

```bash
cd backend && go test ./internal/service -run 'PersonMergeSuggestion' -race -v
```

Expected: PASS.

**Step 6: Commit**

```bash
git add backend/internal/service/person_merge_suggestion_service.go backend/internal/service/person_merge_suggestion_service_test.go backend/internal/service/service.go
git commit -m "feat(people): use identity centers for merge suggestions"
```

### Task 11: Add shadow mode to incremental clustering

**Files:**
- Modify: `backend/internal/service/people_service.go`
- Modify: `backend/internal/service/people_service_test.go`
- Modify: `backend/internal/service/service.go`

**Step 1: Write failing shadow-mode tests**

Cover:

- `legacy` mode never calls the profile matcher;
- `shadow` calls it after each legacy decision but preserves all face/person updates;
- profile matcher failure preserves legacy behavior;
- old/new disagreement is recorded;
- a legacy miss plus profile hit is recorded but not applied;
- retry count does not affect profile score inputs;
- no extra write occurs when telemetry repository fails.

**Step 2: Run tests and confirm failure**

```bash
cd backend && go test ./internal/service -run 'TestPeopleService_IdentityProfileShadow' -v
```

Expected: FAIL.

**Step 3: Add injected matcher/telemetry hooks**

Keep `peopleService` independent of the concrete profile service:

```go
type identityProfileMatchFn func(component []faceWithEmbedding, mode string) IdentityProfileMatch
type identityDecisionRecordFn func(legacy legacyMatchResult, profile IdentityProfileMatch)
```

Invoke shadow scoring outside the SQLite write transaction and never block clustering completion on telemetry.

**Step 4: Run clustering equivalence tests**

```bash
cd backend && go test ./internal/service -run 'IdentityProfileShadow|ClusteringPipelineEquivalence|PeopleService_Profile' -race -v
```

Expected: PASS with byte-for-byte equivalent assignments in shadow versus legacy.

**Step 5: Commit**

```bash
git add backend/internal/service/people_service.go backend/internal/service/people_service_test.go backend/internal/service/service.go
git commit -m "feat(people): shadow identity profile clustering decisions"
```

### Task 12: Add conservative rescue mode

**Files:**
- Modify: `backend/internal/service/people_service.go`
- Modify: `backend/internal/service/people_service_test.go`
- Modify: `backend/internal/service/people_service_equivalence_test.go`

**Step 1: Write failing rescue-mode tests**

Cover:

- successful legacy attach remains unchanged even when profile prefers another person;
- legacy miss is rescued only when `AutoEligible` is true;
- low score, low margin, unstable center, cannot-link, co-occurrence, or unavailable profile preserves legacy behavior;
- rescue attaches the entire coherent component in one write;
- target and affected photo/person state updates match a normal legacy attach;
- rescue does not create a new center synchronously;
- profile is marked dirty after rescue;
- shadow and legacy remain behavior-equivalent;
- rescue mode never invokes retry-based threshold decay for its own decision.

**Step 2: Run tests and confirm failure**

```bash
cd backend && go test ./internal/service -run 'TestPeopleService_IdentityProfileRescue' -v
```

Expected: FAIL.

**Step 3: Implement rescue at the legacy miss boundary**

Call profile rescue only after `attachComponentToExistingPersonWithEmbeddings` returns false and before create-person/pending handling. Preserve existing component score and legacy fallback when rescue declines.

Do not allow profile rescue to override a successful legacy target.

**Step 4: Mark target profile dirty asynchronously**

After a successful rescue, mark only the target profile dirty. Do not rebuild centers in the clustering transaction.

**Step 5: Run focused, equivalence, and race tests**

```bash
cd backend && go test ./internal/service -run 'IdentityProfileRescue|ClusteringPipelineEquivalence|PeopleClusteringCoordinator' -race -v
```

Expected: PASS.

**Step 6: Commit**

```bash
git add backend/internal/service/people_service.go backend/internal/service/people_service_test.go backend/internal/service/people_service_equivalence_test.go
git commit -m "feat(people): rescue conservative legacy clustering misses"
```

### Task 13: Invalidate profiles on every people mutation

**Files:**
- Modify: `backend/internal/service/people_service.go`
- Modify: `backend/internal/service/people_service_test.go`
- Modify: `backend/internal/service/person_merge_suggestion_service.go`
- Modify: `backend/internal/service/person_merge_suggestion_service_test.go`

**Step 1: Write failing invalidation tests**

Assert exact affected IDs and reasons for:

- detection result replaces faces;
- merge marks target dirty and deletes source profiles;
- split marks old and new people dirty;
- move marks source and target dirty;
- dissolve deletes the profile;
- avatar/name/category/hidden-only changes do not rebuild identity centers;
- failed mutations do not dirty profiles;
- merge-suggestion apply uses the same invalidation path as manual merge.

**Step 2: Run tests and confirm failure**

```bash
cd backend && go test ./internal/service -run 'TestPeopleService_IdentityProfileInvalidation' -v
```

Expected: FAIL.

**Step 3: Add one profile invalidation hook**

Follow the existing merge-suggestion dirty-hook pattern. Centralize ID deduplication and reason naming. Avoid calling repository methods at many ad hoc call sites.

**Step 4: Run people service suites**

```bash
cd backend && go test ./internal/service -run 'PeopleService|PersonMergeSuggestion' -race -v
```

Expected: PASS.

**Step 5: Commit**

```bash
git add backend/internal/service/people_service.go backend/internal/service/people_service_test.go backend/internal/service/person_merge_suggestion_service.go backend/internal/service/person_merge_suggestion_service_test.go
git commit -m "feat(people): invalidate identity profiles after mutations"
```

### Task 14: Add operational stats API and documentation

**Files:**
- Modify: `backend/internal/model/dto.go`
- Modify: `backend/internal/api/v1/handler/people_handler.go`
- Modify: `backend/internal/api/v1/handler/people_handler_test.go`
- Modify: `backend/internal/api/v1/router/router.go`
- Modify: `docs/QUICK_REFERENCE.md`
- Modify: `README.md`

**Step 1: Write failing handler tests**

Add authenticated endpoints:

```text
GET /api/v1/people/identity-profiles/stats
GET /api/v1/people/identity-profiles/decisions?limit=50
```

Stats return mode, profiles ready/dirty/failed, centers, accepted/candidate/excluded members, average/max centers, ANN readiness/generation/nodes/delta, last build error, and shadow disagreement/rescue counts. Decision output must omit embeddings and paths.

**Step 2: Run tests and confirm failure**

```bash
cd backend && go test ./internal/api/v1/handler -run 'IdentityProfile' -v
```

Expected: FAIL.

**Step 3: Implement DTOs, handler, and routes**

Keep endpoints read-only. Clamp decision limit to `1..200`. Do not add mutating controls in this task; operational mode changes remain configuration-driven.

**Step 4: Document modes and safe rollout**

Document that production remains `legacy` until shadow calibration, then merge suggestions, then rescue. Include exact rollback configuration.

**Step 5: Run handler and docs checks**

```bash
cd backend && go test ./internal/api/v1/handler ./internal/api/v1/router -v
git diff --check
```

Expected: PASS.

**Step 6: Commit**

```bash
git add backend/internal/model/dto.go backend/internal/api/v1/handler/people_handler.go backend/internal/api/v1/handler/people_handler_test.go backend/internal/api/v1/router/router.go docs/QUICK_REFERENCE.md README.md
git commit -m "docs(people): expose identity profile rollout status"
```

### Task 15: Add calibration report and representative-scale benchmarks

**Files:**
- Create: `backend/cmd/relive-identity-profile-report/main.go`
- Create: `backend/internal/service/person_identity_profile_benchmark_test.go`
- Create: `docs/PEOPLE_IDENTITY_PROFILE_ROLLOUT.md`

**Step 1: Write report logic tests around pure aggregation helpers**

The report reads feedback and decision rows and outputs:

- confirmed-merge recall at K;
- suggestion acceptance/rejection counts;
- legacy-miss/profile-hit count;
- legacy/profile disagreement count;
- score and margin quantiles for positive/negative feedback;
- rescue outcomes when available;
- query/build timing quantiles.

No raw embedding, image path, name, or thumbnail leaves the database.

**Step 2: Run tests and confirm failure**

```bash
cd backend && go test ./cmd/relive-identity-profile-report/... ./internal/service -run 'IdentityProfileReport' -v
```

Expected: FAIL.

**Step 3: Implement read-only report command**

Require an explicit DB path and default to text/JSON output on stdout. Open SQLite read-only. Return a clear insufficient-label warning instead of proposing thresholds from inadequate data.

**Step 4: Add benchmarks**

Benchmark:

- building profiles with 10, 100, 1,000, and 7,000 faces;
- ANN snapshot build at representative node counts;
- query plus exact score with 20/50/200 candidates;
- delta query with bounded sizes.

Benchmarks must generate synthetic normalized vectors and stay out of normal test timing.

**Step 5: Document operational calibration gate**

State that rescue mode must not be enabled until:

- positive/negative label counts are reported;
- merge recall and disagreement rates are reviewed;
- false-attachment guardrail is chosen;
- NAS resource measurements are acceptable;
- rollback is rehearsed.

**Step 6: Run report tests and benchmark smoke test**

```bash
cd backend && go test ./cmd/relive-identity-profile-report/... ./internal/service -run 'IdentityProfileReport' -v
cd backend && go test ./internal/service -run '^$' -bench 'BenchmarkIdentityProfile' -benchtime=1x
```

Expected: PASS.

**Step 7: Commit**

```bash
git add backend/cmd/relive-identity-profile-report backend/internal/service/person_identity_profile_benchmark_test.go docs/PEOPLE_IDENTITY_PROFILE_ROLLOUT.md
git commit -m "feat(people): add identity profile calibration report"
```

### Task 16: Full verification and rollout handoff

**Files:**
- Modify only if verification reveals issues.

**Step 1: Format code**

```bash
cd backend && find internal/model internal/repository internal/service internal/api/v1 cmd/relive-identity-profile-report pkg/config pkg/database -name '*.go' -print0 | xargs -0 gofmt -w
```

Expected: no formatting errors.

**Step 2: Run focused race tests**

```bash
cd backend && go test -race ./internal/service ./internal/repository ./pkg/database
```

Expected: PASS.

**Step 3: Run the complete backend suite**

```bash
make test
```

Expected: PASS.

**Step 4: Build backend and frontend**

```bash
cd backend && go build ./cmd/relive ./cmd/relive-identity-profile-report
cd ../frontend && npm run build
```

Expected: PASS.

**Step 5: Verify migration on a copied database**

Never test migration against the live NAS database. Copy a representative database, start the backend once with `auto_migrate: true`, and verify:

```sql
PRAGMA integrity_check;
SELECT COUNT(*) FROM person_identity_profiles;
SELECT COUNT(*) FROM person_identity_centers;
```

Expected: integrity check `ok`; existing face/person counts unchanged.

**Step 6: Run shadow-mode smoke test**

On the copied database:

- set mode to `shadow`;
- let one bounded backfill/profile slice run;
- process or replay a small set of pending components;
- verify no historical `person_id` changes;
- verify identity decision rows contain no embedding/path data;
- switch back to `legacy` and confirm normal operation.

**Step 7: Review diff and commits**

```bash
git status --short
git diff main...HEAD --stat
git log --oneline main..HEAD
```

Expected: clean status and one intentional commit per task.

**Step 8: Final commit only if verification required fixes**

```bash
git add <verified-fix-files>
git commit -m "fix(people): address identity profile verification findings"
```

Do not squash until review; task-level commits are useful for staged rollout and rollback.
