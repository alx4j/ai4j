#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(git -C "$script_dir/.." rev-parse --show-toplevel)"
cd "$repo_root"

ai4j_go="${AI4J_GO:-go}"
export GOTOOLCHAIN=local

if [[ "${GOWORK+x}" == "x" && "$GOWORK" != "off" ]]; then
  echo "active GOWORK override is prohibited: $GOWORK" >&2
  exit 1
fi
if [[ "${GOWORK:-}" != "off" ]]; then
  detected_work="$($ai4j_go env GOWORK)"
  if [[ -n "$detected_work" ]]; then
    echo "active Go workspace is prohibited: $detected_work" >&2
    exit 1
  fi
fi
export GOWORK=off

effective_flags="$($ai4j_go env GOFLAGS)"
effective_experiment="$($ai4j_go env GOEXPERIMENT)"
if [[ -n "$effective_flags" ]]; then
  echo "GOFLAGS must be empty for release builds: $effective_flags" >&2
  exit 1
fi
if [[ -n "$effective_experiment" ]]; then
  echo "GOEXPERIMENT must be empty for release builds: $effective_experiment" >&2
  exit 1
fi

export GOFLAGS=
export GOEXPERIMENT=
export CGO_ENABLED=0

"$ai4j_go" run -mod=readonly ./internal/repocheck/cmd/repocheck release-inputs
revision="$(git rev-parse --verify HEAD)"
output="${1:-dist/ai4j}"
if [[ "$output" != /* ]]; then
  output="$repo_root/$output"
fi
mkdir -p "$(dirname "$output")"
windows_output="$(dirname "$output")/ai4j.exe"

build_artifact() {
  local target_os="$1"
  local target_arch="$2"
  local artifact="$3"
  local artifact_sha256

  GOOS="$target_os" GOARCH="$target_arch" CGO_ENABLED=0 \
    "$ai4j_go" build -mod=readonly -trimpath -buildvcs=true -o "$artifact" ./cmd/ai4j

  "$ai4j_go" run -mod=readonly ./internal/repocheck/cmd/repocheck \
    binary --file "$artifact" --revision "$revision" > "$artifact.version.json"

  artifact_sha256="$(shasum -a 256 "$artifact" | awk '{print $1}')"
  printf '%s  %s\n' "$artifact_sha256" "$(basename "$artifact")" > "$artifact.sha256"
  cat "$artifact.version.json"
}

build_artifact darwin arm64 "$output"
build_artifact windows amd64 "$windows_output"
