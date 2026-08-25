# MVP-ST-014 — Render the exact-commit Claude catalog

| Field | Value |
|---|---|
| Status | Done |
| Wave | 2 — Complete lifecycle planning |
| Requirements | MVP-FR-09, MVP-FR-10, MVP-NFR-01 |

## User story

As an AI4J user, I want the planned Claude catalog tied to the exact validated commit so a moving branch cannot change the reviewed plugin source.

## MVP scope

- Render one `ai4j-default` plugin entry in the `ai4j` marketplace.
- Use Claude's documented `git-subdir` source with the canonical public HTTPS repository, plugin path, and full commit SHA.
- Produce deterministic JSON bytes and a SHA-256 checksum.
- Do not register, install, enable, or otherwise mutate Claude during planning.

## Acceptance criteria

- [x] The catalog contains exactly one plugin and its `sha` is the planned 40-character commit.
- [x] Identical inputs produce identical bytes and checksum.
- [x] The catalog contains no branch `ref`, credentials, secret values, or unrelated components.
- [x] Planning performs no native mutation.

## Deferred

Native registration and installation belong to the Wave 3 install vertical slice. Compatibility matrices, qualification overlays, retained packages, and rollback artifacts are V1 work.
