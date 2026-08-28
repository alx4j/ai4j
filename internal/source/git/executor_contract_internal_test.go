package git

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/domain"
	gitremote "github.com/alx4j/ai4j/internal/source/gitremote"
)

const (
	testObjectA = "0123456789abcdef0123456789abcdef01234567"
	testObjectB = "89abcdef0123456789abcdef0123456789abcdef"
)

func TestBudgetsAndLimitsAreClosed(t *testing.T) {
	t.Parallel()

	execution := DefaultExecutionBudget()
	if !execution.Valid() || execution.Aggregate() != 5*time.Minute || execution.LocalMaximum() != 15*time.Second ||
		execution.NetworkMaximum() != time.Minute || execution.TerminationGrace() != 2*time.Second {
		t.Fatalf("execution budget = %#v", execution)
	}
	if (ExecutionBudget{}).Valid() || (ExecutionBudget{aggregate: 5 * time.Minute}).Valid() {
		t.Fatal("partial execution budget is valid")
	}
	workspace := DefaultWorkspaceBudget()
	if !workspace.Valid() || workspace.DeclaredMaximumBytes() != 1<<30 || workspace.HeadroomBytes() != 256<<20 ||
		workspace.Enforcement() != WorkspaceEnforcementAdvisory {
		t.Fatalf("workspace budget = %#v", workspace)
	}
	if (WorkspaceBudget{}).Valid() || WorkspaceEnforcement("hard_quota").Valid() {
		t.Fatal("unknown workspace budget state is valid")
	}
	preflight, ok := workspace.PreflightBytes()
	if !ok || preflight != (1<<30)+(256<<20) || !workspace.AllowsObservedUsage(1<<30) ||
		workspace.AllowsObservedUsage((1<<30)+1) {
		t.Fatalf("workspace arithmetic = %d, %t", preflight, ok)
	}
	if MaximumInventoryPathCount != 1<<14 || MaximumInventoryPathBytes != 14<<20 ||
		MaximumBlobBytes != 64<<20 || MaximumValidatedTreeBytes != 512<<20 ||
		MaximumAttributeBatchPaths != 128 || MaximumAttributeBatchBytes != 128<<10 ||
		MaximumCheckoutAttributes != 6 || MaximumCommandArguments != 256 || MaximumCommandArgumentBytes != 4096 {
		t.Fatal("closed source limits drifted")
	}
}

func TestClosedEnumsRejectZeroAndUnknownValues(t *testing.T) {
	t.Parallel()

	operations := []Operation{
		OperationInitialize, OperationProbeExecPath, OperationAuditConfig, OperationEnumerateRefs, OperationFetch,
		OperationObjectType, OperationPeelCommit, OperationCommitTree, OperationListTree,
		OperationReadTree, OperationCheckAttributes, OperationCheckoutIndex, OperationCheckoutDetached, OperationListIndex,
		OperationStatus, OperationIsAncestor,
	}
	for _, operation := range operations {
		if !operation.Valid() {
			t.Errorf("operation %q is invalid", operation)
		}
	}
	for _, value := range []Operation{"", "clone", "push"} {
		if value.Valid() {
			t.Errorf("operation %q is valid", value)
		}
	}
	for _, code := range []FailureCode{
		FailureInvalidOperation, FailureUnsupportedRuntime, FailureRepositoryAuthorityConflict,
		FailureAuthenticationProjectionUnavailable, FailureTransportHelperUnavailable, FailurePolicyRejected,
		FailureAccess, FailureCommand, FailureTimedOut, FailureCancelled, FailureOutputLimit,
		FailureMalformedProtocol, FailureResourceLimit, FailureRepositoryConflict,
		FailureReferenceNotFound, FailureReferenceAmbiguous, FailureDefaultBranchUnavailable,
	} {
		if !code.Valid() || !errors.Is(NewExecutorError(OperationFetch, code), NewExecutorError(OperationFetch, code)) {
			t.Errorf("failure code %q is incoherent", code)
		}
	}
	if FailureCode("").Valid() || FailureCode("native_error").Valid() ||
		!errors.Is(NewExecutorError(OperationFetch, "unknown"), ErrExecutorContract) ||
		!errors.Is(NewExecutorError("unknown", FailureCommand), ErrExecutorContract) {
		t.Fatal("unknown failure code accepted")
	}
	var typed ExecutorError
	fault := NewExecutorError(OperationFetch, FailureCommand)
	if !errors.As(fault, &typed) || typed.Operation() != OperationFetch || typed.Code() != FailureCommand ||
		errors.Is(fault, NewExecutorError(OperationEnumerateRefs, FailureCommand)) ||
		fault.Error() != "Git source operation fetch failed: command_failed" {
		t.Fatalf("typed fault = %#v / %q", typed, fault)
	}
	for _, purpose := range []RuntimeChildPurpose{
		ChildGitRemoteHTTPSDriver, ChildRemoteHTTPS, ChildGitFetchPack, ChildGitIndexPack,
		ChildGitRevList, ChildCredentialShell, ChildCredential, ChildSSHWrapper, ChildSSHClient,
	} {
		if !purpose.Valid() {
			t.Errorf("child purpose %q is invalid", purpose)
		}
	}
	if RuntimeChildPurpose("").Valid() || RuntimeChildPurpose("shell").Valid() {
		t.Fatal("unknown child purpose accepted")
	}
}

func TestRuntimeProfileBindsExactAppleGitFacts(t *testing.T) {
	t.Parallel()

	profile := AppleGit154DarwinARM64Profile()
	if !profile.Valid() || profile.ID() != "apple_git_154_3_macos15_arm64" ||
		profile.GitVersion() != "2.39.5 (Apple Git-154.3)" || profile.HostOS() != "darwin" ||
		profile.HostArchitecture() != "arm64" || profile.HostMajorVersion() != 15 {
		t.Fatalf("profile = %#v", profile)
	}
	changed := profile
	changed.gitVersion = "2.39.5"
	if changed.Valid() || (RuntimeProfile{}).Valid() {
		t.Fatal("partial or changed runtime profile is valid")
	}
}

func TestRuntimeProfileChildAllowlistDistinguishesHTTPSAndSSH(t *testing.T) {
	t.Parallel()

	profile := AppleGit154DarwinARM64Profile()
	httpsRequest := mustResolutionRequest(t, "https://github.com/alx4j/ai4j.git", "", false)
	sshRequest := mustResolutionRequest(t, "git@github.com:alx4j/ai4j.git", "", false)
	anonymous := mustAuthentication(t, httpsRequest, AuthenticationAnonymousHTTPS)
	keychain := mustAuthentication(t, httpsRequest, AuthenticationCredentialHelperHTTPS)
	ssh := mustAuthentication(t, sshRequest, AuthenticationDefaultKeySSH)

	tests := []struct {
		operation Operation
		auth      AuthenticationProjection
		want      []RuntimeChildPurpose
	}{
		{OperationEnumerateRefs, anonymous, []RuntimeChildPurpose{ChildGitRemoteHTTPSDriver, ChildRemoteHTTPS}},
		{OperationEnumerateRefs, keychain, []RuntimeChildPurpose{ChildGitRemoteHTTPSDriver, ChildRemoteHTTPS, ChildCredentialShell, ChildCredential}},
		{OperationEnumerateRefs, ssh, []RuntimeChildPurpose{ChildSSHWrapper, ChildSSHClient}},
		{OperationFetch, anonymous, []RuntimeChildPurpose{ChildGitRevList, ChildGitRemoteHTTPSDriver, ChildRemoteHTTPS, ChildGitFetchPack, ChildGitIndexPack}},
		{OperationFetch, keychain, []RuntimeChildPurpose{ChildGitRevList, ChildGitRemoteHTTPSDriver, ChildRemoteHTTPS, ChildGitFetchPack, ChildGitIndexPack, ChildCredentialShell, ChildCredential}},
		{OperationFetch, ssh, []RuntimeChildPurpose{ChildGitRevList, ChildSSHWrapper, ChildSSHClient, ChildGitIndexPack}},
		{OperationListTree, AuthenticationProjection{}, []RuntimeChildPurpose{}},
	}
	for _, test := range tests {
		got, err := profile.AllowedChildPurposes(test.operation, test.auth)
		if err != nil || !reflect.DeepEqual(got, test.want) {
			t.Errorf("AllowedChildPurposes(%s, %s) = %#v, %v", test.operation, test.auth.mode, got, err)
			continue
		}
		if len(got) > 0 {
			got[0] = "changed"
			repeated, _ := profile.AllowedChildPurposes(test.operation, test.auth)
			if reflect.DeepEqual(got, repeated) {
				t.Fatal("child allowlist aliases profile data")
			}
		}
	}
	sshFetch, _ := profile.AllowedChildPurposes(OperationFetch, ssh)
	if slices.Contains(sshFetch, ChildGitFetchPack) {
		t.Fatal("SSH manifest incorrectly admits a fetch-pack child")
	}
	if _, err := profile.AllowedChildPurposes(OperationFetch, AuthenticationProjection{}); !errors.Is(err, ErrExecutorContract) {
		t.Fatalf("missing auth error = %v", err)
	}
	if _, err := (RuntimeProfile{}).AllowedChildPurposes(OperationFetch, anonymous); !errors.Is(err, ErrExecutorContract) {
		t.Fatalf("invalid profile error = %v", err)
	}
}

func TestAuthenticationProjectionIsTransportExactAndRedacted(t *testing.T) {
	t.Parallel()

	repository := mustRepository(t, "github.com/private/canary-repository")
	for _, test := range []struct {
		transport domain.GitTransport
		mode      AuthenticationMode
	}{
		{domain.HTTPSGitTransport(), AuthenticationAnonymousHTTPS},
		{domain.HTTPSGitTransport(), AuthenticationCredentialHelperHTTPS},
		{domain.SSHGitTransport(), AuthenticationDefaultKeySSH},
	} {
		projection, err := NewAuthenticationProjection(repository, test.transport, test.mode)
		if err != nil || !projection.Valid() || projection.Repository() != repository ||
			projection.Transport() != test.transport || projection.Mode() != test.mode {
			t.Fatalf("projection = %#v, %v", projection, err)
		}
		for _, rendered := range []string{fmt.Sprintf("%v", projection), fmt.Sprintf("%+v", projection), fmt.Sprintf("%#v", projection)} {
			if strings.Contains(rendered, "canary") || rendered != "<git-authentication:redacted>" {
				t.Fatalf("rendered projection = %q", rendered)
			}
		}
		encoded, marshalErr := json.Marshal(projection)
		if marshalErr != nil || strings.Contains(string(encoded), "canary") {
			t.Fatalf("JSON = %s, %v", encoded, marshalErr)
		}
	}
	for _, test := range []struct {
		transport domain.GitTransport
		mode      AuthenticationMode
	}{
		{domain.SSHGitTransport(), AuthenticationAnonymousHTTPS},
		{domain.SSHGitTransport(), AuthenticationCredentialHelperHTTPS},
		{domain.HTTPSGitTransport(), AuthenticationDefaultKeySSH},
		{domain.HTTPSGitTransport(), "agent_ssh"},
	} {
		if _, err := NewAuthenticationProjection(repository, test.transport, test.mode); !errors.Is(err, ErrExecutorContract) {
			t.Errorf("mismatched authentication error = %v", err)
		}
	}
}

func TestGitHardenedEnvironmentIsExactAndDefensivelyCopied(t *testing.T) {
	t.Parallel()

	want := [][2]string{
		{"GIT_ATTR_NOSYSTEM", "1"}, {"GIT_CONFIG_GLOBAL", "/dev/null"}, {"GIT_CONFIG_NOSYSTEM", "1"},
		{"GIT_LFS_SKIP_SMUDGE", "1"}, {"GIT_OPTIONAL_LOCKS", "0"}, {"GIT_PROTOCOL_FROM_USER", "0"},
		{"GIT_TERMINAL_PROMPT", "0"}, {"LANG", "C"}, {"LC_ALL", "C"}, {"PATH", "/dev/null"},
	}
	first := GitHardenedEnvironment()
	if GitHardenedEnvironmentProfile().String() != "git_hardened" || len(first) != len(want) {
		t.Fatalf("profile/env = %q/%d", GitHardenedEnvironmentProfile().String(), len(first))
	}
	for index, expected := range want {
		if first[index].Name != expected[0] || first[index].Value != expected[1] {
			t.Fatalf("environment[%d] = %#v", index, first[index])
		}
	}
	first[0].Value = "changed"
	if GitHardenedEnvironment()[0].Value != "1" {
		t.Fatal("environment result aliases built-in policy")
	}
}

func TestCommandArgvIsExactAndDefensivelyCopied(t *testing.T) {
	t.Parallel()

	request := mustResolutionRequest(t, "https://github.com/alx4j/ai4j.git", "", false)
	auth := mustAuthentication(t, request, AuthenticationAnonymousHTTPS)
	command, err := NewEnumerateReferencesCommand(request, auth)
	if err != nil || !command.Valid() {
		t.Fatalf("command = %#v, %v", command, err)
	}
	wantTail := []string{"-c", "protocol.https.allow=always", "ls-remote", "--quiet", "--symref", "--",
		"https://github.com/alx4j/ai4j.git", "HEAD", "refs/heads/*", "refs/tags/*"}
	want := append(testCommonArguments(true), wantTail...)
	if got := command.Arguments(); !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %#v", got)
	}
	if command.Operation() != OperationEnumerateRefs || command.WorkingDirectory() != GitDirectory ||
		command.OutputGrammar() != RemoteOutputGrammar || command.TimeoutMaximum() != NetworkCommandTimeout ||
		command.OutputLimitBytes() != remoteOutputLimit {
		t.Fatalf("command facts = %#v", command)
	}
	arguments := command.Arguments()
	arguments[0] = "push"
	if command.Arguments()[0] != "--no-pager" {
		t.Fatal("arguments alias command")
	}
	environment := command.Environment()
	environment[0].Value = "changed"
	if command.Environment()[0].Value != "1" {
		t.Fatal("environment aliases command policy")
	}
	for _, rendered := range []string{fmt.Sprintf("%v", command), fmt.Sprintf("%+v", command), fmt.Sprintf("%#v", command)} {
		if strings.Contains(rendered, "github.com") || rendered != "<git-command:redacted>" {
			t.Fatalf("command render = %q", rendered)
		}
	}
}

func TestCommandCoherenceRejectsPartialAndCrossOperationShapes(t *testing.T) {
	t.Parallel()

	if (Command{}).Valid() {
		t.Fatal("zero command is valid")
	}
	valid := NewInitializeCommand()
	changed := valid
	changed.timeout = NetworkCommandTimeout
	if changed.Valid() {
		t.Fatal("cross-operation timeout is valid")
	}
	changed = valid
	changed.arguments = []string{"init", "bad\x00argument"}
	if changed.Valid() {
		t.Fatal("control-bearing argv is valid")
	}
	changed = valid
	changed.arguments = make([]string, MaximumCommandArguments+1)
	for index := range changed.arguments {
		changed.arguments[index] = "x"
	}
	if changed.Valid() {
		t.Fatal("over-count argv is valid")
	}
	changed = valid
	changed.auth = AuthenticationProjection{mode: AuthenticationAnonymousHTTPS}
	if changed.Valid() {
		t.Fatal("invalid hidden authentication state is valid")
	}
}

func TestFetchArgvPinsReferenceAndHardening(t *testing.T) {
	t.Parallel()

	advertised := "ref: refs/heads/main\tHEAD\n" + testObjectA + "\tHEAD\n" +
		testObjectA + "\trefs/heads/main\n" + testObjectB + "\trefs/heads/release/v1\n" +
		testObjectB + "\trefs/tags/v1.0.0\n"
	for _, test := range []struct {
		name      string
		requested string
		provided  bool
		want      string
	}{
		{"default", "", false, "+" + testObjectA + ":refs/ai4j/acquired"},
		{"branch", "release/v1", true, "+" + testObjectB + ":refs/ai4j/acquired"},
		{"tag", "refs/tags/v1.0.0", true, "+" + testObjectB + ":refs/ai4j/acquired"},
		{"commit", testObjectA, true, "+" + testObjectA + ":refs/ai4j/acquired"},
	} {
		request := mustResolutionRequest(t, "git@github.com:alx4j/ai4j.git", test.requested, test.provided)
		auth := mustAuthentication(t, request, AuthenticationDefaultKeySSH)
		selection, err := ResolveReference(request, mustRemoteAdvertisement(t, request, advertised))
		if err != nil {
			t.Fatal(err)
		}
		command, err := NewFetchCommand(selection, auth)
		if err != nil || !command.Valid() {
			t.Fatalf("NewFetchCommand = %#v, %v", command, err)
		}
		wantTail := []string{
			"-c", "protocol.ssh.allow=always", "fetch", "--quiet", "--atomic", "--no-tags",
			"--no-recurse-submodules", "--no-auto-maintenance", "--no-auto-gc", "--no-write-commit-graph",
			"--no-write-fetch-head", "--", "git@github.com:alx4j/ai4j.git", test.want,
		}
		if got, want := command.Arguments(), append(testCommonArguments(true), wantTail...); !reflect.DeepEqual(got, want) {
			t.Errorf("%s argv = %#v", test.name, got)
		}
	}
}

func TestLocalCommandArgvPinsExactRepository(t *testing.T) {
	t.Parallel()

	proven := mustDirectProvenCommit(t, testObjectA)
	command, err := NewObjectTypeCommand(proven.Resolution())
	if err != nil {
		t.Fatal(err)
	}
	want := append(testCommonArguments(true), "cat-file", "-t", "--", testObjectA)
	if got := command.Arguments(); !reflect.DeepEqual(got, want) || command.WorkingDirectory() != GitDirectory {
		t.Fatalf("object argv = %#v", got)
	}
	initialize := NewInitializeCommand()
	if got, want := initialize.Arguments(), append(testCommonArguments(false), "init", "--quiet", "--template=", "--object-format=sha1", "--initial-branch=ai4j-unborn"); !reflect.DeepEqual(got, want) || initialize.WorkingDirectory() != WorkspaceRootDirectory {
		t.Fatalf("init argv = %#v", got)
	}
	config := NewConfigAuditCommand()
	if got, want := config.Arguments(), append(testCommonArguments(true), "config", "--local", "--null", "--list", "--no-includes", "--"); !reflect.DeepEqual(got, want) {
		t.Fatalf("config argv = %#v", got)
	}
}

func TestNetworkCommandUsesCanonicalEnterpriseSSHEndpoint(t *testing.T) {
	t.Parallel()

	request := mustResolutionRequest(t, "git@gitlab.barclays.example:division/team/toolkit.git", "refs/tags/v1.0.0", true)
	auth := mustAuthentication(t, request, AuthenticationDefaultKeySSH)
	command, err := NewEnumerateReferencesCommand(request, auth)
	if err != nil || !command.Valid() {
		t.Fatalf("NewEnumerateReferencesCommand = %#v, %v", command, err)
	}
	arguments := command.Arguments()
	if !slices.Contains(arguments, "git@gitlab.barclays.example:division/team/toolkit.git") || slices.Contains(arguments, "https://gitlab.barclays.example/division/team/toolkit.git") {
		t.Fatalf("enterprise argv = %#v", arguments)
	}
}

func TestEveryRemainingCommandConstructorHasExactGoldenArgv(t *testing.T) {
	t.Parallel()

	commit, _ := domain.NewCommitOID(testObjectA)
	proven := mustDirectProvenCommit(t, testObjectA)
	tree := mustTreeOID(t, testObjectB)
	inventory, err := ParseTreeInventory(tree, []byte(treeRecord("100644", "blob", testObjectA, 1, "file.txt")))
	if err != nil {
		t.Fatal(err)
	}
	proof, err := NewCommitTreeProof(proven, []byte(testObjectB+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewMaterializationPlan(proof, inventory)
	if err != nil {
		t.Fatal(err)
	}
	tagRequest := mustResolutionRequest(t, "https://github.com/alx4j/ai4j.git", "refs/tags/v1", true)
	tagResolution, err := ResolveReference(tagRequest, mustRemoteAdvertisement(t, tagRequest, testObjectA+"\trefs/tags/v1\n"))
	if err != nil {
		t.Fatal(err)
	}
	selectedTag, err := NewSelectedObjectProof(tagResolution, []byte("tag\n"))
	if err != nil {
		t.Fatal(err)
	}
	peel, _ := NewPeelCommitCommand(selectedTag)
	commitTree, _ := NewCommitTreeCommand(proven)
	listTree, _ := NewListTreeCommand(proof)
	readTree, _ := NewReadTreeCommand(plan)
	checkoutIndex, _ := NewCheckoutIndexCommand(mustCheckoutApproval(t, plan))
	checkout, _ := NewCheckoutDetachedCommand(mustCheckoutApproval(t, plan))
	ancestor, _ := NewIsAncestorCommand(commit, mustCommitOID(t, testObjectB))

	tests := []struct {
		name    string
		command Command
		tail    []string
		timeout time.Duration
		grammar OutputGrammar
	}{
		{"probe exec path", NewProbeExecPathCommand(), []string{"--exec-path"}, LocalCommandTimeout, ScalarOutputGrammar},
		{"peel commit", peel, []string{"rev-parse", "--verify", "--end-of-options", testObjectA + "^{commit}"}, LocalCommandTimeout, ScalarOutputGrammar},
		{"commit tree", commitTree, []string{"rev-parse", "--verify", "--end-of-options", testObjectA + "^{tree}"}, LocalCommandTimeout, ScalarOutputGrammar},
		{"list tree", listTree, []string{"ls-tree", "-r", "-z", "--full-tree", "--long", testObjectB, "--"}, NetworkCommandTimeout, TreeOutputGrammar},
		{"read tree", readTree, []string{"read-tree", "--reset", "--no-sparse-checkout", testObjectA}, NetworkCommandTimeout, NoOutputGrammar},
		{"checkout index", checkoutIndex, []string{"checkout-index", "--all"}, NetworkCommandTimeout, NoOutputGrammar},
		{"checkout", checkout, []string{"checkout", "--quiet", "--detach", "--no-recurse-submodules", "--no-overwrite-ignore", testObjectA}, NetworkCommandTimeout, NoOutputGrammar},
		{"list index", NewListIndexCommand(), []string{"ls-files", "--cached", "--stage", "--full-name", "-z", "--"}, NetworkCommandTimeout, IndexOutputGrammar},
		{"status", NewStatusCommand(), []string{"status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored=matching", "--ignore-submodules=none", "--"}, NetworkCommandTimeout, StatusOutputGrammar},
		{"ancestor", ancestor, []string{"merge-base", "--is-ancestor", "--", testObjectA, testObjectB}, LocalCommandTimeout, NoOutputGrammar},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if !test.command.Valid() || test.command.TimeoutMaximum() != test.timeout || test.command.OutputGrammar() != test.grammar {
				t.Fatalf("command facts = %#v", test.command)
			}
			repositoryBound := test.command.Operation() != OperationProbeExecPath
			if got, want := test.command.Arguments(), append(testCommonArguments(repositoryBound), test.tail...); !reflect.DeepEqual(got, want) {
				t.Fatalf("argv = %#v", got)
			}
		})
	}
}

func TestAttributeCommandSortsAndProvesOutputBound(t *testing.T) {
	t.Parallel()

	tree := mustTreeOID(t, testObjectB)
	inventory, err := ParseTreeInventory(tree, []byte(
		treeRecord("100644", "blob", testObjectA, 1, "z/file")+
			treeRecord("100644", "blob", testObjectB, 1, "a/file"),
	))
	if err != nil {
		t.Fatal(err)
	}
	plan := mustMaterializationPlan(t, inventory, testObjectA)
	batches, err := PlanCheckoutAttributeBatches(plan)
	if err != nil || len(batches) != 1 {
		t.Fatalf("batches = %#v, %v", batches, err)
	}
	command, err := NewCheckAttributesCommand(batches[0])
	if err != nil || !command.Valid() {
		t.Fatalf("command = %#v, %v", command, err)
	}
	wantTail := []string{"check-attr", "-z", "--cached", "filter", "text", "eol", "crlf", "ident", "working-tree-encoding", "--", "a/file", "z/file"}
	if got, want := command.Arguments(), append(testCommonArguments(true), wantTail...); !reflect.DeepEqual(got, want) {
		t.Fatalf("attribute argv = %#v", got)
	}
	if command.OutputLimitBytes() != attributeOutputLimit || command.OutputLimitBytes() >= protocolOutputLimit {
		t.Fatalf("attribute output limit = %d", command.OutputLimitBytes())
	}
	changed := batches[0]
	changed.paths = append(changed.paths, changed.paths[0])
	if _, err := NewCheckAttributesCommand(changed); !errors.Is(err, ErrExecutorContract) {
		t.Fatalf("duplicate batch error = %v", err)
	}
}

func TestNetworkCommandRejectsRepositoryAndTransportMismatch(t *testing.T) {
	t.Parallel()

	request := mustResolutionRequest(t, "https://github.com/alx4j/ai4j.git", "", false)
	other := mustRepository(t, "github.com/other/repository")
	otherAuth, _ := NewAuthenticationProjection(other, domain.HTTPSGitTransport(), AuthenticationAnonymousHTTPS)
	if _, err := NewEnumerateReferencesCommand(request, otherAuth); !errors.Is(err, ErrExecutorContract) {
		t.Fatalf("repository mismatch error = %v", err)
	}
	sshAuth, _ := NewAuthenticationProjection(request.Repository(), domain.SSHGitTransport(), AuthenticationDefaultKeySSH)
	if _, err := NewEnumerateReferencesCommand(request, sshAuth); !errors.Is(err, ErrExecutorContract) {
		t.Fatalf("transport mismatch error = %v", err)
	}
}

func testCommonArguments(repositoryBound bool) []string {
	result := []string{"--no-pager", "--no-replace-objects", "--literal-pathspecs", "--no-optional-locks"}
	if repositoryBound {
		result = append(result, "-C", "..", "--git-dir=.git", "--work-tree=.")
	}
	for _, setting := range []string{
		"advice.detachedHead=false", "core.attributesFile=/dev/null", "core.excludesFile=/dev/null",
		"core.fsmonitor=false", "core.hooksPath=/dev/null", "core.sparseCheckout=false", "core.autocrlf=false",
		"core.protectHFS=true", "core.protectNTFS=true", "fetch.fsckObjects=true", "transfer.fsckObjects=true",
		"fetch.unpackLimit=0", "transfer.unpackLimit=0", "fetch.writeCommitGraph=false",
		"fetch.recurseSubmodules=false", "fetch.uriProtocols=", "submodule.recurse=false", "maintenance.auto=false",
		"gc.auto=0", "protocol.allow=never", "protocol.version=0", "http.followRedirects=false", "http.getanyfile=false",
	} {
		result = append(result, "-c", setting)
	}
	return result
}

func mustResolutionRequest(t *testing.T, repository, reference string, referenceProvided bool) ResolutionRequest {
	t.Helper()
	input, err := gitremote.NewSelectionInput(repository, true, reference, referenceProvided)
	if err != nil {
		t.Fatal(err)
	}
	effective, err := gitremote.Resolve(input)
	if err != nil {
		t.Fatal(err)
	}
	request, err := NewResolutionRequest(effective)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func mustRepository(t *testing.T, value string) domain.RepositoryIdentity {
	t.Helper()
	repository, err := domain.NewRepositoryIdentity(value)
	if err != nil {
		t.Fatal(err)
	}
	return repository
}

func mustAuthentication(t *testing.T, request ResolutionRequest, mode AuthenticationMode) AuthenticationProjection {
	t.Helper()
	auth, err := NewAuthenticationProjection(request.Repository(), request.Transport(), mode)
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func mustCommitOID(t *testing.T, value string) domain.CommitOID {
	t.Helper()
	commit, err := domain.NewCommitOID(value)
	if err != nil {
		t.Fatal(err)
	}
	return commit
}

func mustRemoteAdvertisement(t *testing.T, request ResolutionRequest, data string) RemoteAdvertisement {
	t.Helper()
	advertisement, err := ParseRemoteAdvertisement(request, []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return advertisement
}

func mustMaterializationPlan(t *testing.T, inventory TreeInventory, commitValue string) MaterializationPlan {
	t.Helper()
	commit := mustDirectProvenCommit(t, commitValue)
	proof, err := NewCommitTreeProof(commit, []byte(inventory.Tree().String()+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewMaterializationPlan(proof, inventory)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func mustDirectProvenCommit(t *testing.T, value string) ProvenCommit {
	t.Helper()
	request := mustResolutionRequest(t, "https://github.com/alx4j/ai4j.git", value, true)
	selection, err := ResolveReference(request, mustRemoteAdvertisement(t, request, ""))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := NewSelectedObjectProof(selection, []byte("commit\n"))
	if err != nil {
		t.Fatal(err)
	}
	commit, err := NewDirectProvenCommit(selected)
	if err != nil {
		t.Fatal(err)
	}
	return commit
}

func mustCheckoutApproval(t *testing.T, plan MaterializationPlan) CheckoutApproval {
	t.Helper()
	batches, err := PlanCheckoutAttributeBatches(plan)
	if err != nil {
		t.Fatal(err)
	}
	proofs := make([]CheckoutAttributeBatchProof, len(batches))
	for index, batch := range batches {
		var output strings.Builder
		for _, path := range batch.Paths() {
			for _, attribute := range closedCheckoutAttributes {
				output.WriteString(path.String() + "\x00" + attribute + "\x00unspecified\x00")
			}
		}
		proofs[index], err = ValidateCheckoutAttributes(batch, []byte(output.String()))
		if err != nil {
			t.Fatal(err)
		}
	}
	approval, err := CompleteCheckoutAttributeCoverage(plan, proofs)
	if err != nil {
		t.Fatal(err)
	}
	return approval
}
