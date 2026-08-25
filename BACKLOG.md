# AI4J backlog

These items are intentionally outside the completed v1 automated-delivery scope.

## Post-v1 release hardening

- Sign and notarize the macOS Apple Silicon executable with an Apple Developer ID when public distribution volume justifies the account, credential, and CI complexity.
- Authenticode-sign the Windows x64 executable when public distribution volume justifies acquiring and operating a trusted signing identity.
- Generate and publish SBOMs and provenance attestations when downstream consumers require machine-verifiable supply-chain evidence.
- Keep these items outside the v1 exit gate. v1 publishes reproducible unsigned executables with SHA-256 checksums and clear verification instructions.

## Post-v1 publication and interactive qualification

- Publish the Homebrew formula after an immutable AI4J release binary and checksum are available from a public URL. The public tap must not point to a private or moving asset.
- Run a manual Codex `/plugins` installation smoke with the qualified Windows package when interactive acceptance evidence is needed; the documented interface does not expose a non-interactive lifecycle command.

## V1 hardening and cleanup

- Re-evaluate automated Codex install, enable, update, status, and uninstall if Codex publishes a documented non-interactive plugin lifecycle interface. Do not implement this through private Codex caches, databases, or registries.
- Add a macOS watchdog only if AI4J must guarantee descendant-process termination after an uncatchable parent-process death. Current macOS process groups terminate on normal completion, timeout, and cancellation; Windows Job Objects also terminate descendants when the AI4J process exits.
- Add partial native compensation only when a documented Claude interface exposes the exact post-operation identity required to prove a safe inverse. Current automatic recovery rolls forward or cleans up exact recorded before/after states and retains `recovery_required` for mixed or unobservable state.
- Add Claude policy, reload, next-session, current-session activation, and native-version observations only when a documented interface makes them truthful; Wave 4 reports them as `not_observable`.
- Expand package diagnostics from the first stable error to a bounded multi-error report only if user feedback shows it is needed.
