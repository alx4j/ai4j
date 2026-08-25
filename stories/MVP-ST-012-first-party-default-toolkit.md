# MVP-ST-012 — Ship the first-party default toolkit

| Field | Value |
|---|---|
| Status | Defined |
| Type | User content increment |
| Wave | 1 — Secure read-only validation |
| Relative size | M |
| Depends on | MVP-ST-001, MVP-ST-009, MVP-ST-011 |
| Requirements | MVP-AR-03, MVP-FR-02, MVP-FR-05, MVP-FR-12 |
| MVP acceptance | 27, 28 |

## User story

As an AI4J user, I want the AI4J repository to contain a useful default toolkit so that an omitted-source install delivers immediate value and validates the complete product path.

## Outcome

The repository contains a non-empty, self-contained `ai4j-default` Claude plugin selected by the root manifest and developer marketplace.

## Scope

- Add `toolkit.json`, `.claude-plugin/marketplace.json`, and `plugins/ai4j-default/.claude-plugin/plugin.json`.
- Include at least one useful skill with reference/support content, one subagent, non-empty shared rules, and one command-based MCP declaration.
- Declare a bundled compatible executable or a documented clean-host external executable for the MCP.
- Keep every plugin asset under `plugins/ai4j-default` and every shared rule under a manifest-declared closed content root.
- Use the committed developer marketplace's relative plugin source; leave exact-SHA generated catalog rendering to MVP-ST-014.
- Omit plugin and marketplace `version` fields so native update identity falls through to the exact Git SHA.
- Include only MVP-allowed component types and no supplemental marketplace components, dependencies, interactive configuration, or auto-discovered unsupported directory.
- Ensure product source, `go.mod`, `go.sum`, CI, release, and signing files are not toolkit assets.

## MVP delivery rule

Ship the smallest useful non-empty first-party package: one skill with support material, one subagent, shared rules, and one MCP declaration. Choose the simplest executable mode that works on the supported clean host and defer alternate modes, fallbacks, and extra components.

## Technical substories

Complete these technical substories in order. Each substory starts only after the previous substory's completion evidence passes.

### MVP-ST-012.T1 — Declare the first-party toolkit boundary

**Implementation**

- Add the versioned root `toolkit.json` contract for toolkit `ai4j`, selected plugin `ai4j-default`, the dedicated marketplace identity, supported host/Claude ranges, executable/runtime inventory, optional shared-rules source, and closed package/content roots.
- Select only tracked relative paths beneath the declared roots and keep product source, module files, CI, release, signing, story, and unrelated repository paths outside the installable set.
- Keep secret-bearing configuration reference-only and declare every runtime dependency as required or optional without embedding a credential or environment value.

**Completion evidence**

- Root-manifest tests accept the intended manifest and reject a changed identity, open root, out-of-root asset, undeclared executable, unsupported schema, or inline secret field.
- An inventory snapshot proves the initial declared boundary excludes every non-toolkit repository category named by the parent story.

### MVP-ST-012.T2 — Define the developer marketplace and plugin manifest

**Implementation**

- Add one committed developer marketplace entry whose relative source is `./plugins/ai4j-default`, and add the selected plugin manifest beneath `plugins/ai4j-default/.claude-plugin/`.
- Make the plugin manifest authoritative for its explicitly declared MVP components, include no dependency or supplemental component, and omit marketplace and plugin version fields.
- Use only fields and component locations accepted by the supported Claude contract; do not add hooks, LSP, monitor, theme, channel, settings, interactive configuration, or unsupported auto-discovery directories.

**Completion evidence**

- AI4J schema tests prove exactly one relative-path plugin, consistent identities, strict component ownership, no version precedence, and no external dependency.
- Supported Claude validator fixtures accept both the marketplace and plugin, while one fixture per prohibited component demonstrates fail-closed rejection.

### MVP-ST-012.T3 — Add a useful skill and support material

**Implementation**

- Add at least one focused first-party skill under `plugins/ai4j-default/skills/` with a stable name, clear trigger description, bounded instructions, and a referenced support or reference file within the same skill root.
- Keep the skill self-contained, human-reviewable, and free of hooks, implicit startup behavior, inline secrets, absolute paths, or dependencies outside the selected plugin.
- Declare every support file through the closed content boundary so no sibling repository content becomes reachable by convention.

**Completion evidence**

- AI4J and supported Claude validation accept the skill metadata and support reference without executing either file.
- Inventory assertions identify the skill and support file with canonical paths and checksums and reject an out-of-root or undeclared reference.

### MVP-ST-012.T4 — Add the subagent and shared rules

**Implementation**

- Add one focused subagent under the plugin's declared agent root using only the supported, MVP-allowed frontmatter and a bounded instruction body.
- Add non-empty shared rules beneath the manifest-declared shared-rules content root, separate from existing user `CLAUDE.md` or unrelated rule files.
- Keep both resources declarative and self-contained; do not add a lifecycle script, hidden executable, unsupported tool integration, or secret value.

**Completion evidence**

- AI4J and supported Claude fixtures accept the subagent, and AI4J validation accepts the shared-rules source as one declared regular file.
- Inventory tests expose both resources and reject unsupported agent fields, empty rules, path escape, and undeclared adjacent content.

### MVP-ST-012.T5 — Add a statically valid command-based MCP declaration

**Implementation**

- Add one command-based `.mcp.json` entry with an executable, ordered argument vector, only supported path placeholders, and reference-only environment entries.
- Declare exactly one explicit executable mode in the manifest: either a bundled regular `darwin/arm64` artifact beneath the plugin root or a documented executable available on the clean-host baseline. Do not infer or fall back between modes, and classify every additional runtime dependency as required or optional.
- Keep the declaration dormant: do not add install hooks, startup probes, wrapper-shell strings, or configuration that requires resolving a secret value.

**Completion evidence**

- MVP-ST-011 fixtures accept the MCP fields, explicit executable mode, ownership, static compatibility, dependency classification, modes, placeholders, and unchanged environment references, and reject an undeclared or mixed bundled/host mode.
- Process and network sentinels prove AI4J and supported Claude validation do not start the MCP command or declared executable.

### MVP-ST-012.T6 — Qualify the complete first-party package

**Implementation**

- Validate the complete root manifest, committed developer marketplace, plugin manifest, skill/support material, subagent, shared rules, MCP declaration, and executable inventory as one immutable first-party package.
- Generate the deterministic package inventory and rendered-input digest, then compare them with an explicit expected set that excludes all product and repository-only files.
- Run the same validation from a clean checkout and under every supported Claude profile without enabling, installing, reloading, or otherwise activating the plugin. If executable mode is `bundled`, capture its source-to-byte build input and digest contract for MVP-ST-028; if mode is `host`, prove no bundled executable byte enters the package and record the clean-host name/version contract.

**Completion evidence**

- Repeated clean-checkout qualification yields the same sorted inventory and digest and passes both AI4J and supported Claude validation.
- The exact expected-set assertion fails on any missing required component, unexpected auto-discovered component, version field, external dependency, non-toolkit repository byte, or executable-mode/build-contract inconsistency.

## Story acceptance criteria

- [ ] The root manifest identifies toolkit `ai4j` and native plugin `ai4j-default` consistently.
- [ ] AI4J and supported Claude validators accept the complete first-party package without executing it.
- [ ] The package inventory contains the skill, support content, subagent, shared rules, MCP declaration, and declared executable facts.
- [ ] No Go source, module, CI, release, signing, or unrelated file enters the package inventory.
- [ ] Plugin and marketplace metadata omit version fields and every component outside the explicit MVP allowlist.

## Verification

- Validate the first-party package in CI as both repository content and rendered native input.
- Run no-execution sentinels around AI4J and supported Claude validation of the package.

## Out of scope

- Multiple plugins, bundles, per-asset selection, hooks, LSP servers, or external plugin dependencies.
