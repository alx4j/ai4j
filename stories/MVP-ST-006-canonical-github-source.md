# MVP-ST-006 — Select and canonicalize a GitHub source

| Field | Value |
|---|---|
| Status | Defined |
| Type | User capability |
| Wave | 1 — Secure read-only validation |
| Relative size | M |
| Depends on | MVP-ST-002, MVP-ST-003 |
| Requirements | MVP-FR-02 |
| MVP acceptance | 1, 25, 26, 35 |

## User story

As an AI4J user, I want safe GitHub source selection with a convenient first-party default so that I can choose content without ambiguous repository identity or credential handling.

## Outcome

The source parser accepts only the three supported GitHub forms, expands the built-in default explicitly, and produces one sanitized canonical identity.

## Scope

- Accept `owner/repository`, canonical HTTPS, and canonical GitHub SSH forms.
- Serialize identity only as lower-case `github.com/<owner>/<repository>` without `.git`.
- Reject alternate hosts and transports, URL user information, credentials, query/fragment data, local paths, helpers, option-like values, and control characters.
- Expand omitted `--repo` to `github.com/alx4j/ai4j`; apply `--ref` alone to that repository.
- Preserve `sourceSelection` as `built_in_default` or `explicit`.
- Use existing Git/SSH authentication without storing credentials or implementing OAuth.
- Never fall back from an invalid explicit source to the built-in default.

## MVP delivery rule

Deliver anonymous HTTPS selection for the built-in public repository first. Keep this story pure and credential-free; add private HTTPS and SSH handoff fixtures only after the public path works, and leave real Git process behavior to MVP-ST-007.

## Technical substories

Complete these technical substories in order. Each substory starts only after the previous substory's completion evidence passes.

### MVP-ST-006.T1 — Parse the three allowed repository forms

**Implementation**

- Implement a pure parser in `internal/source/github` for `owner/repository`, canonical GitHub HTTPS, and canonical GitHub SSH forms while preserving whether input was omitted or explicitly supplied.
- Reject non-GitHub hosts, alternate protocols, helpers, local paths, URL user information, credentials, queries, fragments, NUL or control characters, option-like repository or reference values, and ambiguous owner/repository boundaries before any external dependency is called.

**Completion evidence**

- Table-driven and fuzz-seed tests cover accepted equivalents, boundary lengths, mixed case, `.git` handling, every rejected family, malformed UTF-8, and option injection without panics or unbounded allocation.
- Authentication, state, lock, and source-acquisition spies prove invalid input returns before any of those boundaries is invoked.

### MVP-ST-006.T2 — Create one canonical repository identity

**Implementation**

- Construct the ST-002 repository identity only as lower-case `github.com/<owner>/<repository>` without `.git`, with validated owner and repository components and stable equality and text encoding.
- Model `https` and `ssh` as a closed credential-free transport preference separate from repository identity; the preference may be persisted for later access continuity, but a raw URL, endpoint, credential, user information, helper response, or command must never be persisted.
- Construct an operation-local `github.Remote` deterministically from the canonical identity and selected preference, use HTTPS for shorthand input, and preserve SSH only when accepted input explicitly selects it.

**Completion evidence**

- Equivalence tests prove all three accepted forms produce the same canonical identity and that transport selection does not affect identity equality.
- Compile-time serializer fixtures and golden output prove only the closed transport preference can accompany persisted identity; raw URLs, endpoints, credentials, user information, and helper responses cannot enter state or user-visible identity fields.

### MVP-ST-006.T3 — Expand built-in and explicit source selection

**Implementation**

- Add a pure selection resolver that expands an omitted repository to `github.com/alx4j/ai4j`, applies a lone `--ref` to that repository, and records `built_in_default` or `explicit` without granting different trust behavior.
- Preserve the distinction between omitted and explicitly empty input, never infer source from the working directory, executable, Git remote, state, or Claude, and never fall back after an explicit parse or authentication failure.

**Completion evidence**

- Matrix tests cover omitted source/ref, ref only, explicit first-party equivalents, explicit alternative repositories, explicitly empty values, and invalid explicit repositories.
- Downstream-spy tests prove default expansion occurs before identity comparison and that every failed explicit selection returns without consulting a fallback provider.

### MVP-ST-006.T4 — Define the credential-free Git handoff contract

**Implementation**

- Produce an immutable effective-source request containing source selection, canonical identity, selected transport preference, deterministically reconstructed sanitized transport endpoint, and requested reference for consumption by `internal/source/git`.
- Delegate authentication exclusively to system Git, SSH, and their existing helpers; define no credential field, callback, OAuth flow, secret resolver, or serialized raw command in the source contract.

**Completion evidence**

- A recording Git-boundary fixture verifies fresh input and a persisted transport preference reconstruct the same accepted GitHub endpoint and requested reference and never ask AI4J to read, return, or persist credential material.
- Secret-canary scans across typed results, human/JSON goldens, errors, and serialized fixtures find no credential, helper response, or unsanitized endpoint.

### MVP-ST-006.T5 — Qualify canonicalization and authentication pass-through

**Implementation**

- Build a source-selection contract harness that first feeds the built-in public HTTPS source to a recording Git boundary, then covers credential-protected HTTPS and SSH without copying credential facts into AI4J values.
- Exercise parser, default expansion, transport handoff, error mapping, and output serialization without producing commit provenance, running Git, or creating a persistent checkout.

**Completion evidence**

- Public HTTPS, private HTTPS/SSH handoff, omitted-default, explicit-default, failed-authentication, and invalid-explicit-source cases produce sanitized identities, transport preferences, and selection facts.
- The omitted and explicit first-party cases differ only in `sourceSelection`; invalid input and failed authentication never trigger default fallback or create AI4J-owned credential artifacts.

## Story acceptance criteria

- [ ] All supported forms normalize to the same canonical identity and transport choice does not enter persisted identity.
- [ ] Every rejected form fails before authentication, locking, state lookup, or source acquisition.
- [ ] Omitted and explicitly equivalent first-party sources yield equal effective repository facts except for `sourceSelection`.
- [ ] A bad explicit repository never downgrades to the default.
- [ ] Public and private sources can be handed to system Git without adding a credential channel or persisting credential material in AI4J.
- [ ] The typed source result and its human/JSON serializers never expose credentials or unsanitized URLs; downstream plans consume only that sanitized result.

## Verification

- Run a table-driven parser and canonicalization suite, including control and option-injection cases.
- Exercise the public default first, then recording private HTTPS/SSH and failed-authentication boundary cases.
- Assert default expansion occurs before identity and state comparisons.

## Out of scope

- Non-GitHub hosts, local source, OAuth, credential storage, or changing an existing installation's repository.
