#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(git -C "$script_dir/.." rev-parse --show-toplevel)"
cd "$repo_root"
if [[ -n "$(git status --porcelain=v1 --untracked-files=normal -- . ':(exclude).idea/**')" ]]; then
  echo "reproducibility check requires a clean tree" >&2
  exit 1
fi

ai4j_go="${AI4J_GO:-go}"
temp_root="$(mktemp -d)"
trap 'rm -rf -- "$temp_root"' EXIT

git clone --quiet --no-hardlinks "$repo_root" "$temp_root/one"
git clone --quiet --no-hardlinks "$repo_root" "$temp_root/two"

AI4J_GO="$ai4j_go" bash "$temp_root/one/scripts/build-release.sh" "$temp_root/one/dist/ai4j" > "$temp_root/one-build.json"
AI4J_GO="$ai4j_go" bash "$temp_root/two/scripts/build-release.sh" "$temp_root/two/dist/ai4j" > "$temp_root/two-build.json"

cmp "$temp_root/one/dist/ai4j" "$temp_root/two/dist/ai4j"
cmp "$temp_root/one/dist/ai4j.exe" "$temp_root/two/dist/ai4j.exe"
cmp "$temp_root/one-build.json" "$temp_root/two-build.json"
cmp "$temp_root/one/dist/ai4j.version.json" "$temp_root/two/dist/ai4j.version.json"
cmp "$temp_root/one/dist/ai4j.sha256" "$temp_root/two/dist/ai4j.sha256"
cmp "$temp_root/one/dist/ai4j.exe.version.json" "$temp_root/two/dist/ai4j.exe.version.json"
cmp "$temp_root/one/dist/ai4j.exe.sha256" "$temp_root/two/dist/ai4j.exe.sha256"
