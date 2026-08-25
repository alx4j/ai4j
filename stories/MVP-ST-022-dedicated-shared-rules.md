# MVP-ST-022 — Manage the dedicated shared-rules file

| Field | Value |
|---|---|
| Status | Done |
| Type | Claude adapter resource |
| Wave | 3 — First complete installation |
| Relative size | S |
| Depends on | MVP-ST-010, MVP-ST-015, MVP-ST-018 |
| Requirements | MVP-FR-06, MVP-FR-11, MVP-NFR-05 |
| MVP acceptance | 6, 9, 12 |

## User story

As an AI4J user, I want shared instructions stored in one clearly owned file so unrelated Claude configuration is preserved.

## Scope

- Manage only `~/.claude/rules/ai4j.md` for the MVP Claude user scope.
- Plan create, checksum-matched update, checksum-matched removal, or `no_change`.
- Treat an unmanaged existing file, recorded checksum mismatch, symlink, non-regular file, or policy block as a conflict.
- Write with a same-directory temporary file and atomic replacement.
- Never edit `CLAUDE.md` or any unrelated rule file.

## Acceptance criteria

- [x] Install creates only the dedicated rules file with the validated first-party bytes.
- [x] A converged file produces no write.
- [x] Update and uninstall require the checksum recorded in installation state.
- [x] Conflict leaves the file and unrelated Claude configuration byte-identical.

## Verification

- Test absent, converged, update, remove, unmanaged, drifted, symlink, and policy-blocked cases.
- Assert unrelated rules and `CLAUDE.md` remain unchanged.

## Out of scope

- Managed Markdown sections, alternate paths, project scope, structural inverses, and compensation descriptors.
