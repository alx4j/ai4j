# MVP-ST-016 — Preview install, update, and uninstall

| Field | Value |
|---|---|
| Status | Done |
| Wave | 2 — Complete lifecycle planning |
| Requirements | MVP-FR-09, MVP-FR-13, MVP-FR-15 |

## User story

As an AI4J user, I want complete read-only lifecycle previews so I can review source, active content, actions, conflicts, and the expected result before any mutation.

## MVP scope

- Implement `install --dry-run`, `update --dry-run`, and `uninstall --dry-run` using the common human and JSON plan model.
- Reuse exact-commit package validation and active-content disclosure.
- Inspect only AI4J-owned files and Claude's documented read-only plugin/marketplace commands.
- Classify update as pinned, unchanged, fast-forward available, or rewritten.
- Check owned catalog/rules checksums and native identities before update or uninstall.
- Leave source workspaces, AI4J state, owned files, and Claude unchanged.

## Acceptance criteria

- [x] Every plan shows its exact source commit, ordered actions, active content, warnings, conflicts, and expected final state.
- [x] Install planning detects existing state, owned destinations, and native identity collisions.
- [x] Update planning reports pinned and unchanged without actions, emits actions for a fast-forward commit, and blocks a rewritten reference.
- [x] Uninstall planning removes only the recorded plugin, marketplace, catalog, rules, and installation state.
- [x] Conflicts return the conflict exit code without mutation.
- [x] Human and JSON output use the existing deterministic response contract.
- [x] Planning invokes only Git, Claude package validation, and documented read-only Claude inspection commands; it does not start toolkit content.

## Deferred

Approval, locking, the operation marker, native mutation, owned-file writes, reconciliation, and final state commit belong to the Wave 3 install vertical slice.
