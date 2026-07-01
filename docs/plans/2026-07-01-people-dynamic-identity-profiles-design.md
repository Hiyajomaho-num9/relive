# Dynamic People Identity Profiles Design

**Date:** 2026-07-01  
**Status:** Approved

## Goal

Reduce the number of duplicate `Person` records created for the same real person while preserving the current system's low false-merge rate and reducing repeated prototype and ANN rebuild work.

## Problem Statement

Relive's current automatic assignment is conservative and generally reliable, but its recall stops improving as a person accumulates more faces:

- `ListPrototypeEmbeddings` only loads the top 10 faces per person by manual lock, quality, confidence, and ID.
- `selectDiversePrototypes` reduces those candidates to five real-face prototypes.
- A component is scored by averaging each query face's best match against those five prototypes.
- Faces outside the top-quality candidate set cannot expand the person's identity coverage, even when they represent stable variations such as side profile, glasses, lighting, or age.
- Retry-based threshold decay eventually relaxes link and attach thresholds. That reduces pending work by weakening the decision boundary rather than by adding evidence.
- Once a missed component becomes a separate `Person`, normal incremental clustering no longer revisits the identity decision. Recovery depends on person-level merge suggestions using the same bounded prototype representation.

The desired behavior is therefore higher same-person recall without making automatic assignment broadly more aggressive.

## Design Principles

1. A person should become easier to recognize as reliable evidence accumulates.
2. More evidence should expand identity coverage, not lower the global safety threshold.
3. Belonging to a person and being trusted to represent that person are separate facts.
4. A single outlier must not create or move an identity center.
5. Existing successful legacy assignments remain authoritative during migration.
6. The new matcher first rescues legacy misses and improves merge suggestions; it does not initially replace the legacy matcher.
7. All profile and index state is derived, versioned, and safely disposable.

## Alternatives Considered

### Increase the fixed prototype count

Load 30-50 candidate faces and retain 10 or more prototypes.

This is inexpensive to implement, but computation grows linearly and outlier handling remains weak. It improves coverage only by expanding the same fixed representation.

### Replace the face recognition model

Upgrade from `buffalo_sc`, or introduce a trained score calibrator.

This may eventually provide additional recall, but it requires recomputing every embedding and complicates attribution of regressions. It is deliberately out of scope for the first implementation.

### Dynamic multi-center identity profiles

Maintain a bounded set of stable embedding modes for each person and update only from trusted evidence. Use centers for ANN retrieval and exact profile scoring, while retaining real medoid faces for auditability.

This is the selected approach.

## Identity Profile Model

Each person owns an active profile generation containing one to eight centers. Most people should need one to three centers; larger, diverse identities may use up to eight.

Each center stores:

- a normalized, quality-weighted centroid for ANN and exact similarity;
- the normalized weighted vector sum and total weight for stable incremental updates;
- a medoid face ID, the real face nearest the centroid;
- accepted support count;
- lower and median within-center similarity statistics;
- whether the center contains manual confirmation;
- the embedding model signature and algorithm version;
- a generation number and timestamps.

Center members have one of three states:

- `accepted`: contributes to centroid and distribution statistics;
- `candidate`: belongs to the person but needs corroboration before representing it;
- `excluded`: remains assigned to the person but cannot influence the profile.

## Persistence and Atomic Generations

Add three derived-data models:

- `person_identity_profiles`: one row per person, active generation, build status, algorithm/model version, and face-count snapshot;
- `person_identity_centers`: center vector and statistics for a particular generation;
- `person_identity_center_members`: face membership, weight, similarity, and state.

Rebuilding a person writes a new generation first. Validation completes before a transaction switches `active_generation`. Failed or interrupted builds leave the previous generation active. Old generations can be removed later by bounded cleanup.

The embedding model signature must be stored with every profile. Embeddings produced by different recognition models must never share a profile or ANN index.

## Center Construction

### Eligible evidence

Only faces with valid embeddings and a current assignment are considered. Manual faces rank highest. High-confidence automatic assignments are accepted. Borderline or low-quality assignments begin as candidates or are excluded from representation.

### Initialization

The first center is seeded from a high-quality manual face when available. Otherwise, it uses the reliable face with the highest average similarity to the other reliable faces, avoiding an arbitrary earliest or maximum-quality seed.

### Assignment and center creation

Faces are processed in reliability and quality order:

1. Assign to the nearest center when similarity satisfies both a global safety floor and the center's observed boundary.
2. Otherwise place the face in an uncovered candidate pool.
3. Create a new automatic center only when at least three mutually coherent faces from at least two photos support the new mode.
4. A manual face may seed a confirmed center alone, but a single-face confirmed center is initially usable for retrieval and suggestions only, not for widening automatic attachment.

Run a bounded three-to-five iteration spherical reassignment pass, recomputing centroids, medoids, and distribution statistics. Merge centers only when the combined member distribution remains compact. Never prune a strong center solely because it is old; old-age and rare-appearance modes are valuable.

## Online Profile Growth

An automatic assignment does not automatically become profile evidence.

- Manual corrections and high-confidence assignments may update an existing center.
- Borderline assignments remain candidate members until corroborated.
- A new automatic center requires multiple coherent faces from multiple photos.
- Per-face update weight is capped and combines source trust, face quality, and assignment confidence.
- Mature centers move less because their existing total weight dominates any single sample.
- Periodic person-local rebuilds correct numerical drift without rescanning unrelated people.

This prevents the feedback loop where one bad assignment moves a center and attracts further bad assignments.

## Candidate Retrieval and Exact Scoring

The ANN index stores center nodes mapped to person IDs. A pending component searches all of its valid embeddings and unions the returned person candidates.

Exact scoring uses:

- each query face's best center similarity;
- a quality-weighted median for small components or trimmed weighted mean for larger components;
- fit against the target center's observed similarity distribution;
- the margin between the best and second-best person;
- support count and center confirmation state;
- hard negative evidence such as `cannot_link`;
- same-photo co-occurrence as an automatic-attachment blocker, while allowing a warning-only exception in manual review for collages, reflections, and screen captures.

Automatic attachment requires absolute score, margin, profile stability, and negative-evidence checks. Retry count does not lower the automatic identity threshold.

## Legacy Rescue Mode

The initial production mode preserves every successful legacy decision:

```text
legacy matcher
  successful -> keep legacy result
  no match   -> dynamic-profile rescue
                 confident -> attach existing person
                 uncertain -> keep legacy pending/create behavior
```

If the legacy matcher and profile matcher disagree on a successful legacy target, the legacy target wins during rescue mode and the disagreement is logged for analysis.

This directly addresses fragmentation while minimizing the regression surface.

## Existing Fragment Recovery

Merge suggestion candidate retrieval switches from fixed face prototypes to identity centers before automatic attachment changes.

Any strong center-to-center match can retrieve a person pair. Exact validation then loads real supporting faces and checks:

- multiple cross-person face matches;
- support across distinct photos;
- profile distribution compatibility after a hypothetical merge;
- `cannot_link` constraints;
- same-photo co-occurrence warnings or blockers.

All merges remain manually confirmed in the first version. Confirmed merges rebuild only the target profile and invalidate source profiles.

## ANN Lifecycle

Maintain the existing face-prototype ANN during migration and add an identity-center ANN.

The center ANN consists of:

- a read-only main snapshot built from active center generations;
- a small delta containing recently changed centers and invalidated center IDs.

Queries merge results from both. A low-priority background rebuild creates a complete replacement snapshot and atomically swaps it after validation. Stale candidates are filtered by active generation and person existence before exact scoring.

If the profile index is unavailable, rescue mode falls back to legacy behavior.

## Feedback and Calibration

Add `people_feedback_events` to preserve the decision context for:

- confirmed and rejected merges;
- face moves and person splits;
- new `cannot_link` constraints;
- corrected automatic assignments.

Positive evaluation examples come from confirmed merges, manual moves into a person, and trusted faces within one person. Negative examples come from rejected suggestions, `cannot_link`, splits, corrected assignments, and cautiously interpreted same-photo co-occurrence.

Select separate operating points:

- automatic rescue: optimize for extremely low false attachment;
- merge suggestions: optimize for high recall with human review.

Do not choose production thresholds from generic benchmark values alone.

## Modes and Rollout

Add modes:

- `legacy`: no profile computation in the decision path;
- `shadow`: compute profile decisions and telemetry without changing assignments;
- `rescue`: preserve successful legacy decisions and rescue only legacy misses;
- `primary`: profile matcher is authoritative; deferred until evidence justifies it.

Rollout order:

1. Add models, repositories, profile builder, and versioned persistence.
2. Backfill profiles at low priority without changing any `person_id`.
3. Enable shadow scoring for all legacy misses and sampled successes.
4. Use center ANN for merge suggestions, keeping manual confirmation.
5. Accumulate feedback and calibrate thresholds and margin.
6. Enable rescue mode behind a feature flag.
7. Decide later whether primary mode is necessary. Rescue mode may be the final steady state.

## Observability

Shadow and rescue telemetry records:

- component and face IDs;
- legacy target and score;
- profile best and second-best targets and scores;
- margin and matching center IDs;
- center support and confirmation state;
- profile/index generation and algorithm version;
- negative-evidence reason;
- decision and elapsed time.

Primary quality measures:

- percentage of later confirmed merges retrieved in top 10/top 20;
- merge-suggestion acceptance rate;
- new people created per 1,000 detected faces;
- rescue attachment rate and later correction rate;
- old/new target disagreement rate;
- automatic split/move/cannot-link correction rate.

Performance measures:

- ANN and exact scoring P50/P95;
- per-person profile rebuild time;
- center count distribution;
- snapshot build time and delta size;
- SQLite write time and NAS CPU utilization.

## Failure Handling and Rollback

- Profile tables and indexes are derived state; legacy assignments remain the source of truth.
- A failed person build retains its last active generation.
- Missing, corrupt, stale-model, or stale-generation profiles fall back to legacy matching.
- Mode can be changed back to `legacy` without rewriting faces or people.
- Backfill never changes historical `person_id` values.
- Profile/index generation is idempotent and resumable by person cursor.
- Merge, split, move, dissolve, redetection, and deletion invalidate only affected profiles.

## Testing Strategy

- Unit-test spherical normalization, weighted centroid updates, medoid selection, adaptive center creation, center merging, candidate quarantine, and robust scoring using deterministic synthetic vectors.
- Repository-test atomic generation activation and rollback on build failure.
- Integration-test dirty marking after detection, merge, split, move, dissolve, and redetection.
- Golden-test legacy success preservation and profile rescue of a legacy miss.
- Test `cannot_link`, best/second margin, same-photo co-occurrence, stale generation, missing index, and model-signature mismatch fallbacks.
- Benchmark center ANN build/query, delta query, profile rebuild, and rescue scoring at representative scale.

## Out of Scope

- Replacing `buffalo_sc` or recomputing embeddings.
- Automatically merging existing people in the initial version.
- Lowering current global automatic thresholds to improve recall.
- Rewriting historical face assignments during backfill.
- Removing the legacy prototype matcher before shadow and rescue evidence exists.
- Building a generic external vector database.

