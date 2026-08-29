package git

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/pathsafe"
	"github.com/alx4j/ai4j/internal/source/git/protocol"
	"github.com/alx4j/ai4j/internal/source/gitremote"
)

var closedCheckoutAttributes = []string{
	"filter", "text", "eol", "crlf", "ident", "working-tree-encoding",
}

// Command is a closed direct-Git invocation plan. It deliberately contains no
// executable locator, shell command, working-directory path, or credential.
type Command struct {
	operation Operation
	arguments []string
	directory WorkingDirectoryClass
	grammar   OutputGrammar
	timeout   time.Duration
	auth      AuthenticationProjection
	hasAuth   bool
}

func (c Command) Operation() Operation                    { return c.operation }
func (c Command) Arguments() []string                     { return append([]string(nil), c.arguments...) }
func (c Command) WorkingDirectory() WorkingDirectoryClass { return c.directory }
func (c Command) OutputGrammar() OutputGrammar            { return c.grammar }
func (c Command) TimeoutMaximum() time.Duration           { return c.timeout }
func (c Command) Authentication() (AuthenticationProjection, bool) {
	return c.auth, c.hasAuth && c.auth.Valid()
}

func (c Command) Valid() bool {
	if !c.operation.Valid() || len(c.arguments) == 0 || !c.directory.Valid() || !c.grammar.Valid() ||
		c.timeout <= 0 || c.timeout > NetworkCommandTimeout || c.hasAuth != c.auth.Valid() ||
		!c.hasAuth && c.auth != (AuthenticationProjection{}) || len(c.arguments) > MaximumCommandArguments ||
		!commandShapeValid(c) {
		return false
	}
	for _, argument := range c.arguments {
		if argument == "" || len(argument) > MaximumCommandArgumentBytes || !utf8.ValidString(argument) {
			return false
		}
		for _, character := range argument {
			if unicode.IsControl(character) {
				return false
			}
		}
	}
	_, ok := serializedArgumentBytes(c.arguments)
	return ok
}

func commandShapeValid(c Command) bool {
	type shape struct {
		directory WorkingDirectoryClass
		grammar   OutputGrammar
		timeout   time.Duration
		auth      bool
	}
	var expected shape
	switch c.operation {
	case OperationInitialize:
		expected = shape{WorkspaceRootDirectory, NoOutputGrammar, LocalCommandTimeout, false}
	case OperationEnumerateRefs:
		expected = shape{GitDirectory, RemoteOutputGrammar, NetworkCommandTimeout, true}
	case OperationFetch:
		expected = shape{GitDirectory, NoOutputGrammar, NetworkCommandTimeout, true}
	case OperationObjectType, OperationPeelCommit, OperationCommitTree:
		expected = shape{GitDirectory, ScalarOutputGrammar, LocalCommandTimeout, false}
	case OperationListTree:
		expected = shape{GitDirectory, TreeOutputGrammar, NetworkCommandTimeout, false}
	case OperationReadTree, OperationCheckoutIndex, OperationCheckoutDetached:
		expected = shape{GitDirectory, NoOutputGrammar, NetworkCommandTimeout, false}
	case OperationCheckAttributes:
		expected = shape{GitDirectory, AttributeOutputGrammar, NetworkCommandTimeout, false}
	case OperationListIndex:
		expected = shape{GitDirectory, IndexOutputGrammar, NetworkCommandTimeout, false}
	case OperationStatus:
		expected = shape{GitDirectory, StatusOutputGrammar, NetworkCommandTimeout, false}
	case OperationIsAncestor:
		expected = shape{GitDirectory, NoOutputGrammar, LocalCommandTimeout, false}
	default:
		return false
	}
	return c.directory == expected.directory && c.grammar == expected.grammar && c.timeout == expected.timeout &&
		c.hasAuth == expected.auth
}

func (Command) String() string   { return "<git-command:redacted>" }
func (Command) GoString() string { return "<git-command:redacted>" }
func (c Command) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, c.String())
}
func (c Command) MarshalText() ([]byte, error) { return []byte(c.String()), nil }
func (Command) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"git_command": "redacted"})
}

func NewInitializeCommand() Command {
	return buildCommand(OperationInitialize, WorkspaceRootDirectory, NoOutputGrammar,
		LocalCommandTimeout, AuthenticationProjection{}, false,
		[]string{"init", "--quiet", "--template=", "--object-format=sha1", "--initial-branch=ai4j-unborn"})
}

func NewEnumerateReferencesCommand(
	request ResolutionRequest,
	auth AuthenticationProjection,
) (Command, error) {
	endpoint, err := networkEndpoint(request, auth)
	if err != nil {
		return Command{}, err
	}
	return buildNetworkCommand(OperationEnumerateRefs, RemoteOutputGrammar, auth,
		[]string{"ls-remote", "--quiet", "--symref", "--", endpoint, "HEAD", "refs/heads/*", "refs/tags/*"}), nil
}

func NewFetchCommand(
	selection ReferenceResolution,
	auth AuthenticationProjection,
) (Command, error) {
	if !selection.Valid() {
		return Command{}, ErrExecutorContract
	}
	request := selection.Request()
	endpoint, err := networkEndpoint(request, auth)
	if err != nil {
		return Command{}, ErrExecutorContract
	}
	refspec, ok := fetchRefspec(selection)
	if !ok {
		return Command{}, ErrExecutorContract
	}
	return buildNetworkCommand(OperationFetch, NoOutputGrammar, auth,
		[]string{
			"fetch", "--quiet", "--atomic", "--no-tags", "--no-recurse-submodules",
			"--no-auto-maintenance", "--no-auto-gc", "--no-write-commit-graph", "--no-write-fetch-head",
			"--", endpoint, refspec,
		}), nil
}

func NewObjectTypeCommand(resolution ReferenceResolution) (Command, error) {
	if !resolution.Valid() {
		return Command{}, ErrExecutorContract
	}
	return buildCommand(OperationObjectType, GitDirectory, ScalarOutputGrammar,
		LocalCommandTimeout, AuthenticationProjection{}, false,
		[]string{"cat-file", "-t", "--", resolution.SelectedObject().String()}), nil
}

func NewPeelCommitCommand(selected SelectedObjectProof) (Command, error) {
	if !selected.Valid() || selected.Type() != SelectedTagObject {
		return Command{}, ErrExecutorContract
	}
	return buildCommand(OperationPeelCommit, GitDirectory, ScalarOutputGrammar,
		LocalCommandTimeout, AuthenticationProjection{}, false,
		[]string{"rev-parse", "--verify", "--end-of-options", selected.Object().String() + "^{commit}"}), nil
}

func NewCommitTreeCommand(commit ProvenCommit) (Command, error) {
	if !commit.Valid() {
		return Command{}, ErrExecutorContract
	}
	return buildCommand(OperationCommitTree, GitDirectory, ScalarOutputGrammar,
		LocalCommandTimeout, AuthenticationProjection{}, false,
		[]string{"rev-parse", "--verify", "--end-of-options", commit.Commit().String() + "^{tree}"}), nil
}

func NewListTreeCommand(proof CommitTreeProof) (Command, error) {
	if !proof.Valid() {
		return Command{}, ErrExecutorContract
	}
	return buildCommand(OperationListTree, GitDirectory, TreeOutputGrammar,
		NetworkCommandTimeout, AuthenticationProjection{}, false,
		[]string{"ls-tree", "-r", "-z", "--full-tree", "--long", proof.Tree().String(), "--"}), nil
}

func NewReadTreeCommand(plan MaterializationPlan) (Command, error) {
	if !plan.Valid() {
		return Command{}, ErrExecutorContract
	}
	return buildCommand(OperationReadTree, GitDirectory, NoOutputGrammar,
		NetworkCommandTimeout, AuthenticationProjection{}, false,
		[]string{"read-tree", "--reset", "--no-sparse-checkout", plan.Commit().String()}), nil
}

func NewCheckAttributesCommand(batch CheckoutAttributeBatch) (Command, error) {
	if !batch.Valid() {
		return Command{}, ErrExecutorContract
	}
	validated := batch.Paths()
	arguments := []string{"check-attr", "-z", "--cached"}
	arguments = append(arguments, closedCheckoutAttributes...)
	arguments = append(arguments, "--")
	for _, path := range validated {
		arguments = append(arguments, path.String())
	}
	return buildCommand(OperationCheckAttributes, GitDirectory, AttributeOutputGrammar,
		NetworkCommandTimeout, AuthenticationProjection{}, false, arguments), nil
}

func NewCheckoutDetachedCommand(approval CheckoutApproval) (Command, error) {
	if !approval.Valid() {
		return Command{}, ErrExecutorContract
	}
	plan := approval.Plan()
	return buildCommand(OperationCheckoutDetached, GitDirectory, NoOutputGrammar,
		NetworkCommandTimeout, AuthenticationProjection{}, false,
		[]string{"checkout", "--quiet", "--detach", "--no-recurse-submodules", "--no-overwrite-ignore", plan.Commit().String()}), nil
}

func NewCheckoutIndexCommand(approval CheckoutApproval) (Command, error) {
	if !approval.Valid() {
		return Command{}, ErrExecutorContract
	}
	return buildCommand(OperationCheckoutIndex, GitDirectory, NoOutputGrammar,
		NetworkCommandTimeout, AuthenticationProjection{}, false,
		[]string{"checkout-index", "--all"}), nil
}

func NewListIndexCommand() Command {
	return buildCommand(OperationListIndex, GitDirectory, IndexOutputGrammar,
		NetworkCommandTimeout, AuthenticationProjection{}, false,
		[]string{"ls-files", "--cached", "--stage", "--full-name", "-z", "--"})
}

func NewStatusCommand() Command {
	return buildCommand(OperationStatus, GitDirectory, StatusOutputGrammar,
		NetworkCommandTimeout, AuthenticationProjection{}, false,
		[]string{"status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored=matching", "--ignore-submodules=none", "--"})
}

func NewIsAncestorCommand(ancestor, descendant domain.CommitOID) (Command, error) {
	if !ancestor.Valid() || !descendant.Valid() {
		return Command{}, ErrExecutorContract
	}
	return buildCommand(OperationIsAncestor, GitDirectory, NoOutputGrammar,
		LocalCommandTimeout, AuthenticationProjection{}, false,
		[]string{"merge-base", "--is-ancestor", "--", ancestor.String(), descendant.String()}), nil
}

func buildNetworkCommand(
	operation Operation,
	grammar OutputGrammar,
	auth AuthenticationProjection,
	tail []string,
) Command {
	return buildCommand(operation, GitDirectory, grammar, NetworkCommandTimeout, auth, true, tail)
}

func buildCommand(
	operation Operation,
	directory WorkingDirectoryClass,
	grammar OutputGrammar,
	timeout time.Duration,
	auth AuthenticationProjection,
	hasAuth bool,
	tail []string,
) Command {
	arguments := commonArguments(directory == GitDirectory)
	if hasAuth {
		arguments = append(arguments, "-c", protocolAllowance(auth.Transport()))
	}
	arguments = append(arguments, tail...)
	return Command{
		operation: operation, arguments: arguments, directory: directory, grammar: grammar,
		timeout: timeout, auth: auth, hasAuth: hasAuth,
	}
}

func commonArguments(repositoryBound bool) []string {
	arguments := []string{"--no-pager", "--no-replace-objects", "--literal-pathspecs", "--no-optional-locks"}
	if repositoryBound {
		arguments = append(arguments, "-C", "..", "--git-dir=.git", "--work-tree=.")
	}
	for _, setting := range []string{
		"advice.detachedHead=false",
		"core.attributesFile=/dev/null",
		"core.excludesFile=/dev/null",
		"core.fsmonitor=false",
		"core.hooksPath=/dev/null",
		"core.sparseCheckout=false",
		"core.autocrlf=false",
		"core.protectHFS=true",
		"core.protectNTFS=true",
		"fetch.fsckObjects=true",
		"transfer.fsckObjects=true",
		"fetch.unpackLimit=0",
		"transfer.unpackLimit=0",
		"fetch.writeCommitGraph=false",
		"fetch.recurseSubmodules=false",
		"fetch.uriProtocols=",
		"submodule.recurse=false",
		"maintenance.auto=false",
		"gc.auto=0",
		"protocol.allow=never",
		"protocol.version=0",
		"http.followRedirects=false",
		"http.getanyfile=false",
	} {
		arguments = append(arguments, "-c", setting)
	}
	return arguments
}

func protocolAllowance(transport domain.GitTransport) string {
	switch transport {
	case domain.HTTPSGitTransport():
		return "protocol.https.allow=always"
	case domain.SSHGitTransport():
		return "protocol.ssh.allow=always"
	default:
		return "invalid"
	}
}

func networkEndpoint(request ResolutionRequest, auth AuthenticationProjection) (string, error) {
	if !request.Valid() || !auth.Valid() || request.Repository() != auth.Repository() ||
		request.Transport() != auth.Transport() {
		return "", ErrExecutorContract
	}
	remote, err := gitremote.ReconstructRemote(request.Repository(), request.Transport())
	if err != nil {
		return "", ErrExecutorContract
	}
	return remote.Endpoint(), nil
}

func fetchRefspec(selection ReferenceResolution) (string, bool) {
	const destination = "refs/ai4j/acquired"
	if !selection.Valid() {
		return "", false
	}
	return "+" + selection.SelectedObject().String() + ":" + destination, true
}

func validateAttributeBatch(paths []pathsafe.RelativePath) ([]pathsafe.RelativePath, error) {
	if len(paths) == 0 || len(paths) > MaximumAttributeBatchPaths {
		return nil, ErrExecutorContract
	}
	result := append([]pathsafe.RelativePath(nil), paths...)
	seen := make(map[string]struct{}, len(result))
	pathBytes := 0
	for _, path := range result {
		if !path.Valid() || pathBytes > MaximumAttributeBatchBytes-len(path.String()) {
			return nil, ErrExecutorContract
		}
		pathBytes += len(path.String())
		if _, duplicate := seen[path.String()]; duplicate {
			return nil, ErrExecutorContract
		}
		seen[path.String()] = struct{}{}
	}
	slices.SortFunc(result, func(left, right pathsafe.RelativePath) int {
		if left.String() < right.String() {
			return -1
		}
		if left.String() > right.String() {
			return 1
		}
		return 0
	})
	if !attributeBatchBoundsFit(pathBytes, len(result)) {
		return nil, ErrExecutorContract
	}
	return result, nil
}

func attributeBatchBoundsFit(pathBytes, pathCount int) bool {
	if pathBytes < 0 || pathCount <= 0 || pathCount > MaximumAttributeBatchPaths ||
		len(closedCheckoutAttributes) == 0 || len(closedCheckoutAttributes) > MaximumCheckoutAttributes {
		return false
	}
	fixedArguments := append(commonArguments(true), "check-attr", "-z", "--cached")
	fixedArguments = append(fixedArguments, closedCheckoutAttributes...)
	fixedArguments = append(fixedArguments, "--")
	if len(fixedArguments)+pathCount > MaximumCommandArguments {
		return false
	}
	argumentBytes, ok := serializedArgumentBytes(fixedArguments)
	if !ok || pathBytes > MaximumAttributeBatchBytes-argumentBytes-pathCount {
		return false
	}
	// check-attr -z repeats every path for every requested attribute. The
	// accepted value vocabulary is bounded by "unspecified" (11 bytes).
	worst := uint64(pathBytes)*uint64(len(closedCheckoutAttributes)) +
		uint64(pathCount*len(closedCheckoutAttributes))*(1+32+1+11+1)
	return worst < protocol.MaximumAttributeOutputBytes
}

func serializedArgumentBytes(arguments []string) (int, bool) {
	total := 0
	for _, argument := range arguments {
		if argument == "" || total > MaximumAttributeBatchBytes-len(argument)-1 {
			return 0, false
		}
		total += len(argument) + 1
	}
	return total, true
}
