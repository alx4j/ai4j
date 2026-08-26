package cli_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/fault"
)

const commitOID = "0123456789abcdef0123456789abcdef01234567"

func TestParserAcceptsEveryCanonicalCommandAndApplicableOption(t *testing.T) {
	t.Parallel()

	parser := cli.NewParser("darwin")
	tests := []struct {
		name    string
		argv    []string
		command cli.Command
		typeOf  any
	}{
		{name: "init", argv: []string{"ai4j", "init", "--target", "claude", "--target=codex", "--output", "new-toolkit", "--examples", "--json"}, command: cli.CommandInit, typeOf: cli.InitRequest{}},
		{name: "validate", argv: []string{"ai4j", "validate", "--repo", "alx4j/ai4j", "--ref=main", "--target", "claude", "--json"}, command: cli.CommandValidate, typeOf: cli.ValidateRequest{}},
		{name: "build", argv: []string{"ai4j", "build", "--target", "codex", "--host=darwin-arm64", "--output", "dist/codex", "--all", "--json"}, command: cli.CommandBuild, typeOf: cli.BuildRequest{}},
		{name: "dry-run install", argv: []string{"/usr/local/bin/ai4j", "install", "--ref", "tag", "--target", "claude", "--scope", "user", "--all", "--dry-run", "--json"}, command: cli.CommandInstall, typeOf: cli.InstallRequest{}},
		{name: "install", argv: []string{"ai4j", "install", "--target", "claude", "--scope", "user", "--all", "--expected-commit", commitOID, "--yes", "--json"}, command: cli.CommandInstall, typeOf: cli.InstallRequest{}},
		{name: "dry-run update", argv: []string{"ai4j", "update", "installation-001", "--dry-run", "--json"}, command: cli.CommandUpdate, typeOf: cli.UpdateRequest{}},
		{name: "update", argv: []string{"ai4j", "update", "installation-001", "--yes", "--expected-commit=" + commitOID}, command: cli.CommandUpdate, typeOf: cli.UpdateRequest{}},
		{name: "list", argv: []string{"ai4j", "list", "--target", "claude", "--scope=user", "--json"}, command: cli.CommandList, typeOf: cli.ListRequest{}},
		{name: "status", argv: []string{"ai4j", "status", "installation-001", "--json"}, command: cli.CommandStatus, typeOf: cli.StatusRequest{}},
		{name: "dry-run uninstall", argv: []string{"ai4j", "uninstall", "installation-001", "--dry-run"}, command: cli.CommandUninstall, typeOf: cli.UninstallRequest{}},
		{name: "uninstall", argv: []string{"ai4j", "uninstall", "installation-001", "--yes"}, command: cli.CommandUninstall, typeOf: cli.UninstallRequest{}},
		{name: "version", argv: []string{"ai4j", "version", "--json"}, command: cli.CommandVersion, typeOf: cli.VersionRequest{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request, err := parser.Parse(test.argv)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if request.Command() != test.command || reflect.TypeOf(request) != reflect.TypeOf(test.typeOf) {
				t.Fatalf("request = %T/%q, want %T/%q", request, request.Command(), test.typeOf, test.command)
			}
		})
	}
}

func TestParserAcceptsCompleteLifecycleGrammar(t *testing.T) {
	t.Parallel()
	parser := cli.NewParser("darwin")
	digest := strings.Repeat("a", 64)
	tests := []struct {
		command cli.Command
		argv    []string
	}{
		{cli.CommandValidate, []string{"ai4j", "validate", "--source", ".", "--target", "claude", "--allow-dirty", "--json"}},
		{cli.CommandBuild, []string{"ai4j", "build", "--source", ".", "--target", "codex", "--host", "windows-amd64", "--output", "dist", "--bundle", "default", "--allow-dirty"}},
		{cli.CommandInstall, []string{"ai4j", "install", "--repo", "alx4j/ai4j", "--target", "claude", "--scope", "user", "--all", "--dry-run"}},
		{cli.CommandInstall, []string{"ai4j", "install", "--source", ".", "--target", "codex", "--scope", "project-local", "--project", ".", "--asset", "review-skill", "--expected-source-digest", digest, "--allow-dirty", "--yes"}},
		{cli.CommandInstall, []string{"ai4j", "install", "--installation", "installation-001", "--allow-dirty", "--yes"}},
		{cli.CommandUpdate, []string{"ai4j", "update", "installation-001", "--repo", "alx4j/ai4j", "--conflict-policy", "keep", "--dry-run"}},
		{cli.CommandUpdate, []string{"ai4j", "update", "installation-001", "--expected-source-digest", digest, "--conflict-policy", "replace-owned", "--yes"}},
		{cli.CommandSync, []string{"ai4j", "sync", "installation-001", "--bundle", "default", "--conflict-policy", "fail", "--dry-run"}},
		{cli.CommandSync, []string{"ai4j", "sync", "installation-001", "--all", "--expected-source-digest", digest, "--yes"}},
		{cli.CommandStatus, []string{"ai4j", "status", "installation-001", "--json"}},
		{cli.CommandDoctor, []string{"ai4j", "doctor", "installation-001", "--test-mcp", "server-id", "--yes"}},
		{cli.CommandRollback, []string{"ai4j", "rollback", "installation-001", "--operation", "operation-001", "--conflict-policy", "fail", "--dry-run"}},
		{cli.CommandRollback, []string{"ai4j", "rollback", "installation-001", "--conflict-policy", "interactive", "--yes"}},
		{cli.CommandUninstall, []string{"ai4j", "uninstall", "installation-001", "--conflict-policy", "keep", "--dry-run"}},
		{cli.CommandUninstall, []string{"ai4j", "uninstall", "installation-001", "--conflict-policy", "replace-owned", "--yes"}},
		{cli.CommandHistory, []string{"ai4j", "history", "installation-001"}},
		{cli.CommandHistoryPurge, []string{"ai4j", "history", "purge", "installation-001", "--expired", "--dry-run"}},
		{cli.CommandHistoryPurge, []string{"ai4j", "history", "purge", "installation-001", "--operation", "operation-001", "--yes", "--json"}},
	}
	for _, test := range tests {
		request, err := parser.Parse(test.argv)
		if err != nil {
			t.Fatalf("Parse(%q) error = %v", test.argv, err)
		}
		if request.Command() != test.command {
			t.Fatalf("Parse(%q) command = %s, want %s", test.argv, request.Command(), test.command)
		}
	}
}

func TestParserRejectsLifecycleMutualExclusions(t *testing.T) {
	t.Parallel()
	parser := cli.NewParser("darwin")
	digest := strings.Repeat("a", 64)
	tests := [][]string{
		{"ai4j", "validate", "--repo", "alx4j/ai4j", "--source", ".", "--target", "claude"},
		{"ai4j", "build", "--target", "claude", "--host", "darwin-arm64", "--output", "dist", "--all", "--allow-dirty"},
		{"ai4j", "install", "--installation", "installation-001", "--target", "claude", "--yes"},
		{"ai4j", "install", "--source", ".", "--target", "claude", "--scope", "user", "--all", "--expected-commit", commitOID},
		{"ai4j", "install", "--target", "claude", "--scope", "user", "--all", "--expected-source-digest", digest},
		{"ai4j", "sync", "installation-001", "--all", "--asset", "skill-id"},
		{"ai4j", "rollback", "installation-001", "--conflict-policy", "interactive", "--dry-run"},
		{"ai4j", "rollback", "installation-001", "--conflict-policy", "interactive", "--json"},
		{"ai4j", "install", "--dry-run", "--yes"},
		{"ai4j", "update", "--dry-run", "--expected-commit", commitOID},
		{"ai4j", "sync", "installation-001", "--dry-run", "--expected-source-digest", digest},
		{"ai4j", "history", "purge", "installation-001", "--expired", "--all", "--yes"},
	}
	for _, argv := range tests {
		if _, err := parser.Parse(argv); err == nil {
			t.Fatalf("Parse(%q) succeeded", argv)
		}
	}
}

func TestParserPreservesTypedOptions(t *testing.T) {
	t.Parallel()

	parser := cli.NewParser("darwin")
	request, err := parser.Parse([]string{"ai4j", "install", "--ref", "main", "--repo=alx4j/ai4j", "--target", "claude", "--scope", "user", "--all", "--yes", "--expected-commit", commitOID, "--json"})
	if err != nil {
		t.Fatal(err)
	}
	install := request.(cli.InstallRequest)
	commit, present := install.ExpectedCommit()
	if install.Source().Repository() != "alx4j/ai4j" || !install.Source().HasRepository() || install.Source().Reference() != "main" || !install.Source().HasReference() ||
		!install.Approved() || install.OutputMode() != cli.OutputJSON || !present || commit.String() != commitOID {
		t.Fatalf("install request lost options: %#v", install)
	}
	statusRequest, err := parser.Parse([]string{"ai4j", "status", "installation-001"})
	if err != nil || statusRequest.(cli.StatusRequest).InstallationID().String() != "installation-001" {
		t.Fatalf("status request = %#v, error = %v", statusRequest, err)
	}
	listRequest, err := parser.Parse([]string{"ai4j", "list", "--target", "codex", "--scope", "project-local"})
	if err != nil {
		t.Fatal(err)
	}
	list := listRequest.(cli.ListRequest)
	if !list.HasTarget() || list.Target() != cli.BuildTargetCodex || !list.HasScope() || list.Scope() != cli.ScopeProjectLocal {
		t.Fatalf("list request lost filters: %#v", list)
	}
	buildRequest, err := parser.Parse([]string{"ai4j", "build", "--repo", "alx4j/ai4j", "--ref", "main", "--target", "codex", "--host", "darwin-arm64", "--output", "dist/codex", "--all", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	build := buildRequest.(cli.BuildRequest)
	if build.Source().Repository() != "alx4j/ai4j" || build.Source().Reference() != "main" || build.Target() != cli.BuildTargetCodex ||
		build.Host() != cli.BuildHostDarwinARM64 || build.Output() != "dist/codex" || !build.SelectAll() || build.OutputMode() != cli.OutputJSON {
		t.Fatalf("build request lost options: %#v", build)
	}
	selectedRequest, err := parser.Parse([]string{"ai4j", "build", "--target", "claude", "--host", "darwin-arm64", "--output", "dist/selected", "--asset", "review-checklist", "--bundle", "default", "--asset", "check-diff"})
	if err != nil {
		t.Fatal(err)
	}
	selected := selectedRequest.(cli.BuildRequest)
	if selected.SelectAll() || !slices.Equal(selected.Assets(), []string{"review-checklist", "check-diff"}) || !slices.Equal(selected.Bundles(), []string{"default"}) {
		t.Fatalf("selected build request lost options: %#v", selected)
	}
	initRequest, err := parser.Parse([]string{"ai4j", "init", "--output", "new-toolkit", "--target", "codex", "--target", "claude", "--examples", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	init := initRequest.(cli.InitRequest)
	if !slices.Equal(init.Targets(), []cli.BuildTarget{cli.BuildTargetCodex, cli.BuildTargetClaude}) || init.Output() != "new-toolkit" || !init.Examples() || init.OutputMode() != cli.OutputJSON {
		t.Fatalf("init request lost options: %#v", init)
	}
}

func TestParserPreservesDryRunOnEveryModifyingCommand(t *testing.T) {
	t.Parallel()

	parser := cli.NewParser("darwin")
	tests := []struct {
		argv   []string
		dryRun func(cli.Request) bool
	}{
		{[]string{"ai4j", "install", "--target", "claude", "--scope", "user", "--all", "--dry-run"}, func(request cli.Request) bool { return request.(cli.InstallRequest).DryRun() }},
		{[]string{"ai4j", "update", "installation-001", "--dry-run"}, func(request cli.Request) bool { return request.(cli.UpdateRequest).DryRun() }},
		{[]string{"ai4j", "sync", "installation-001", "--all", "--dry-run"}, func(request cli.Request) bool { return request.(cli.SyncRequest).DryRun() }},
		{[]string{"ai4j", "rollback", "installation-001", "--dry-run"}, func(request cli.Request) bool { return request.(cli.RollbackRequest).DryRun() }},
		{[]string{"ai4j", "uninstall", "installation-001", "--dry-run"}, func(request cli.Request) bool { return request.(cli.UninstallRequest).DryRun() }},
		{[]string{"ai4j", "history", "purge", "installation-001", "--all", "--dry-run"}, func(request cli.Request) bool { return request.(cli.HistoryPurgeRequest).DryRun() }},
	}
	for _, test := range tests {
		request, err := parser.Parse(test.argv)
		if err != nil {
			t.Fatalf("Parse(%q) error = %v", test.argv, err)
		}
		if !test.dryRun(request) {
			t.Fatalf("Parse(%q) did not preserve --dry-run", test.argv)
		}
	}
}

func TestParserAcceptsInstallationIDImmediatelyAfterLifecycleCommand(t *testing.T) {
	t.Parallel()

	parser := cli.NewParser("darwin")
	tests := []struct {
		argv []string
		id   func(cli.Request) string
	}{
		{[]string{"ai4j", "update", "installation-001", "--dry-run"}, func(request cli.Request) string { return request.(cli.UpdateRequest).InstallationID().String() }},
		{[]string{"ai4j", "sync", "installation-001", "--all", "--dry-run"}, func(request cli.Request) string { return request.(cli.SyncRequest).InstallationID().String() }},
		{[]string{"ai4j", "doctor", "installation-001"}, func(request cli.Request) string { return request.(cli.DoctorRequest).InstallationID().String() }},
		{[]string{"ai4j", "rollback", "installation-001", "--dry-run"}, func(request cli.Request) string { return request.(cli.RollbackRequest).InstallationID().String() }},
		{[]string{"ai4j", "uninstall", "installation-001", "--dry-run"}, func(request cli.Request) string { return request.(cli.UninstallRequest).InstallationID().String() }},
		{[]string{"ai4j", "history", "installation-001"}, func(request cli.Request) string { return request.(cli.HistoryRequest).InstallationID().String() }},
		{[]string{"ai4j", "history", "purge", "installation-001", "--all", "--dry-run"}, func(request cli.Request) string { return request.(cli.HistoryPurgeRequest).InstallationID().String() }},
	}
	for _, test := range tests {
		request, err := parser.Parse(test.argv)
		if err != nil {
			t.Fatalf("Parse(%q) error = %v", test.argv, err)
		}
		if got := test.id(request); got != "installation-001" {
			t.Fatalf("Parse(%q) installation = %q", test.argv, got)
		}
	}
}

func TestParserRejectsNonCanonicalInstallationArgumentForms(t *testing.T) {
	t.Parallel()

	parser := cli.NewParser("darwin")
	tests := []struct {
		argv   []string
		issue  cli.UsageIssue
		option string
	}{
		{[]string{"ai4j", "update", "INVALID!"}, cli.UsageInvalidOptionValue, "installation"},
		{[]string{"ai4j", "sync", "--installation", "installation-001", "--all"}, cli.UsageInapplicableOption, "installation"},
		{[]string{"ai4j", "status", "--installation", "installation-001"}, cli.UsageInapplicableOption, "installation"},
		{[]string{"ai4j", "doctor", "--json", "installation-001"}, cli.UsageUnexpectedArgument, ""},
		{[]string{"ai4j", "rollback", "installation-001", "installation-002"}, cli.UsageUnexpectedArgument, ""},
		{[]string{"ai4j", "history", "purge", "--all", "installation-001"}, cli.UsageUnexpectedArgument, ""},
	}
	for _, test := range tests {
		_, err := parser.Parse(test.argv)
		var usage *cli.UsageError
		if !errors.As(err, &usage) || usage.Issue() != test.issue || usage.Option() != test.option {
			t.Fatalf("Parse(%q) = %v, want %s for %q", test.argv, err, test.issue, test.option)
		}
	}
}

func TestParserRequiresInstallationArgumentForLifecycleCommands(t *testing.T) {
	t.Parallel()

	parser := cli.NewParser("darwin")
	tests := [][]string{
		{"ai4j", "update"},
		{"ai4j", "sync", "--all"},
		{"ai4j", "status"},
		{"ai4j", "doctor"},
		{"ai4j", "rollback"},
		{"ai4j", "uninstall"},
		{"ai4j", "history"},
		{"ai4j", "history", "purge", "--all"},
	}
	for _, argv := range tests {
		_, err := parser.Parse(argv)
		var usage *cli.UsageError
		if !errors.As(err, &usage) || usage.Issue() != cli.UsageMissingOptionValue || usage.Option() != "installation" {
			t.Fatalf("Parse(%q) = %v, want missing installation", argv, err)
		}
	}
}

func TestParserBuildReportsMissingRequiredOptionsInCanonicalOrder(t *testing.T) {
	t.Parallel()
	parser := cli.NewParser("darwin")
	tests := []struct {
		argv   []string
		option string
	}{
		{argv: []string{"ai4j", "build"}, option: "target"},
		{argv: []string{"ai4j", "build", "--target", "claude"}, option: "host"},
		{argv: []string{"ai4j", "build", "--target", "claude", "--host", "darwin-arm64"}, option: "output"},
		{argv: []string{"ai4j", "build", "--target", "claude", "--host", "darwin-arm64", "--output", "dist"}, option: "all"},
	}
	for _, test := range tests {
		_, err := parser.Parse(test.argv)
		var usage *cli.UsageError
		if !errors.As(err, &usage) || usage.Issue() != cli.UsageMissingOptionValue || usage.Option() != test.option {
			t.Fatalf("Parse(%q) = %v, want missing %s", test.argv, err, test.option)
		}
	}
}

func TestParserValidateRequiresTarget(t *testing.T) {
	t.Parallel()
	parser := cli.NewParser("darwin")

	_, err := parser.Parse([]string{"ai4j", "validate"})
	var usage *cli.UsageError
	if !errors.As(err, &usage) || usage.Issue() != cli.UsageMissingOptionValue || usage.Option() != "target" {
		t.Fatalf("Parse() = %v, want missing target", err)
	}
}

func TestParserValidateRejectsMultipleTargets(t *testing.T) {
	t.Parallel()
	parser := cli.NewParser("darwin")

	_, err := parser.Parse([]string{"ai4j", "validate", "--target", "claude", "--target", "codex"})
	var usage *cli.UsageError
	if !errors.As(err, &usage) || usage.Issue() != cli.UsageInvalidOptionValue || usage.Option() != "target" {
		t.Fatalf("Parse() = %v, want invalid target", err)
	}
}

func TestParserPreservesSourceOptionPresence(t *testing.T) {
	t.Parallel()

	parser := cli.NewParser("darwin")
	tests := []struct {
		name       string
		argv       []string
		repository string
		hasRepo    bool
		reference  string
		hasRef     bool
	}{
		{name: "omitted", argv: []string{"ai4j", "validate", "--target", "claude"}},
		{name: "reference only", argv: []string{"ai4j", "validate", "--ref", "main", "--target", "claude"}, reference: "main", hasRef: true},
		{name: "repository only", argv: []string{"ai4j", "validate", "--repo", "alx4j/ai4j", "--target", "claude"}, repository: "alx4j/ai4j", hasRepo: true},
		{name: "both", argv: []string{"ai4j", "validate", "--repo=alx4j/ai4j", "--ref=main", "--target=claude"}, repository: "alx4j/ai4j", hasRepo: true, reference: "main", hasRef: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request, err := parser.Parse(test.argv)
			if err != nil {
				t.Fatal(err)
			}
			options := request.(cli.ValidateRequest).Source()
			if options.Repository() != test.repository || options.HasRepository() != test.hasRepo || options.Reference() != test.reference || options.HasReference() != test.hasRef {
				t.Fatalf("source options = %q/%v %q/%v", options.Repository(), options.HasRepository(), options.Reference(), options.HasReference())
			}
		})
	}
}

func TestParserAcceptsEveryApplicableOptionCombination(t *testing.T) {
	t.Parallel()

	type option struct {
		separate []string
		inline   []string
	}
	tests := []struct {
		name    string
		command []string
		options []option
	}{
		{name: "validate", command: []string{"validate", "--target", "claude"}, options: []option{{[]string{"--repo", "alx4j/ai4j"}, []string{"--repo=alx4j/ai4j"}}, {[]string{"--ref", "main"}, []string{"--ref=main"}}, {[]string{"--json"}, []string{"--json"}}}},
		{name: "install", command: []string{"install", "--target", "claude", "--scope", "user", "--all"}, options: []option{{[]string{"--repo", "alx4j/ai4j"}, []string{"--repo=alx4j/ai4j"}}, {[]string{"--ref", "main"}, []string{"--ref=main"}}, {[]string{"--expected-commit", commitOID}, []string{"--expected-commit=" + commitOID}}, {[]string{"--yes"}, []string{"--yes"}}, {[]string{"--json"}, []string{"--json"}}}},
		{name: "update", command: []string{"update", "installation-001"}, options: []option{{[]string{"--expected-commit", commitOID}, []string{"--expected-commit=" + commitOID}}, {[]string{"--yes"}, []string{"--yes"}}, {[]string{"--json"}, []string{"--json"}}}},
		{name: "list", command: []string{"list"}, options: []option{{[]string{"--target", "claude"}, []string{"--target=claude"}}, {[]string{"--scope", "user"}, []string{"--scope=user"}}, {[]string{"--json"}, []string{"--json"}}}},
		{name: "status", command: []string{"status", "installation-001"}, options: []option{{[]string{"--json"}, []string{"--json"}}}},
		{name: "uninstall", command: []string{"uninstall", "installation-001"}, options: []option{{[]string{"--yes"}, []string{"--yes"}}, {[]string{"--json"}, []string{"--json"}}}},
		{name: "version", command: []string{"version"}, options: []option{{[]string{"--json"}, []string{"--json"}}}},
	}
	parser := cli.NewParser("darwin")
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for mask := 0; mask < 1<<len(test.options); mask++ {
				for variant := 0; variant < 2; variant++ {
					selected := make([][]string, 0, len(test.options))
					for index, candidate := range test.options {
						if mask&(1<<index) == 0 {
							continue
						}
						if variant == 0 {
							selected = append(selected, candidate.separate)
						} else {
							selected = append(selected, candidate.inline)
						}
					}
					if variant == 1 {
						slices.Reverse(selected)
					}
					argv := append([]string{"ai4j"}, test.command...)
					for _, tokens := range selected {
						argv = append(argv, tokens...)
					}
					if _, err := parser.Parse(argv); err != nil {
						t.Fatalf("Parse(%q) error = %v", argv, err)
					}
				}
			}
		})
	}
}

func TestParserRejectsEveryNonCanonicalGrammarFamily(t *testing.T) {
	t.Parallel()

	parser := cli.NewParser("darwin")
	tests := []struct {
		name  string
		argv  []string
		issue cli.UsageIssue
		json  bool
	}{
		{name: "missing executable", argv: nil, issue: cli.UsageMissingExecutable},
		{name: "alternate executable", argv: []string{"toolkit", "version"}, issue: cli.UsageAlternateExecutable},
		{name: "case variant executable", argv: []string{"AI4J", "version"}, issue: cli.UsageAlternateExecutable},
		{name: "missing command", argv: []string{"ai4j"}, issue: cli.UsageMissingCommand},
		{name: "unknown command", argv: []string{"ai4j", "repair"}, issue: cli.UsageUnknownCommand},
		{name: "removed plan command", argv: []string{"ai4j", "plan", "--json"}, issue: cli.UsageUnknownCommand, json: true},
		{name: "removed plan command with arguments", argv: []string{"ai4j", "plan", "repair"}, issue: cli.UsageUnknownCommand},
		{name: "duplicate flag", argv: []string{"ai4j", "status", "--json", "--json"}, issue: cli.UsageDuplicateOption, json: true},
		{name: "duplicate value flag", argv: []string{"ai4j", "validate", "--ref=main", "--ref", "next"}, issue: cli.UsageDuplicateOption},
		{name: "missing value", argv: []string{"ai4j", "validate", "--repo"}, issue: cli.UsageMissingOptionValue},
		{name: "empty equals value", argv: []string{"ai4j", "validate", "--repo="}, issue: cli.UsageEmptyOptionValue},
		{name: "flag where value required", argv: []string{"ai4j", "validate", "--repo", "--json"}, issue: cli.UsageMissingOptionValue, json: true},
		{name: "boolean value", argv: []string{"ai4j", "install", "--yes=true"}, issue: cli.UsageUnexpectedOptionValue},
		{name: "unknown flag", argv: []string{"ai4j", "version", "--verbose"}, issue: cli.UsageUnknownOption},
		{name: "inapplicable flag", argv: []string{"ai4j", "update", "--target", "claude"}, issue: cli.UsageInapplicableOption},
		{name: "target flag", argv: []string{"ai4j", "status", "--target", "claude"}, issue: cli.UsageInapplicableOption},
		{name: "scope flag", argv: []string{"ai4j", "status", "--scope=user"}, issue: cli.UsageInapplicableOption},
		{name: "selection flag", argv: []string{"ai4j", "install", "--selection", "all"}, issue: cli.UsageInapplicableOption},
		{name: "force flag", argv: []string{"ai4j", "uninstall", "--force"}, issue: cli.UsageInapplicableOption},
		{name: "dry-run flag", argv: []string{"ai4j", "status", "--dry-run"}, issue: cli.UsageInapplicableOption},
		{name: "single dash", argv: []string{"ai4j", "version", "-json"}, issue: cli.UsageUnexpectedArgument},
		{name: "option terminator", argv: []string{"ai4j", "version", "--"}, issue: cli.UsageUnexpectedArgument},
		{name: "extra positional argument", argv: []string{"ai4j", "status", "installation-001", "extra"}, issue: cli.UsageUnexpectedArgument},
		{name: "removed status update flag", argv: []string{"ai4j", "status", "installation-001", "--check-updates"}, issue: cli.UsageUnknownOption},
		{name: "flag before command", argv: []string{"ai4j", "--json", "version"}, issue: cli.UsageMisplacedOption, json: true},
		{name: "short expected commit", argv: []string{"ai4j", "update", "--expected-commit", "0123456"}, issue: cli.UsageInvalidOptionValue},
		{name: "uppercase expected commit", argv: []string{"ai4j", "update", "--expected-commit", "0123456789ABCDEF0123456789ABCDEF01234567"}, issue: cli.UsageInvalidOptionValue},
		{name: "build all with asset", argv: []string{"ai4j", "build", "--target", "claude", "--host", "darwin-arm64", "--output", "dist", "--all", "--asset", "check-diff"}, issue: cli.UsageInvalidOptionValue},
		{name: "duplicate build asset", argv: []string{"ai4j", "build", "--target", "claude", "--host", "darwin-arm64", "--output", "dist", "--asset", "check-diff", "--asset", "check-diff"}, issue: cli.UsageInvalidOptionValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := parser.Parse(test.argv)
			var usage *cli.UsageError
			if !errors.As(err, &usage) {
				t.Fatalf("Parse() error = %v, want UsageError", err)
			}
			if usage.Issue() != test.issue || usage.JSONRequested() != test.json || !errors.Is(usage, fault.ErrInvalidInput) {
				t.Fatalf("usage = issue %q json %v error %v", usage.Issue(), usage.JSONRequested(), usage)
			}
		})
	}
}

func TestParserRejectsInvalidFormsForEveryCommandOption(t *testing.T) {
	t.Parallel()

	type option struct {
		name    string
		value   string
		boolean bool
	}
	tests := []struct {
		name    string
		command []string
		options []option
	}{
		{name: "validate", command: []string{"validate"}, options: []option{{name: "repo", value: "alx4j/ai4j"}, {name: "ref", value: "main"}, {name: "source", value: "."}, {name: "target", value: "claude"}, {name: "allow-dirty", boolean: true}, {name: "json", boolean: true}}},
		{name: "install", command: []string{"install"}, options: []option{{name: "repo", value: "alx4j/ai4j"}, {name: "ref", value: "main"}, {name: "source", value: "."}, {name: "installation", value: "installation-001"}, {name: "target", value: "claude"}, {name: "scope", value: "user"}, {name: "project", value: "."}, {name: "all", boolean: true}, {name: "asset", value: "skill-id"}, {name: "bundle", value: "bundle-id"}, {name: "allow-dirty", boolean: true}, {name: "expected-commit", value: commitOID}, {name: "expected-source-digest", value: strings.Repeat("a", 64)}, {name: "dry-run", boolean: true}, {name: "yes", boolean: true}, {name: "json", boolean: true}}},
		{name: "update", command: []string{"update", "installation-001"}, options: []option{{name: "repo", value: "alx4j/ai4j"}, {name: "ref", value: "main"}, {name: "allow-dirty", boolean: true}, {name: "expected-commit", value: commitOID}, {name: "expected-source-digest", value: strings.Repeat("a", 64)}, {name: "conflict-policy", value: "fail"}, {name: "dry-run", boolean: true}, {name: "yes", boolean: true}, {name: "json", boolean: true}}},
		{name: "sync", command: []string{"sync", "installation-001"}, options: []option{{name: "all", boolean: true}, {name: "asset", value: "skill-id"}, {name: "bundle", value: "bundle-id"}, {name: "allow-dirty", boolean: true}, {name: "expected-source-digest", value: strings.Repeat("a", 64)}, {name: "conflict-policy", value: "fail"}, {name: "dry-run", boolean: true}, {name: "yes", boolean: true}, {name: "json", boolean: true}}},
		{name: "list", command: []string{"list"}, options: []option{{name: "target", value: "claude"}, {name: "scope", value: "user"}, {name: "json", boolean: true}}},
		{name: "status", command: []string{"status", "installation-001"}, options: []option{{name: "json", boolean: true}}},
		{name: "doctor", command: []string{"doctor", "installation-001"}, options: []option{{name: "test-mcp", value: "server-id"}, {name: "yes", boolean: true}, {name: "json", boolean: true}}},
		{name: "rollback", command: []string{"rollback", "installation-001"}, options: []option{{name: "operation", value: "operation-001"}, {name: "conflict-policy", value: "fail"}, {name: "dry-run", boolean: true}, {name: "yes", boolean: true}, {name: "json", boolean: true}}},
		{name: "uninstall", command: []string{"uninstall", "installation-001"}, options: []option{{name: "conflict-policy", value: "fail"}, {name: "dry-run", boolean: true}, {name: "yes", boolean: true}, {name: "json", boolean: true}}},
		{name: "history", command: []string{"history", "installation-001"}, options: []option{{name: "json", boolean: true}}},
		{name: "history purge", command: []string{"history", "purge", "installation-001"}, options: []option{{name: "operation", value: "operation-001"}, {name: "expired", boolean: true}, {name: "all", boolean: true}, {name: "dry-run", boolean: true}, {name: "yes", boolean: true}, {name: "json", boolean: true}}},
		{name: "version", command: []string{"version"}, options: []option{{name: "json", boolean: true}}},
	}
	parser := cli.NewParser("darwin")
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			allowed := make(map[string]struct{}, len(test.options))
			for _, candidate := range test.options {
				allowed[candidate.name] = struct{}{}
				token := "--" + candidate.name
				if !candidate.boolean {
					token += "=" + candidate.value
				}
				duplicateIssue := cli.UsageDuplicateOption
				if candidate.name == "asset" || candidate.name == "bundle" || test.name == "validate" && candidate.name == "target" {
					duplicateIssue = cli.UsageInvalidOptionValue
				}
				assertUsageIssue(t, parser, appendCommand(test.command, token, token), duplicateIssue)
				if candidate.boolean {
					assertUsageIssue(t, parser, appendCommand(test.command, token+"=true"), cli.UsageUnexpectedOptionValue)
				} else {
					assertUsageIssue(t, parser, appendCommand(test.command, "--"+candidate.name), cli.UsageMissingOptionValue)
					assertUsageIssue(t, parser, appendCommand(test.command, "--"+candidate.name+"="), cli.UsageEmptyOptionValue)
				}
			}
			for _, name := range []string{"repo", "ref", "source", "expected-commit", "expected-source-digest", "yes", "json", "allow-dirty", "target", "host", "output", "all", "asset", "bundle", "examples", "scope", "project", "installation", "conflict-policy", "operation", "test-mcp", "expired", "selection", "force", "dry-run"} {
				if _, ok := allowed[name]; ok {
					continue
				}
				assertUsageIssue(t, parser, appendCommand(test.command, "--"+name), cli.UsageInapplicableOption)
			}
			assertUsageIssue(t, parser, appendCommand(test.command, "--future-option"), cli.UsageUnknownOption)
		})
	}
}

func appendCommand(command []string, arguments ...string) []string {
	argv := append([]string{"ai4j"}, command...)
	return append(argv, arguments...)
}

func assertUsageIssue(t *testing.T, parser cli.Parser, argv []string, want cli.UsageIssue) {
	t.Helper()
	_, err := parser.Parse(argv)
	var usage *cli.UsageError
	if !errors.As(err, &usage) {
		t.Fatalf("Parse(%q) error = %v, want UsageError", argv, err)
	}
	if usage.Issue() != want {
		t.Fatalf("Parse(%q) issue = %q, want %q", argv, usage.Issue(), want)
	}
}

func TestWindowsExecutableExtensionIsPlatformBound(t *testing.T) {
	t.Parallel()

	if _, err := cli.NewParser("windows").Parse([]string{`C:\bin\ai4j.exe`, "version"}); err != nil {
		t.Fatalf("Windows ai4j.exe error = %v", err)
	}
	if _, err := cli.NewParser("darwin").Parse([]string{"ai4j.exe", "version"}); err == nil {
		t.Fatal("Darwin parser accepted ai4j.exe")
	}
	if _, err := cli.NewParser("windows").Parse([]string{"ai4j", "version"}); err == nil {
		t.Fatal("Windows parser accepted extensionless alternate executable")
	}
}

func TestUsageErrorsDoNotRetainOrRenderUnknownArguments(t *testing.T) {
	t.Parallel()

	const canary = "secret-canary"
	unknown := "--" + canary + strings.Repeat("x", 4096)
	_, err := cli.NewParser("darwin").Parse([]string{"ai4j", "version", unknown})
	var usage *cli.UsageError
	if !errors.As(err, &usage) {
		t.Fatalf("Parse() error = %v, want UsageError", err)
	}
	if usage.Option() != "" || strings.Contains(usage.Error(), canary) || len(usage.Error()) > 128 {
		t.Fatalf("usage retained or rendered unknown input: option=%q error=%q", usage.Option(), usage.Error())
	}
}
