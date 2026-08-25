# V1-EVAL-03 — Cross-target fidelity baseline

- Status: Accepted for schema work
- Date: 2026-08-24
- Evidence: `ai4j build` outputs and `internal/validate/build_test.go`

## Native interfaces

- Claude output follows the documented plugin layout and is checked with `claude plugin validate`. See [Claude Code plugin reference](https://code.claude.com/docs/en/plugins-reference).
- Codex output follows the documented plugin layout, project agent configuration, persistent instruction, and MCP configuration contracts. See [Codex plugins](https://developers.openai.com/plugins/concepts/plugins), [skills](https://developers.openai.com/codex/skills), [custom agents](https://learn.chatgpt.com/docs/agent-configuration/subagents), [AGENTS.md](https://developers.openai.com/codex/guides/agents-md), and [MCP](https://developers.openai.com/codex/mcp).

## Fidelity matrix

| Canonical content | Claude output | Codex output | Fidelity |
|---|---|---|---|
| Skill, references, and scripts | `plugin/skills/<id>/` | `plugin/skills/<id>/` | Exact |
| Review agent | `plugin/agents/<id>.md` | `configuration/.codex/agents/<id>.toml` | Exact intent; native syntax transformation |
| Shared instruction | `configuration/rules/<id>.md` | `configuration/AGENTS.md` | Exact content |
| Command-based MCP declaration | `plugin/.mcp.json` | `plugin/.mcp.json` | Exact command and arguments; same-name environment references use Codex `env_vars` |
| Representative canonical hook | Not emitted | Not emitted | The Wave 0 manifest cannot declare a hook, so hook selection is explicitly unsupported even though both native plugin formats provide hooks |

## Consequences

- One canonical exact commit can produce both target outputs without installing content or editing a target cache or registry.
- `ai4j-build.json` records the aggregate input digest, CLI build commit, target capability profile, and every payload checksum; the command result also checksums the manifest itself. Identical inputs produce identical bytes.
- Codex hook selection must fail as unsupported in the v1 selection model until a documented equivalent is approved. The unsupported mapping is not silently dropped.
