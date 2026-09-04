package git

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/pathsafe"
	"github.com/alx4j/ai4j/internal/source/git/protocol"
)

// CommitTreeProof binds one exact commit to the tree observed through the
// closed commit^{tree} command. Commit and tree identities remain distinct.
type CommitTreeProof struct {
	commit ProvenCommit
	tree   domain.TreeOID
}

func NewCommitTreeProof(commit ProvenCommit, data []byte) (CommitTreeProof, error) {
	if !commit.Valid() {
		return CommitTreeProof{}, NewExecutorError(OperationCommitTree, FailureInvalidOperation)
	}
	value, err := protocol.ParseSingleLine(data)
	if err != nil {
		return CommitTreeProof{}, NewExecutorError(OperationCommitTree, FailureMalformedProtocol)
	}
	tree, err := domain.NewTreeOID(value)
	if err != nil {
		return CommitTreeProof{}, NewExecutorError(OperationCommitTree, FailureMalformedProtocol)
	}
	proof := CommitTreeProof{commit: commit, tree: tree}
	if !proof.Valid() {
		return CommitTreeProof{}, NewExecutorError(OperationCommitTree, FailureMalformedProtocol)
	}
	return proof, nil
}

func (p CommitTreeProof) CommitProof() ProvenCommit { return p.commit }
func (p CommitTreeProof) Commit() domain.CommitOID  { return p.commit.Commit() }
func (p CommitTreeProof) Tree() domain.TreeOID      { return p.tree }
func (p CommitTreeProof) Valid() bool {
	return p.commit.Valid() && p.tree.Valid()
}

// Matches re-parses a final commit^{tree} observation and proves it has not
// drifted from the tree used for inventory validation and materialization.
func (p CommitTreeProof) Matches(data []byte) error {
	if !p.Valid() {
		return NewExecutorError(OperationCommitTree, FailureInvalidOperation)
	}
	repeated, err := NewCommitTreeProof(p.commit, data)
	if err != nil {
		return err
	}
	if repeated.Commit() != p.Commit() || repeated.Tree() != p.Tree() {
		return NewExecutorError(OperationCommitTree, FailureRepositoryConflict)
	}
	return nil
}

func (CommitTreeProof) String() string   { return "<git-commit-tree-proof:redacted>" }
func (CommitTreeProof) GoString() string { return "<git-commit-tree-proof:redacted>" }
func (p CommitTreeProof) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, p.String())
}
func (p CommitTreeProof) MarshalText() ([]byte, error) { return []byte(p.String()), nil }
func (CommitTreeProof) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"git_commit_tree_proof": "redacted"})
}

// MaterializationPlan is the only checkout-capable aggregate. It proves that
// the exact commit tree and the collision/resource-validated inventory agree.
type MaterializationPlan struct {
	proof     CommitTreeProof
	inventory TreeInventory
}

func NewMaterializationPlan(proof CommitTreeProof, inventory TreeInventory) (MaterializationPlan, error) {
	plan := MaterializationPlan{proof: proof, inventory: cloneTreeInventory(inventory)}
	if !plan.Valid() {
		return MaterializationPlan{}, ErrExecutorContract
	}
	return plan, nil
}

func (p MaterializationPlan) Commit() domain.CommitOID { return p.proof.Commit() }
func (p MaterializationPlan) Tree() domain.TreeOID     { return p.proof.tree }
func (p MaterializationPlan) Inventory() TreeInventory { return cloneTreeInventory(p.inventory) }
func (p MaterializationPlan) Valid() bool {
	return p.proof.Valid() && p.inventory.Valid() && p.proof.tree == p.inventory.tree
}

func (MaterializationPlan) String() string   { return "<git-materialization-plan:redacted>" }
func (MaterializationPlan) GoString() string { return "<git-materialization-plan:redacted>" }
func (p MaterializationPlan) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, p.String())
}
func (p MaterializationPlan) MarshalText() ([]byte, error) { return []byte(p.String()), nil }
func (MaterializationPlan) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"git_materialization_plan": "redacted"})
}

// CheckoutAttributeBatch is one deterministic, bounded slice of the exact
// tree inventory in a materialization plan.
type CheckoutAttributeBatch struct {
	planTree domain.TreeOID
	index    int
	start    int
	end      int
	paths    []pathsafe.RelativePath
}

func (b CheckoutAttributeBatch) Paths() []pathsafe.RelativePath {
	return append([]pathsafe.RelativePath(nil), b.paths...)
}

func (b CheckoutAttributeBatch) Valid() bool {
	if !b.planTree.Valid() || b.index < 0 || b.start < 0 || b.end <= b.start || b.end-b.start != len(b.paths) {
		return false
	}
	validated, err := validateAttributeBatch(b.paths)
	return err == nil && samePaths(b.paths, validated)
}

// PlanCheckoutAttributeBatches partitions every inventory path exactly once
// using the closed argument-count and serialized-argv limits. The partition is
// deterministic for a given materialization plan.
func PlanCheckoutAttributeBatches(plan MaterializationPlan) ([]CheckoutAttributeBatch, error) {
	ranges, err := deterministicCheckoutAttributePaths(plan)
	if err != nil {
		return nil, err
	}
	batches := make([]CheckoutAttributeBatch, len(ranges))
	for index := range ranges {
		paths := append([]pathsafe.RelativePath(nil), ranges[index].paths...)
		batches[index] = CheckoutAttributeBatch{
			planTree: plan.Tree(), index: index, start: ranges[index].start, end: ranges[index].end, paths: paths,
		}
	}
	return batches, nil
}

// CheckoutAttributeBatchProof records that the exact output for one issued
// deterministic batch passed the closed attribute policy.
type CheckoutAttributeBatchProof struct {
	batch CheckoutAttributeBatch
}

func (p CheckoutAttributeBatchProof) Valid() bool {
	return p.batch.Valid()
}

// CheckoutApproval is the only checkout-capable proof. It is issued only
// after every deterministic batch for one exact plan has passed exactly once.
type CheckoutApproval struct {
	plan MaterializationPlan
}

func (a CheckoutApproval) Plan() MaterializationPlan { return cloneMaterializationPlan(a.plan) }
func (a CheckoutApproval) Valid() bool {
	return a.plan.Valid()
}

// CompleteCheckoutAttributeCoverage proves complete, ordered coverage of the
// plan's exact tree and paths. Omissions, duplicates, reordering, and batches
// from another tree fail.
func CompleteCheckoutAttributeCoverage(
	plan MaterializationPlan,
	proofs []CheckoutAttributeBatchProof,
) (CheckoutApproval, error) {
	expected, err := PlanCheckoutAttributeBatches(plan)
	if err != nil || len(proofs) != len(expected) {
		return CheckoutApproval{}, ErrExecutorContract
	}
	for index := range expected {
		actualBatch, expectedBatch := proofs[index].batch, expected[index]
		if !proofs[index].Valid() || actualBatch.planTree != expectedBatch.planTree || actualBatch.index != index ||
			actualBatch.start != expectedBatch.start || actualBatch.end != expectedBatch.end ||
			!samePaths(actualBatch.paths, expectedBatch.paths) {
			return CheckoutApproval{}, ErrExecutorContract
		}
	}
	approval := CheckoutApproval{plan: cloneMaterializationPlan(plan)}
	if !approval.Valid() {
		return CheckoutApproval{}, ErrExecutorContract
	}
	return approval, nil
}

func (CheckoutAttributeBatch) String() string   { return "<git-checkout-attribute-batch:redacted>" }
func (CheckoutAttributeBatch) GoString() string { return "<git-checkout-attribute-batch:redacted>" }
func (b CheckoutAttributeBatch) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, b.String())
}
func (b CheckoutAttributeBatch) MarshalText() ([]byte, error) { return []byte(b.String()), nil }
func (CheckoutAttributeBatch) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"git_checkout_attribute_batch": "redacted"})
}

func (CheckoutAttributeBatchProof) String() string {
	return "<git-checkout-attribute-proof:redacted>"
}
func (CheckoutAttributeBatchProof) GoString() string {
	return "<git-checkout-attribute-proof:redacted>"
}
func (p CheckoutAttributeBatchProof) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, p.String())
}
func (p CheckoutAttributeBatchProof) MarshalText() ([]byte, error) { return []byte(p.String()), nil }
func (CheckoutAttributeBatchProof) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"git_checkout_attribute_proof": "redacted"})
}

func (CheckoutApproval) String() string   { return "<git-checkout-approval:redacted>" }
func (CheckoutApproval) GoString() string { return "<git-checkout-approval:redacted>" }
func (a CheckoutApproval) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, a.String())
}
func (a CheckoutApproval) MarshalText() ([]byte, error) { return []byte(a.String()), nil }
func (CheckoutApproval) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"git_checkout_approval": "redacted"})
}

type checkoutAttributePathRange struct {
	start int
	end   int
	paths []pathsafe.RelativePath
}

func deterministicCheckoutAttributePaths(plan MaterializationPlan) ([]checkoutAttributePathRange, error) {
	if !plan.Valid() {
		return nil, ErrExecutorContract
	}
	entries := plan.inventory.entries
	result := make([]checkoutAttributePathRange, 0, (len(entries)+MaximumAttributeBatchPaths-1)/MaximumAttributeBatchPaths)
	current := make([]pathsafe.RelativePath, 0, MaximumAttributeBatchPaths)
	currentStart := 0
	currentBytes := 0
	for _, entry := range entries {
		pathBytes := len(entry.path.String())
		if attributeBatchBoundsFit(currentBytes+pathBytes, len(current)+1) {
			current = append(current, entry.path)
			currentBytes += pathBytes
			continue
		}
		if len(current) == 0 {
			return nil, ErrExecutorContract
		}
		result = append(result, checkoutAttributePathRange{
			start: currentStart, end: currentStart + len(current), paths: current,
		})
		currentStart += len(current)
		current = []pathsafe.RelativePath{entry.path}
		currentBytes = pathBytes
		if !attributeBatchBoundsFit(currentBytes, 1) {
			return nil, ErrExecutorContract
		}
	}
	if len(current) != 0 {
		result = append(result, checkoutAttributePathRange{
			start: currentStart, end: currentStart + len(current), paths: current,
		})
	}
	return result, nil
}

func samePaths(left, right []pathsafe.RelativePath) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].String() != right[index].String() {
			return false
		}
	}
	return true
}

func cloneTreeInventory(inventory TreeInventory) TreeInventory {
	cloned := inventory
	cloned.entries = append([]TreeEntry(nil), inventory.entries...)
	return cloned
}

func cloneMaterializationPlan(plan MaterializationPlan) MaterializationPlan {
	cloned := plan
	cloned.inventory = cloneTreeInventory(plan.inventory)
	return cloned
}

func cloneCheckoutAttributeBatch(batch CheckoutAttributeBatch) CheckoutAttributeBatch {
	cloned := batch
	cloned.paths = append([]pathsafe.RelativePath(nil), batch.paths...)
	return cloned
}
