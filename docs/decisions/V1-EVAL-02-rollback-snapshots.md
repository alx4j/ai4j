# V1-EVAL-02 — Do not add encrypted whole-file snapshots

- Status: Accepted
- Date: 2026-08-24
- Decision budget: 0.5 day

## Decision

AI4J v1 rollback uses structural history and bounded copies of toolkit-owned native artifacts. Encrypted whole-file snapshots and platform credential-store integration are not part of v1.

## Why

The selected approach restores AI4J-owned structures without retaining unrelated user configuration or secrets. Whole-file snapshots would require encryption-key lifecycle, Keychain and Windows credential-vault integrations, retention rules, format transitions, and recovery behavior that no current acceptance criterion needs.

## Rejected alternative

Encrypted complete preimages were estimated at 5–8 additional focused engineering days and would create a larger sensitive-data surface.

## Revisit when

A documented target API cannot provide a safe structural inverse for a required mutation and the missing rollback capability blocks a concrete v1 operation.
