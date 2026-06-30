# Photo Scan Path Lazy Stats Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Avoid requesting expensive scan-path derived statistics while the photo page's scan-path section is collapsed, and load them once when expanded.

**Architecture:** Extract the load decision into a small pure helper so the behavior can be tested without mounting the full Vue page. The page keeps loading scan-path configuration on mount, but gates derived-stat loading by expansion state and tracks whether the current page instance has already loaded it.

**Tech Stack:** Vue 3, TypeScript, Node test runner, Vite

---

### Task 1: Add the load-decision regression test

**Files:**
- Create: `frontend/tests/scanPathStatsHelpers.test.ts`
- Create: `frontend/src/views/Photos/scanPathStatsHelpers.ts`

**Step 1: Write the failing test**

Test that derived statistics load only when the section is expanded and the data has not already loaded.

**Step 2: Run test to verify it fails**

Run:

```bash
cd frontend
rm -rf .tmp-tests
npx tsc tests/scanPathStatsHelpers.test.ts src/views/Photos/scanPathStatsHelpers.ts --module nodenext --moduleResolution nodenext --target es2022 --outDir .tmp-tests
node --test .tmp-tests/tests/scanPathStatsHelpers.test.js
```

Expected: FAIL because the helper does not yet implement the required decision.

**Step 3: Implement the pure helper**

Implement `shouldLoadScanPathDerivedStatus(collapsed, loaded)` as `!collapsed && !loaded`.

**Step 4: Run test to verify it passes**

Run the same compile and test command. Expected: all tests pass.

### Task 2: Gate page-level derived-stat requests

**Files:**
- Modify: `frontend/src/views/Photos/index.vue`

**Step 1: Add page lifecycle state**

Add a boolean ref that records whether derived statistics have loaded successfully.

**Step 2: Add a guarded loader**

Use the tested helper to skip the request while collapsed or already loaded. Mark data loaded only after the API request resolves.

**Step 3: Update mount and toggle flows**

Call the guarded loader after scan-path configuration loads. When expanding the section, call it again; when collapsing, do nothing.

**Step 4: Preserve explicit refreshes**

Keep existing post-operation calls to `loadPathDerivedStatus()` so scan/rebuild/status changes can refresh displayed values.

### Task 3: Verify the frontend

**Files:**
- Test: `frontend/tests/scanPathStatsHelpers.test.ts`

**Step 1: Run the focused test**

Run the Task 1 test command and confirm all cases pass.

**Step 2: Run all lightweight frontend tests**

Compile and run the existing helper tests plus the new test.

**Step 3: Run the production build**

Run:

```bash
cd frontend
npm run build
```

Expected: TypeScript and Vite build complete successfully.

