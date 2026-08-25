# V1-EVAL-01 — Keep source acquisition ephemeral

- Status: Accepted
- Date: 2026-08-24
- Decision budget: 0.5 day

## Decision

AI4J v1 resolves GitHub input to an exact commit in a private temporary workspace and removes that workspace after the operation. It will not add a persistent repository or worktree cache.

## Why

The existing acquisition path already proves the selected object type, commit, root tree, and checked-out inventory. Reusing it satisfies deterministic build and lifecycle requirements without adding cache ownership, eviction, locking, corruption recovery, or credential-retention behavior.

## Rejected alternative

A persistent bare-repository cache could reduce repeated network transfer, but it is not required for v1 correctness. It would add an estimated 3–5 focused engineering days plus new cleanup and concurrency cases.

## Revisit when

Measured source acquisition is a material user-facing bottleneck and a bounded cache design has an explicit product requirement.
