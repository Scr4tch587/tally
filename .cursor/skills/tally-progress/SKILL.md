---
name: tally-progress
description: Applies Tally repo coaching, spec adherence, handwrite/generate boundaries, and progress tracking. Use when working in the Tally repository, assessing progress against docs/spec.md, planning or implementing features, reviewing code, finishing a section, or updating docs/progress.md.
---

# Tally Progress

## Required Reading

At the start of any Tally feature, review, progress assessment, or implementation task:

1. Read `docs/private/coach.md`.
2. Read the relevant sections of `docs/spec.md`.
3. Read `docs/progress.md`.

Use `docs/spec.md` as the product/architecture source of truth. Use `docs/private/coach.md` for coaching behavior, learning goals, and handwrite/generate enforcement. Use `docs/progress.md` as the living implementation tracker.

## Operating Rules

- Follow `docs/private/coach.md` interaction style: structured, step-by-step, high-signal, no hype.
- Do not apply code changes unless Kai explicitly asks for implementation.
- When a request touches a HANDWRITE zone, push back once with the specific learning or craft reason before generating code. If Kai still wants it, comply.
- When coaching HANDWRITE or handwrite-adjacent work, do not provide whole function/block code answers unless Kai explicitly asks for them. Prefer small hints, invariants, signatures, pseudocode, tests, or line-level bug descriptions. Even when identifying bugs, explain the fix without pasting the full corrected block unless requested.
- Generate freely in VIBE-CODE zones, while preserving CORE/PRODUCT boundaries from the spec.
- Re-read the relevant spec section when the active feature changes.
- Keep CORE responsible for ledger integrity, reconciliation, entity resolution, graph truth, and graph query semantics.
- Keep PRODUCT responsible for interpretation artifacts, UI state, saved views, notes, tags, pinned insights, and sandbox controls.
- Do not let PRODUCT read CORE tables directly; use the gRPC boundary described by the spec.

## Task Breakdown Workflow

For every non-trivial chunk of work:

1. Break the chunk into small tasks before starting.
2. Summarize all tasks briefly at the beginning.
3. Mark the first task as the active task.
4. Serve tasks one by one.
5. For the active task, give more depth: goal, relevant files/spec sections, implementation or review focus, and verification.
6. Do not expand later tasks in detail until they become active.
7. After each task, update progress and move to the next task.

Keep the initial task list short and scannable. The detailed explanation belongs with the current task, not the whole plan.

## Progress Tracker Workflow

Before changing the repo:

1. Check `docs/progress.md` for current blockers, incomplete items, and the relevant phase checklist.
2. Identify which checklist items the requested change should affect.
3. If the change touches a handwrite zone, apply the coach-doc pushback rule before writing code.

After changing the repo:

1. Run the most relevant verification available: tests, build, migration check, benchmark, lint, or manual smoke check.
2. Update `docs/progress.md` in the same work session.
3. Mark items complete only when implementation is wired into the normal path and has basic verification.
4. Leave partial work unchecked and label it `PARTIAL` when useful.
5. Add or remove blockers based on the actual new state.
6. Update `Latest Verification` with what was run and the result.
7. Give Kai a very short suitable commit message for the progress update.

## Section Completion Trigger

When Kai says a section, milestone, checklist area, or feature is done, immediately update `docs/progress.md` without waiting for a separate request.

Do this workflow:

1. Identify the relevant `docs/progress.md` section and spec section.
2. Inspect the repo state needed to verify completion.
3. Run the most relevant lightweight verification if available.
4. Mark completed items only if they satisfy the completion standard below.
5. Keep unverified or partially wired work unchecked and label it `PARTIAL` if that is the most accurate state.
6. Update `Current Snapshot`, `Current Blockers`, and `Latest Verification` if the section completion changes them.
7. Tell Kai exactly what was updated and what remains unchecked.
8. Give Kai a very short suitable commit message for the progress update.

If Kai says a section is done but the repo state does not support that yet, do not mark it complete. Explain the missing evidence and update the tracker with a blocker or partial note instead.

## Completion Standard

A progress item is complete only when:

- The implementation exists in the expected repo location.
- It is integrated into the normal execution path, not just a scratch file.
- It respects tenant scoping and CORE/PRODUCT ownership where applicable.
- It has at least basic verification.
- It does not contradict `docs/spec.md`.

If any of those are missing, keep the item unchecked.

## Common References

- Current spec: `docs/spec.md`
- Coaching and handwrite/generate rules: `docs/private/coach.md`
- Living progress checklist: `docs/progress.md`
- Local verification: `go test ./...`
- Local services: `docker compose up -d`
- Migrations: `make migrate`

