#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(git -C "$script_dir/.." rev-parse --show-toplevel)"
cd "$repo_root"

ai4j_go="${AI4J_GO:-go}"
export GOTOOLCHAIN=local
export GOWORK=off
export CGO_ENABLED=0

go_version="$($ai4j_go version)"
case "$go_version" in
  "go version go1.26.6 "*) ;;
  *) echo "unexpected Go toolchain: $go_version" >&2; exit 1 ;;
esac

"$ai4j_go" version
"$ai4j_go" env GOOS GOARCH CGO_ENABLED GOTOOLCHAIN GOMOD GOWORK
"$ai4j_go" run -mod=readonly ./internal/repocheck/cmd/repocheck format
"$ai4j_go" run -mod=readonly ./internal/repocheck/cmd/repocheck module
"$ai4j_go" mod tidy -diff
"$ai4j_go" mod verify
"$ai4j_go" list -m -mod=readonly all
"$ai4j_go" test -mod=readonly ./...
"$ai4j_go" vet -mod=readonly ./...
"$ai4j_go" run -mod=readonly ./internal/repocheck/cmd/repocheck authorship --range HEAD
git diff --exit-code -- .
