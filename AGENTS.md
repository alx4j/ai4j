# AI4J Repository Instructions

## Project identity

- Product name: **AI4J**
- Canonical executable: `ai4j` on macOS and `ai4j.exe` on Windows
- Canonical repository identity: `github.com/alx4j/ai4j`
- Project URL: `https://github.com/alx4j/ai4j`
- Git remote: `git@github.com:alx4j/ai4j.git`
- Go module: `github.com/alx4j/ai4j`

“Toolkit” remains the generic name for installable AI content. Do not rename domain terms such as toolkit identifier, toolkit content, toolkit-owned state, or `toolkit.json` to AI4J.

## Git attribution and publishing

Every commit created or pushed for this repository must use both of these exact Git identities:

```text
Author:    Oleksii Stupin <oleksii.stupin@gmail.com>
Committer: Oleksii Stupin <oleksii.stupin@gmail.com>
```

- Set and verify `user.name` and `user.email` in the repository-local Git configuration before committing.
- Do not add bot, assistant, generated-by, or `Co-authored-by` attribution.
- Push only to the canonical `origin` remote unless Oleksii explicitly changes the destination.
- Do not rewrite published history unless Oleksii explicitly requests it.

## Product and architecture guardrails

- Keep implementation aligned with the user-visible workflows in `README.md`.
- Implement the CLI in modern, idiomatic Go using the repository-pinned supported toolchain.
- Keep the MVP core target-, host-, and source-neutral behind compile-time interfaces; model scope and selection as explicit typed core contracts; expose only the MVP capabilities in its CLI.
- Use documented target-native interfaces. Do not edit Claude or Codex private caches or registries directly.
- Preserve exact-commit source resolution, active-content disclosure, no implicit execution, structural recovery, and fail-closed ownership rules.
- Keep first-party installable content self-contained under its declared package roots. Go source, CI configuration, and unrelated repository files are never installable content.
