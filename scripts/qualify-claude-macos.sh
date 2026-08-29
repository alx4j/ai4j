#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(git -C "$script_dir/.." rev-parse --show-toplevel)"
cd "$repo_root"

qualification_ref="${AI4J_QUALIFICATION_REF:?AI4J_QUALIFICATION_REF is required}"
qualification_source_ref="${AI4J_QUALIFICATION_SOURCE_REF:?AI4J_QUALIFICATION_SOURCE_REF is required}"
evidence_root="${AI4J_QUALIFICATION_EVIDENCE:?AI4J_QUALIFICATION_EVIDENCE is required}"
claude_version="${AI4J_CLAUDE_VERSION:?AI4J_CLAUDE_VERSION is required}"
github_token="${AI4J_QUALIFICATION_GITHUB_TOKEN:?AI4J_QUALIFICATION_GITHUB_TOKEN is required}"
work_root="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/ai4j-claude-qualification.XXXXXX")"
release_root="$work_root/release"
ai4j="$release_root/ai4j"
active_installation=""
credential_token_file="$work_root/github-token"
credential_helper="$work_root/git-credential-ai4j"
qualification_git_config="$work_root/gitconfig"

mkdir -p "$evidence_root"

(
  umask 077
  printf '%s' "$github_token" > "$credential_token_file"
  {
    printf '%s\n' '#!/usr/bin/env bash' 'set -euo pipefail'
    printf 'token_file=%q\n' "$credential_token_file"
    printf '%s\n' \
      'if [[ "${1:-}" == "get" ]]; then' \
      '  printf "username=x-access-token\\npassword="' \
      '  tr -d "\\r\\n" < "$token_file"' \
      '  printf "\\n"' \
      'fi'
  } > "$credential_helper"
)
chmod 700 "$credential_helper"
unset github_token AI4J_QUALIFICATION_GITHUB_TOKEN
export GIT_CONFIG_GLOBAL="$qualification_git_config"
git config --file "$qualification_git_config" credential.helper "!$credential_helper"
credential_probe="$(printf 'protocol=https\nhost=github.com\n\n' | git credential fill)"
grep -q '^username=x-access-token$' <<< "$credential_probe"
grep -q '^password=.' <<< "$credential_probe"
unset credential_probe

cleanup() {
  if [[ -n "$active_installation" && -x "$ai4j" ]]; then
    "$ai4j" uninstall "$active_installation" --yes --json >/dev/null 2>&1 || true
  fi
  rm -rf -- "$work_root"
}
trap cleanup EXIT

assert_success() {
  local document="$1"
  jq -e '(.status == "ok" or .status == "no_change") and .exitCode == 0 and (.errors | length == 0)' "$document" >/dev/null
}

assert_default_bundle_status() {
  local document="$1"
  jq -e '.data.installation.nativePluginIds == ["ai4j-review", "ai4j-tools"] and
    .data.summary.requestedBundle == "default" and
    .data.summary.resolvedBundles == ["default", "review", "tools"] and
    .data.summary.packages == ["ai4j-review", "ai4j-tools"]' "$document" >/dev/null
}

run_ai4j() {
  local evidence_name="$1"
  shift
  "$ai4j" "$@" --json | tee "$evidence_root/$evidence_name"
  assert_success "$evidence_root/$evidence_name"
}

run_project_journey() {
  local scope="$1"
  local project_root="$work_root/project-$scope"
  local prefix="project-$scope"
  local marketplace_id

  git clone --quiet --no-hardlinks "$repo_root" "$project_root"
  run_ai4j "$prefix-plan.json" install --dry-run \
    --repo alx4j/ai4j --ref "$qualification_source_ref" \
    --target claude --scope "$scope" --project "$project_root" --bundle default
  run_ai4j "$prefix-install.json" install \
    --repo alx4j/ai4j --ref "$qualification_source_ref" \
    --target claude --scope "$scope" --project "$project_root" --bundle default \
    --expected-commit "$qualification_ref" --yes

  active_installation="$(jq -er '.data.installationId' "$evidence_root/$prefix-install.json")"
  run_ai4j "$prefix-status.json" status "$active_installation"
  jq -e '.data.nativeState.registration == "registered" and
    .data.nativeState.installation == "installed" and
    .data.nativeState.enablement == "enabled"' "$evidence_root/$prefix-status.json" >/dev/null
  assert_default_bundle_status "$evidence_root/$prefix-status.json"
  marketplace_id="$(jq -er '.data.actions[] | select(.kind == "register_marketplace") | .resource' "$evidence_root/$prefix-plan.json")"
  (
    cd "$project_root"
    claude plugin list --json
  ) | tee "$evidence_root/$prefix-plugin-list.json"
  while IFS= read -r native_plugin_id; do
    jq -e --arg id "${native_plugin_id}@${marketplace_id}" '[.. | strings] | index($id) != null' \
      "$evidence_root/$prefix-plugin-list.json" >/dev/null
  done < <(jq -er '.data.installation.nativePluginIds[]' "$evidence_root/$prefix-status.json")
  (
    cd "$project_root"
    claude plugin marketplace list --json
  ) | tee "$evidence_root/$prefix-marketplace-list.json"
  jq -e --arg id "$marketplace_id" '[.. | strings] | index($id) != null' \
    "$evidence_root/$prefix-marketplace-list.json" >/dev/null

  if [[ "$scope" == "project-local" ]]; then
    local rules_file
    rules_file="$(find "$project_root/.claude/rules" -type f -name '*.md' -print -quit)"
    test -n "$rules_file"
    git -C "$project_root" check-ignore -v "$rules_file" \
      | tee "$evidence_root/$prefix-git-exclusion.txt"
  else
    jq . "$project_root/.claude/settings.json" \
      | tee "$evidence_root/$prefix-settings.json" >/dev/null
    jq -e --arg id "$marketplace_id" --arg sha "$qualification_ref" \
      '.extraKnownMarketplaces[$id].source.source == "settings" and
       (.extraKnownMarketplaces[$id].source.plugins | map(.name)) == ["ai4j-review", "ai4j-tools"] and
       all(.extraKnownMarketplaces[$id].source.plugins[];
         .source.source == "git-subdir" and
         .source.sha == $sha and
         .source.path == ("plugins/" + .name))' \
      "$evidence_root/$prefix-settings.json" >/dev/null
  fi

  run_ai4j "$prefix-doctor.json" doctor "$active_installation"
  run_ai4j "$prefix-uninstall.json" uninstall "$active_installation" --yes
  active_installation=""
  git -C "$project_root" status --short --untracked-files=all > "$evidence_root/$prefix-post-uninstall-git-status.txt"
  test ! -s "$evidence_root/$prefix-post-uninstall-git-status.txt"
}

command -v claude git go jq shasum sw_vers >/dev/null
test "$(uname -m)" = "arm64"
test "$RUNNER_OS/$RUNNER_ARCH" = "macOS/ARM64"
[[ "$(sw_vers -productVersion)" == 15.* ]]
test "$(go env GOVERSION)" = "go1.26.6"
test "$(go env GOOS)/$(go env GOARCH)" = "darwin/arm64"
test "$(go env CGO_ENABLED)" = "0"
claude --version | tee "$evidence_root/claude-version.txt"
grep -Eq "^${claude_version//./\.}([[:space:]]|$)" "$evidence_root/claude-version.txt"

{
  printf 'runner=%s/%s\n' "$RUNNER_OS" "$RUNNER_ARCH"
  printf 'macos_product_version=%s\n' "$(sw_vers -productVersion)"
  printf 'macos_build_version=%s\n' "$(sw_vers -buildVersion)"
  printf 'machine=%s\n' "$(uname -m)"
  printf 'git=%s\n' "$(git --version)"
  printf 'go=%s\n' "$(go env GOVERSION)"
  printf 'claude=%s\n' "$(head -n 1 "$evidence_root/claude-version.txt")"
  printf 'source_ref=%s\n' "$qualification_source_ref"
  printf 'commit=%s\n' "$qualification_ref"
} | tee "$evidence_root/environment.txt"

go test -mod=readonly ./internal/host/darwin/installlock \
  -run 'TestLock(BlocksConcurrentMutationAndReleases|IsReleasedWhenOwnerProcessExits)$' \
  -count=1 | tee "$evidence_root/darwin-lock-tests.txt"

for package in ai4j-review ai4j-tools; do
  (
    cd "plugins/$package"
    claude plugin validate . --strict
  ) 2>&1 | tee "$evidence_root/native-plugin-validate-$package.txt"
done

bash scripts/build-release.sh "$ai4j" | tee "$evidence_root/release-build.txt"
(
  cd "$release_root"
  shasum -a 256 -c ai4j.sha256
) | tee "$evidence_root/release-checksum.txt"
run_ai4j version.json version
jq -e '.data.target.os == "darwin" and .data.target.arch == "arm64"' "$evidence_root/version.json" >/dev/null

run_ai4j validate.json validate \
  --repo alx4j/ai4j --ref "$qualification_source_ref" --target claude
jq -e '.data.validation.valid == true' "$evidence_root/validate.json" >/dev/null

run_ai4j user-plan.json install --dry-run \
  --repo alx4j/ai4j --ref "$qualification_source_ref" \
  --target claude --scope user --bundle default
run_ai4j user-install.json install \
  --repo alx4j/ai4j --ref "$qualification_source_ref" \
  --target claude --scope user --bundle default \
  --expected-commit "$qualification_ref" --yes

active_installation="$(jq -er '.data.installationId' "$evidence_root/user-install.json")"
run_ai4j user-status.json status "$active_installation"
jq -e '.data.nativeState.registration == "registered" and
  .data.nativeState.installation == "installed" and
  .data.nativeState.enablement == "enabled"' "$evidence_root/user-status.json" >/dev/null
assert_default_bundle_status "$evidence_root/user-status.json"
marketplace_id="ai4j-${active_installation}"

claude plugin marketplace list --json | tee "$evidence_root/user-marketplace-list.json"
jq -e --arg id "$marketplace_id" '[.. | strings] | index($id) != null' \
  "$evidence_root/user-marketplace-list.json" >/dev/null
claude plugin list --json | tee "$evidence_root/user-plugin-list.json"
while IFS= read -r native_plugin_id; do
  jq -e --arg id "${native_plugin_id}@${marketplace_id}" '[.. | strings] | index($id) != null' \
    "$evidence_root/user-plugin-list.json" >/dev/null
done < <(jq -er '.data.installation.nativePluginIds[]' "$evidence_root/user-status.json")

claude plugin marketplace update "$marketplace_id" | tee "$evidence_root/user-marketplace-update.txt"
while IFS= read -r native_plugin_id; do
  claude plugin update "${native_plugin_id}@${marketplace_id}" --scope user \
    | tee "$evidence_root/user-plugin-update-$native_plugin_id.txt"
done < <(jq -er '.data.installation.nativePluginIds[]' "$evidence_root/user-status.json")
run_ai4j user-status-after-refresh.json status "$active_installation"

run_ai4j user-doctor.json doctor "$active_installation"
set +e
"$ai4j" doctor "$active_installation" --test-mcp claude-tools --json \
  > "$evidence_root/user-mcp-preview.json"
preview_exit=$?
set -e
test "$preview_exit" = "2"
jq -e '.status == "error" and .exitCode == 2 and .data.startupCheck != null' \
  "$evidence_root/user-mcp-preview.json" >/dev/null

run_ai4j user-mcp-startup.json doctor "$active_installation" \
  --test-mcp claude-tools --yes
jq -e '.data.startupCheck.result == "timed_out" or .data.startupCheck.result == "exited"' \
  "$evidence_root/user-mcp-startup.json" >/dev/null

run_ai4j user-uninstall.json uninstall "$active_installation" --yes
active_installation=""
claude plugin marketplace list --json | tee "$evidence_root/user-post-uninstall-marketplace-list.json"
jq -e --arg id "$marketplace_id" '[.. | strings] | index($id) == null' \
  "$evidence_root/user-post-uninstall-marketplace-list.json" >/dev/null
claude plugin list --json | tee "$evidence_root/user-post-uninstall-plugin-list.json"
while IFS= read -r native_plugin_id; do
  jq -e --arg id "${native_plugin_id}@${marketplace_id}" '[.. | strings] | index($id) == null' \
    "$evidence_root/user-post-uninstall-plugin-list.json" >/dev/null
done < <(jq -er '.data.installation.nativePluginIds[]' "$evidence_root/user-status.json")

run_project_journey project-local
run_project_journey project-shared

printf 'PASS: Claude %s on %s (%s) at %s\n' \
  "$claude_version" "$(sw_vers -productVersion)" "$(uname -m)" "$qualification_ref" \
  | tee "$evidence_root/qualification-summary.txt"
