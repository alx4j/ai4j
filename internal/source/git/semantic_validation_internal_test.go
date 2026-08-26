package git

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/pathsafe"
	"github.com/alx4j/ai4j/internal/source/git/protocol"
)

func TestRemoteAdvertisementRejectsDuplicateAndInconsistentFacts(t *testing.T) {
	t.Parallel()

	request := mustResolutionRequest(t, "https://github.com/alx4j/ai4j.git", "", false)
	valid := "ref: refs/heads/main\tHEAD\n" + testObjectA + "\tHEAD\n" +
		testObjectA + "\trefs/heads/main\n" + testObjectB + "\trefs/tags/v1\n" +
		testObjectA + "\trefs/tags/v1^{}\n"
	advertisement, err := ParseRemoteAdvertisement(request, []byte(valid))
	if err != nil || !advertisement.Valid() {
		t.Fatalf("advertisement = %#v, %v", advertisement, err)
	}
	head, ok := advertisement.Head()
	defaultBranch, defaultOK := advertisement.DefaultBranch()
	refs := advertisement.References()
	if !ok || head.String() != testObjectA || !defaultOK || defaultBranch != "main" || len(refs) != 2 ||
		refs[0].Kind() != AdvertisedBranch || refs[1].Kind() != AdvertisedTag {
		t.Fatalf("advertisement facts = %#v", advertisement)
	}
	peeled, peeledOK := refs[1].PeeledOID()
	if !peeledOK || peeled.String() != testObjectA {
		t.Fatalf("peeled = %s, %t", peeled.String(), peeledOK)
	}
	refs[0] = AdvertisedReference{}
	if !advertisement.References()[0].Valid() {
		t.Fatal("reference accessor aliases advertisement")
	}

	for _, data := range []string{
		valid + testObjectA + "\trefs/heads/main\n",
		"ref: refs/heads/main\tHEAD\n" + testObjectA + "\tHEAD\n" + testObjectB + "\trefs/heads/main\n",
		"ref: refs/heads/missing\tHEAD\n" + testObjectA + "\tHEAD\n" + testObjectA + "\trefs/heads/main\n",
		testObjectA + "\trefs/tags/v1^{}\n",
		"ref: refs/tags/v1\tHEAD\n" + testObjectA + "\tHEAD\n" + testObjectA + "\trefs/tags/v1\n",
		strings.Repeat("0", 40) + "\trefs/heads/main\n",
		testObjectA + "\trefs/pull/1/head\n",
	} {
		if _, err := ParseRemoteAdvertisement(request, []byte(data)); !errors.Is(err, NewExecutorError(OperationEnumerateRefs, FailureMalformedProtocol)) {
			t.Errorf("inconsistent advertisement error = %v", err)
		}
	}
}

func TestRemoteAdvertisementFormattingIsNonDataBearing(t *testing.T) {
	t.Parallel()

	request := mustResolutionRequest(t, "https://github.com/alx4j/ai4j.git", "canary-secret", true)
	advertisement, err := ParseRemoteAdvertisement(request, []byte(testObjectA+"\trefs/heads/canary-secret\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, rendered := range []string{fmt.Sprintf("%v", advertisement), fmt.Sprintf("%+v", advertisement), fmt.Sprintf("%#v", advertisement)} {
		if rendered != "<git-remote-advertisement:redacted>" || strings.Contains(rendered, "canary") {
			t.Fatalf("rendered = %q", rendered)
		}
	}
	encoded, _ := json.Marshal(advertisement)
	if strings.Contains(string(encoded), "canary") {
		t.Fatalf("JSON = %s", encoded)
	}
}

func TestResolveReferenceProvesDefaultQualifiedCommitAndShortNameSemantics(t *testing.T) {
	t.Parallel()

	data := "ref: refs/heads/main\tHEAD\n" + testObjectA + "\tHEAD\n" +
		testObjectA + "\trefs/heads/main\n" + testObjectB + "\trefs/heads/feature\n" +
		testObjectA + "\trefs/heads/shared\n" + testObjectB + "\trefs/tags/v1\n" +
		testObjectB + "\trefs/tags/shared\n"
	tests := []struct {
		name      string
		requested string
		provided  bool
		kind      ResolvedReferenceKind
		resolved  string
		object    string
	}{
		{"omitted default", "", false, ResolvedDefaultBranch, "main", testObjectA},
		{"qualified branch", "refs/heads/feature", true, ResolvedBranch, "feature", testObjectB},
		{"qualified tag", "refs/tags/v1", true, ResolvedTag, "v1", testObjectB},
		{"short branch", "feature", true, ResolvedBranch, "feature", testObjectB},
		{"short tag", "v1", true, ResolvedTag, "v1", testObjectB},
		{"full commit", testObjectA, true, ResolvedCommit, testObjectA, testObjectA},
	}
	for _, test := range tests {
		request := mustResolutionRequest(t, "https://github.com/alx4j/ai4j.git", test.requested, test.provided)
		advertisement := mustRemoteAdvertisement(t, request, data)
		selection, err := ResolveReference(request, advertisement)
		resolved := selection.Resolved()
		if err != nil || !selection.Valid() || resolved.Kind() != test.kind || resolved.Name() != test.resolved ||
			selection.SelectedObject().String() != test.object || !sameResolutionRequest(selection.Request(), request) {
			t.Errorf("%s resolved = %#v, %v", test.name, selection, err)
		}
		value, provided := request.RequestedReference().Value()
		if value != test.requested || provided != test.provided {
			t.Errorf("%s request changed = %q, %t", test.name, value, provided)
		}
	}

	ambiguousRequest := mustResolutionRequest(t, "https://github.com/alx4j/ai4j.git", "shared", true)
	if _, err := ResolveReference(ambiguousRequest, mustRemoteAdvertisement(t, ambiguousRequest, data)); !errors.Is(err, NewExecutorError(OperationEnumerateRefs, FailureReferenceAmbiguous)) {
		t.Fatalf("ambiguous error = %v", err)
	}
	missingRequest := mustResolutionRequest(t, "https://github.com/alx4j/ai4j.git", "missing", true)
	if _, err := ResolveReference(missingRequest, mustRemoteAdvertisement(t, missingRequest, data)); !errors.Is(err, NewExecutorError(OperationEnumerateRefs, FailureReferenceNotFound)) {
		t.Fatalf("missing error = %v", err)
	}
	qualifiedMissing := mustResolutionRequest(t, "https://github.com/alx4j/ai4j.git", "refs/heads/v1", true)
	if _, err := ResolveReference(qualifiedMissing, mustRemoteAdvertisement(t, qualifiedMissing, data)); !errors.Is(err, NewExecutorError(OperationEnumerateRefs, FailureReferenceNotFound)) {
		t.Fatalf("qualified kind confusion error = %v", err)
	}
	omitted := mustResolutionRequest(t, "https://github.com/alx4j/ai4j.git", "", false)
	withoutDefault, err := ParseRemoteAdvertisement(omitted, []byte(testObjectA+"\trefs/heads/main\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveReference(omitted, withoutDefault); !errors.Is(err, NewExecutorError(OperationEnumerateRefs, FailureDefaultBranchUnavailable)) {
		t.Fatalf("default error = %v", err)
	}
}

func TestReferenceResolutionBindsRequestAdvertisementAndSelectedObject(t *testing.T) {
	t.Parallel()

	request := mustResolutionRequest(t, "https://github.com/alx4j/ai4j.git", "main", true)
	firstData := testObjectA + "\trefs/heads/main\n"
	secondData := testObjectB + "\trefs/heads/main\n"
	first, err := ResolveReference(request, mustRemoteAdvertisement(t, request, firstData))
	if err != nil || !first.Valid() || first.SelectedObject().String() != testObjectA {
		t.Fatalf("first selection = %#v, %v", first, err)
	}
	second, err := ResolveReference(request, mustRemoteAdvertisement(t, request, secondData))
	if err != nil || !second.Valid() || second.SelectedObject().String() != testObjectB {
		t.Fatalf("second selection = %#v, %v", second, err)
	}
	auth := mustAuthentication(t, request, AuthenticationAnonymousHTTPS)
	firstFetch, err := NewFetchCommand(first, auth)
	if err != nil {
		t.Fatal(err)
	}
	secondFetch, err := NewFetchCommand(second, auth)
	if err != nil {
		t.Fatal(err)
	}
	if got := firstFetch.Arguments()[len(firstFetch.Arguments())-1]; got != "+"+testObjectA+":refs/ai4j/acquired" {
		t.Fatalf("first refspec = %q", got)
	}
	if got := secondFetch.Arguments()[len(secondFetch.Arguments())-1]; got != "+"+testObjectB+":refs/ai4j/acquired" {
		t.Fatalf("second refspec = %q", got)
	}

	otherRepository := mustResolutionRequest(t, "https://github.com/other/repository.git", "main", true)
	if _, err := ResolveReference(otherRepository, mustRemoteAdvertisement(t, request, firstData)); !errors.Is(err, NewExecutorError(OperationEnumerateRefs, FailureInvalidOperation)) {
		t.Fatalf("cross-repository advertisement error = %v", err)
	}
	otherTransport := mustResolutionRequest(t, "git@github.com:alx4j/ai4j.git", "main", true)
	if _, err := ResolveReference(otherTransport, mustRemoteAdvertisement(t, request, firstData)); !errors.Is(err, NewExecutorError(OperationEnumerateRefs, FailureInvalidOperation)) {
		t.Fatalf("cross-transport advertisement error = %v", err)
	}
	otherReference := mustResolutionRequest(t, "https://github.com/alx4j/ai4j.git", "other", true)
	if _, err := ResolveReference(otherReference, mustRemoteAdvertisement(t, request, firstData)); !errors.Is(err, NewExecutorError(OperationEnumerateRefs, FailureInvalidOperation)) {
		t.Fatalf("cross-reference advertisement error = %v", err)
	}

	for _, mutate := range []func(*ReferenceResolution){
		func(value *ReferenceResolution) { value.request = otherRepository },
		func(value *ReferenceResolution) { value.resolved, _ = NewResolvedReference(ResolvedBranch, "other") },
		func(value *ReferenceResolution) { value.object, _ = NewGitObjectOID(testObjectB) },
		func(value *ReferenceResolution) { value.seal = nil },
	} {
		changed := first
		mutate(&changed)
		if changed.Valid() {
			t.Fatalf("tampered selection is valid: %#v", changed)
		}
		if _, err := NewFetchCommand(changed, auth); !errors.Is(err, ErrExecutorContract) {
			t.Fatalf("tampered fetch error = %v", err)
		}
	}
	copyValue := first
	if !copyValue.Valid() || copyValue.SelectedObject() != first.SelectedObject() {
		t.Fatal("copied selection lost its proof")
	}
	for _, rendered := range []string{fmt.Sprintf("%v", first), fmt.Sprintf("%+v", first), fmt.Sprintf("%#v", first)} {
		if rendered != "<git-reference-resolution:redacted>" || strings.Contains(rendered, testObjectA) {
			t.Fatalf("selection render = %q", rendered)
		}
	}
	encoded, _ := json.Marshal(first)
	if strings.Contains(string(encoded), testObjectA) || strings.Contains(string(encoded), "github.com") {
		t.Fatalf("selection JSON = %s", encoded)
	}
}

func TestSelectedObjectCommitTreeAndProvenanceFormOneSealedChain(t *testing.T) {
	t.Parallel()

	branchRequest := mustResolutionRequest(t, "https://github.com/alx4j/ai4j.git", "main", true)
	selectionA, err := ResolveReference(
		branchRequest,
		mustRemoteAdvertisement(t, branchRequest, testObjectA+"\trefs/heads/main\n"),
	)
	if err != nil {
		t.Fatal(err)
	}
	selectionB, err := ResolveReference(
		branchRequest,
		mustRemoteAdvertisement(t, branchRequest, testObjectB+"\trefs/heads/main\n"),
	)
	if err != nil {
		t.Fatal(err)
	}
	objectCommand, err := NewObjectTypeCommand(selectionA)
	if err != nil || objectCommand.Arguments()[len(objectCommand.Arguments())-1] != testObjectA {
		t.Fatalf("selected-object command = %#v, %v", objectCommand, err)
	}
	selectedA, err := NewSelectedObjectProof(selectionA, []byte("commit\n"))
	if err != nil || !selectedA.Valid() || selectedA.Object().String() != testObjectA {
		t.Fatalf("selected A = %#v, %v", selectedA, err)
	}
	selectedB, err := NewSelectedObjectProof(selectionB, []byte("commit\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, rejectedType := range []string{"tag\n", "tree\n", "blob\n", "commit\x00\n"} {
		if _, err := NewSelectedObjectProof(selectionA, []byte(rejectedType)); err == nil {
			t.Errorf("branch selected object type %q accepted", rejectedType)
		}
	}
	commitA, err := NewDirectProvenCommit(selectedA)
	if err != nil || !commitA.Valid() || commitA.Commit().String() != testObjectA {
		t.Fatalf("commit A = %#v, %v", commitA, err)
	}
	commitB, err := NewDirectProvenCommit(selectedB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewPeelCommitCommand(selectedA); !errors.Is(err, ErrExecutorContract) {
		t.Fatalf("direct commit peel command error = %v", err)
	}
	if _, err := NewPeeledProvenCommit(selectedA, []byte(testObjectB+"\n")); !errors.Is(err, NewExecutorError(OperationPeelCommit, FailureInvalidOperation)) {
		t.Fatalf("direct commit peel proof error = %v", err)
	}

	tagRequest := mustResolutionRequest(t, "https://github.com/alx4j/ai4j.git", "refs/tags/v1", true)
	tagResolution, err := ResolveReference(
		tagRequest,
		mustRemoteAdvertisement(t, tagRequest, testObjectA+"\trefs/tags/v1\n"),
	)
	if err != nil {
		t.Fatal(err)
	}
	lightweight, err := NewSelectedObjectProof(tagResolution, []byte("commit\n"))
	if err != nil {
		t.Fatal(err)
	}
	lightweightCommit, err := NewDirectProvenCommit(lightweight)
	if err != nil || lightweightCommit.Commit().String() != testObjectA {
		t.Fatalf("lightweight commit = %#v, %v", lightweightCommit, err)
	}
	annotated, err := NewSelectedObjectProof(tagResolution, []byte("tag\n"))
	if err != nil || !annotated.Valid() {
		t.Fatalf("annotated tag = %#v, %v", annotated, err)
	}
	if _, err := NewDirectProvenCommit(annotated); !errors.Is(err, NewExecutorError(OperationObjectType, FailureInvalidOperation)) {
		t.Fatalf("annotated direct commit error = %v", err)
	}
	peelCommand, err := NewPeelCommitCommand(annotated)
	if err != nil || peelCommand.Arguments()[len(peelCommand.Arguments())-1] != testObjectA+"^{commit}" {
		t.Fatalf("peel command = %#v, %v", peelCommand, err)
	}
	peeled, err := NewPeeledProvenCommit(annotated, []byte(testObjectB+"\n"))
	if err != nil || !peeled.Valid() || peeled.Commit().String() != testObjectB {
		t.Fatalf("peeled commit = %#v, %v", peeled, err)
	}
	annotatedTree, err := NewCommitTreeProof(peeled, []byte(testObjectA+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	annotatedProvenance, err := NewSourceProvenance(annotatedTree)
	if err != nil || annotatedProvenance.ResolvedReference().Kind() != ResolvedTag ||
		annotatedProvenance.Commit().OID() != peeled.Commit() || annotatedProvenance.TrackingPolicy() != TrackPinned {
		t.Fatalf("annotated provenance = %#v, %v", annotatedProvenance, err)
	}
	for _, nonCommit := range []string{"blob\n", "tree\n"} {
		if _, err := NewSelectedObjectProof(tagResolution, []byte(nonCommit)); !errors.Is(err, NewExecutorError(OperationObjectType, FailurePolicyRejected)) {
			t.Errorf("tag noncommit type %q error = %v", nonCommit, err)
		}
	}

	treeProof, err := NewCommitTreeProof(commitA, []byte(testObjectB+"\n"))
	if err != nil || !treeProof.Valid() || treeProof.Commit() != commitA.Commit() || treeProof.Tree().String() != testObjectB {
		t.Fatalf("tree proof = %#v, %v", treeProof, err)
	}
	if command, err := NewCommitTreeCommand(commitA); err != nil || command.Arguments()[len(command.Arguments())-1] != testObjectA+"^{tree}" {
		t.Fatalf("tree command = %#v, %v", command, err)
	}
	inventory, err := ParseTreeInventory(treeProof.Tree(), []byte(treeRecord("100644", "blob", testObjectA, 1, "file")))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewMaterializationPlan(treeProof, inventory)
	if err != nil || !plan.Valid() {
		t.Fatalf("plan = %#v, %v", plan, err)
	}
	provenance, err := NewSourceProvenance(treeProof)
	if err != nil || !provenance.Valid() || provenance.Commit().OID() != commitA.Commit() ||
		provenance.RootTree() != treeProof.Tree() || provenance.ResolvedReference() != selectionA.Resolved() {
		t.Fatalf("provenance = %#v, %v", provenance, err)
	}

	changedSelected := selectedA
	changedSelected.resolution = selectionB
	if changedSelected.Valid() {
		t.Fatal("selected-object A/B substitution is valid")
	}
	changedCommit := commitA
	changedCommit.selected = selectedB
	if changedCommit.Valid() {
		t.Fatal("proven-commit A/B substitution is valid")
	}
	changedCommit = commitA
	changedCommit.commit = commitB.Commit()
	if changedCommit.Valid() {
		t.Fatal("proven-commit OID substitution is valid")
	}
	changedTree := treeProof
	changedTree.commit = commitB
	if changedTree.Valid() {
		t.Fatal("commit-tree A/B substitution is valid")
	}
	changedProvenance := provenance
	changedProvenance.proof = changedTree
	if changedProvenance.Valid() {
		t.Fatal("provenance A/B substitution is valid")
	}
	for _, mutate := range []func(*SourceProvenance){
		func(value *SourceProvenance) { value.requested = annotatedProvenance.requested },
		func(value *SourceProvenance) { value.resolved = annotatedProvenance.resolved },
		func(value *SourceProvenance) { value.commit = annotatedProvenance.commit },
		func(value *SourceProvenance) { value.tree = annotatedProvenance.tree },
		func(value *SourceProvenance) { value.tracking = TrackPinned },
	} {
		changed := provenance
		mutate(&changed)
		if changed.Valid() {
			t.Fatal("independent provenance substitution is valid")
		}
	}
	if _, err := NewCommitTreeProof(ProvenCommit{}, []byte(testObjectB+"\n")); err == nil {
		t.Fatal("zero commit produced a tree proof")
	}
	if _, err := NewMaterializationPlan(CommitTreeProof{}, inventory); !errors.Is(err, ErrExecutorContract) {
		t.Fatalf("zero proof plan error = %v", err)
	}
	if _, err := NewSourceProvenance(CommitTreeProof{}); !errors.Is(err, ErrInvalidSourceProvenance) {
		t.Fatalf("zero proof provenance error = %v", err)
	}
	for _, value := range []any{selectedA, commitA, treeProof} {
		for _, rendered := range []string{fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
			if !strings.Contains(rendered, "redacted") || strings.Contains(rendered, testObjectA) {
				t.Fatalf("chain proof render = %q", rendered)
			}
		}
	}
}

func TestTreeInventoryAcceptsOnlyRegularCollisionFreeBlobs(t *testing.T) {
	t.Parallel()

	tree := mustTreeOID(t, testObjectB)
	data := treeRecord("100755", "blob", testObjectB, 7, "bin/tool") +
		treeRecord("100644", "blob", testObjectA, 0, "README.md")
	inventory, err := ParseTreeInventory(tree, []byte(data))
	if err != nil || !inventory.Valid() {
		t.Fatalf("inventory = %#v, %v", inventory, err)
	}
	entries := inventory.Entries()
	if inventory.Tree() != tree || inventory.PathCount() != 2 || inventory.TreeBytes() != 7 ||
		inventory.PathBytes() != uint64(len("bin/tool")+len("README.md")) ||
		entries[0].Path().String() != "README.md" || entries[1].Mode() != SourceExecutableFile {
		t.Fatalf("inventory facts = %#v", inventory)
	}
	entries[0] = TreeEntry{}
	if !inventory.Entries()[0].Valid() {
		t.Fatal("entries accessor aliases inventory")
	}
	for _, rendered := range []string{fmt.Sprintf("%v", inventory), fmt.Sprintf("%+v", inventory), fmt.Sprintf("%#v", inventory)} {
		if rendered != "<git-tree-inventory:redacted>" || strings.Contains(rendered, "README") {
			t.Fatalf("inventory render = %q", rendered)
		}
	}
}

func TestMaterializationPlanBindsCommitTreeAndInventory(t *testing.T) {
	t.Parallel()

	commit := mustCommitOID(t, testObjectA)
	proven := mustDirectProvenCommit(t, testObjectA)
	tree := mustTreeOID(t, testObjectB)
	proof, err := NewCommitTreeProof(proven, []byte(testObjectB+"\n"))
	if err != nil || !proof.Valid() || proof.Commit() != commit || proof.Tree() != tree {
		t.Fatalf("proof = %#v, %v", proof, err)
	}
	inventory, err := ParseTreeInventory(tree, []byte(treeRecord("100644", "blob", testObjectA, 1, "file")))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewMaterializationPlan(proof, inventory)
	if err != nil || !plan.Valid() || plan.Commit() != commit || plan.Tree() != tree {
		t.Fatalf("plan = %#v, %v", plan, err)
	}
	budget := DefaultWorkspaceBudget()
	if !budget.AllowsMaterialization(MaximumWorkspaceBytes-1, inventory) ||
		budget.AllowsMaterialization(MaximumWorkspaceBytes, inventory) {
		t.Fatal("workspace plus selected-tree arithmetic is incoherent")
	}
	copyInventory := plan.Inventory()
	copyInventory.entries[0] = TreeEntry{}
	if !plan.Inventory().Valid() {
		t.Fatal("materialization plan aliases inventory")
	}
	otherTree := mustTreeOID(t, testObjectA)
	otherInventory, err := ParseTreeInventory(otherTree, []byte(treeRecord("100644", "blob", testObjectA, 1, "file")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewMaterializationPlan(proof, otherInventory); !errors.Is(err, ErrExecutorContract) {
		t.Fatalf("mismatched plan error = %v", err)
	}
	if err := proof.Matches([]byte(testObjectB + "\n")); err != nil {
		t.Fatal(err)
	}
	if err := proof.Matches([]byte(testObjectA + "\n")); !errors.Is(err, NewExecutorError(OperationCommitTree, FailureRepositoryConflict)) {
		t.Fatalf("tree drift error = %v", err)
	}
	for _, value := range []any{proof, plan} {
		for _, rendered := range []string{fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
			if strings.Contains(rendered, testObjectA) || strings.Contains(rendered, testObjectB) || !strings.Contains(rendered, "redacted") {
				t.Fatalf("rendered proof/plan = %q", rendered)
			}
		}
	}
}

func TestCheckoutRequiresCompleteExactPlanBoundAttributeCoverage(t *testing.T) {
	t.Parallel()

	tree := mustTreeOID(t, testObjectB)
	var records strings.Builder
	for index := 0; index < MaximumAttributeBatchPaths+1; index++ {
		oid := testObjectA
		if index%2 != 0 {
			oid = testObjectB
		}
		records.WriteString(treeRecord("100644", "blob", oid, 1, fmt.Sprintf("file-%03d", index)))
	}
	inventory, err := ParseTreeInventory(tree, []byte(records.String()))
	if err != nil {
		t.Fatal(err)
	}
	plan := mustMaterializationPlan(t, inventory, testObjectA)
	batches, err := PlanCheckoutAttributeBatches(plan)
	if err != nil || len(batches) != 2 || len(batches[0].Paths()) != MaximumAttributeBatchPaths ||
		len(batches[1].Paths()) != 1 {
		t.Fatalf("batches = %#v, %v", batches, err)
	}
	proofs := make([]CheckoutAttributeBatchProof, len(batches))
	for index, batch := range batches {
		proofs[index], err = ValidateCheckoutAttributes(batch, checkoutAttributeOutput(batch, "", "", ""))
		if err != nil || !proofs[index].Valid() {
			t.Fatalf("proof %d = %#v, %v", index, proofs[index], err)
		}
	}

	if _, err := CompleteCheckoutAttributeCoverage(plan, proofs[:1]); !errors.Is(err, ErrExecutorContract) {
		t.Fatalf("omitted batch error = %v", err)
	}
	if _, err := CompleteCheckoutAttributeCoverage(plan, []CheckoutAttributeBatchProof{proofs[0], proofs[0]}); !errors.Is(err, ErrExecutorContract) {
		t.Fatalf("duplicate batch error = %v", err)
	}
	if _, err := CompleteCheckoutAttributeCoverage(plan, []CheckoutAttributeBatchProof{proofs[1], proofs[0]}); !errors.Is(err, ErrExecutorContract) {
		t.Fatalf("reordered batch error = %v", err)
	}
	otherPlan := mustMaterializationPlan(t, inventory, testObjectA)
	otherBatches, err := PlanCheckoutAttributeBatches(otherPlan)
	if err != nil {
		t.Fatal(err)
	}
	otherProof, err := ValidateCheckoutAttributes(otherBatches[1], checkoutAttributeOutput(otherBatches[1], "", "", ""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CompleteCheckoutAttributeCoverage(plan, []CheckoutAttributeBatchProof{proofs[0], otherProof}); !errors.Is(err, ErrExecutorContract) {
		t.Fatalf("mixed-plan batch error = %v", err)
	}

	uncovered := batches[1].Paths()[0].String()
	if _, err := ValidateCheckoutAttributes(
		batches[1], checkoutAttributeOutput(batches[1], uncovered, "working-tree-encoding", "utf-16"),
	); !errors.Is(err, NewExecutorError(OperationCheckAttributes, FailurePolicyRejected)) {
		t.Fatalf("uncovered transformed path error = %v", err)
	}
	if _, err := CompleteCheckoutAttributeCoverage(plan, proofs[:1]); !errors.Is(err, ErrExecutorContract) {
		t.Fatalf("transformed omission error = %v", err)
	}

	approval, err := CompleteCheckoutAttributeCoverage(plan, proofs)
	if err != nil || !approval.Valid() {
		t.Fatalf("approval = %#v, %v", approval, err)
	}
	if command, err := NewCheckoutDetachedCommand(approval); err != nil || !command.Valid() {
		t.Fatalf("checkout = %#v, %v", command, err)
	}
	pathsCopy := batches[0].Paths()
	pathsCopy[0] = pathsafe.RelativePath{}
	if !batches[0].Valid() || !batches[0].Paths()[0].Valid() {
		t.Fatal("batch path accessor aliases issued batch")
	}
	mutableBatch := cloneCheckoutAttributeBatch(batches[0])
	proofCopy, err := ValidateCheckoutAttributes(mutableBatch, checkoutAttributeOutput(mutableBatch, "", "", ""))
	if err != nil {
		t.Fatal(err)
	}
	mutableBatch.paths[0] = pathsafe.RelativePath{}
	if !proofCopy.Valid() {
		t.Fatal("proof aliases caller batch storage")
	}
	planCopy := approval.Plan()
	planCopy.inventory.entries[0] = TreeEntry{}
	if !approval.Valid() || !approval.Plan().Inventory().Entries()[0].Valid() {
		t.Fatal("approval plan accessor aliases issued approval")
	}
	for _, value := range []any{batches[0], proofs[0], approval} {
		for _, rendered := range []string{fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
			if !strings.Contains(rendered, "redacted") || strings.Contains(rendered, "file-") {
				t.Fatalf("attribute proof render = %q", rendered)
			}
		}
	}
}

func TestCheckoutAttributeCoverageHandlesZeroAndEmptyInventory(t *testing.T) {
	t.Parallel()

	if (CheckoutAttributeBatch{}).Valid() || (CheckoutAttributeBatchProof{}).Valid() || (CheckoutApproval{}).Valid() {
		t.Fatal("zero attribute proof state is valid")
	}
	if _, err := NewCheckAttributesCommand(CheckoutAttributeBatch{}); !errors.Is(err, ErrExecutorContract) {
		t.Fatalf("zero batch command error = %v", err)
	}
	if _, err := NewCheckoutDetachedCommand(CheckoutApproval{}); !errors.Is(err, ErrExecutorContract) {
		t.Fatalf("zero approval checkout error = %v", err)
	}
	if _, err := NewCheckoutIndexCommand(CheckoutApproval{}); !errors.Is(err, ErrExecutorContract) {
		t.Fatalf("zero approval checkout-index error = %v", err)
	}

	tree := mustTreeOID(t, testObjectB)
	inventory, err := ParseTreeInventory(tree, nil)
	if err != nil || !inventory.Valid() || inventory.PathCount() != 0 {
		t.Fatalf("empty inventory = %#v, %v", inventory, err)
	}
	plan := mustMaterializationPlan(t, inventory, testObjectA)
	batches, err := PlanCheckoutAttributeBatches(plan)
	if err != nil || batches == nil || len(batches) != 0 {
		t.Fatalf("empty batches = %#v, %v", batches, err)
	}
	approval, err := CompleteCheckoutAttributeCoverage(plan, []CheckoutAttributeBatchProof{})
	if err != nil || !approval.Valid() {
		t.Fatalf("empty approval = %#v, %v", approval, err)
	}
}

func TestCheckoutAttributePlanningRemainsLinearAtClosedMaximum(t *testing.T) {
	tree := mustTreeOID(t, testObjectB)
	var records strings.Builder
	for index := 0; index < MaximumInventoryPathCount; index++ {
		records.WriteString(treeRecord("100644", "blob", testObjectA, 1, fmt.Sprintf("files/%05d", index)))
	}
	inventory, err := ParseTreeInventory(tree, []byte(records.String()))
	if err != nil {
		t.Fatal(err)
	}
	plan := mustMaterializationPlan(t, inventory, testObjectA)
	baselineAllocations := testing.AllocsPerRun(1, func() {
		if !plan.Valid() {
			panic("maximum plan became invalid")
		}
	})
	planningAllocations := testing.AllocsPerRun(1, func() {
		batches, planErr := PlanCheckoutAttributeBatches(plan)
		if planErr != nil || len(batches) == 0 {
			panic("maximum attribute partition failed")
		}
	})
	if overhead := planningAllocations - baselineAllocations; overhead > MaximumInventoryPathCount*5 {
		t.Fatalf("maximum partition incremental allocations = %.0f (total %.0f, baseline %.0f)",
			overhead, planningAllocations, baselineAllocations)
	}

	started := time.Now()
	batches, err := PlanCheckoutAttributeBatches(plan)
	if err != nil {
		t.Fatal(err)
	}
	proofs := make([]CheckoutAttributeBatchProof, len(batches))
	totalPaths := 0
	for index, batch := range batches {
		paths := batch.Paths()
		totalPaths += len(paths)
		if len(paths) == 0 || len(paths) > MaximumAttributeBatchPaths || !batch.Valid() {
			t.Fatalf("batch %d size = %d", index, len(paths))
		}
		proofs[index], err = ValidateCheckoutAttributes(batch, checkoutAttributeOutput(batch, "", "", ""))
		if err != nil {
			t.Fatal(err)
		}
	}
	if totalPaths != MaximumInventoryPathCount {
		t.Fatalf("covered paths = %d", totalPaths)
	}
	if approval, err := CompleteCheckoutAttributeCoverage(plan, proofs); err != nil || !approval.Valid() {
		t.Fatalf("maximum approval = %#v, %v", approval, err)
	}
	if elapsed := time.Since(started); elapsed > 30*time.Second {
		t.Fatalf("maximum coverage took %s", elapsed)
	}
}

func TestTreeInventoryRejectsUnsafeKindsModesAndResourceLimits(t *testing.T) {
	t.Parallel()

	tree := mustTreeOID(t, testObjectB)
	for _, data := range []string{
		treeRecord("120000", "blob", testObjectA, 1, "link"),
		treeRecordWithoutSize("160000", "commit", testObjectA, "submodule"),
		treeRecordWithoutSize("040000", "tree", testObjectA, "directory"),
		treeRecord("100664", "blob", testObjectA, 1, "group-writable"),
		treeRecord("100644", "blob", strings.Repeat("0", 40), 1, "zero-oid"),
		treeRecord("100644", "blob", testObjectA, MaximumBlobBytes+1, "large"),
	} {
		if _, err := ParseTreeInventory(tree, []byte(data)); err == nil {
			t.Errorf("unsafe inventory accepted: %q", data)
		}
	}

	var aggregate strings.Builder
	for index := 0; index < 9; index++ {
		aggregate.WriteString(treeRecord("100644", "blob", testObjectA, MaximumBlobBytes, fmt.Sprintf("file-%d", index)))
	}
	if _, err := ParseTreeInventory(tree, []byte(aggregate.String())); !errors.Is(err, NewExecutorError(OperationListTree, FailureResourceLimit)) {
		t.Fatalf("aggregate tree error = %v", err)
	}
}

func TestTreeInventoryRejectsEquivalentAndAncestorLeafCollisions(t *testing.T) {
	t.Parallel()

	tree := mustTreeOID(t, testObjectB)
	for _, paths := range [][2]string{
		{"A", "a"},
		{"caf\u00e9", "cafe\u0301"},
		{"A", "a/child"},
		{"a/child", "A"},
	} {
		data := treeRecord("100644", "blob", testObjectA, 1, paths[0]) +
			treeRecord("100644", "blob", testObjectB, 1, paths[1])
		if _, err := ParseTreeInventory(tree, []byte(data)); !errors.Is(err, NewExecutorError(OperationListTree, FailurePolicyRejected)) {
			t.Errorf("collision %q/%q error = %v", paths[0], paths[1], err)
		}
	}
}

func TestTreeInventoryReservesGitMetadataAliasesInEveryAncestor(t *testing.T) {
	t.Parallel()

	tree := mustTreeOID(t, testObjectB)
	for _, path := range []string{
		".git/config", ".GIT/config", ".g\u200bit/config", ".git~1/config", "GIT~1/config", "nested/.GiT/objects/x",
	} {
		data := treeRecord("100644", "blob", testObjectA, 1, path)
		if _, err := ParseTreeInventory(tree, []byte(data)); !errors.Is(err, NewExecutorError(OperationListTree, FailurePolicyRejected)) {
			t.Errorf("reserved path %q error = %v", path, err)
		}
	}
	for _, path := range []string{"git/config", ".github/workflows/check.yml", "nested/git~0/file"} {
		data := treeRecord("100644", "blob", testObjectA, 1, path)
		if inventory, err := ParseTreeInventory(tree, []byte(data)); err != nil || !inventory.Valid() {
			t.Errorf("benign path %q rejected: %v", path, err)
		}
	}
}

func TestTreeAndAttributeProtocolOverheadProofs(t *testing.T) {
	if MaximumReferenceBytes != protocol.MaximumRemoteReferenceBytes ||
		remoteOutputLimit != protocol.MaximumRemoteOutputBytes || protocolOutputLimit != protocol.MaximumTreeOutputBytes ||
		attributeOutputLimit != protocol.MaximumAttributeOutputBytes || configurationOutputLimit != protocol.MaximumConfigOutputBytes {
		t.Fatal("command and parser bounds drifted")
	}
	if !treeProtocolFits(MaximumInventoryPathBytes, MaximumInventoryPathCount) {
		t.Fatal("published inventory boundary does not fit tree capture")
	}
	if treeProtocolFits(protocol.MaximumTreeOutputBytes, 1) {
		t.Fatal("path-only stream ignored record overhead")
	}
	if got := uint64(MaximumInventoryPathBytes) + uint64(MaximumInventoryPathCount)*maximumTreeRecordOverheadBytes; got >= protocol.MaximumTreeOutputBytes {
		t.Fatalf("tree boundary %d does not fit %d", got, protocol.MaximumTreeOutputBytes)
	}

	pathOnlyBoundary := make([]pathsafe.RelativePath, MaximumAttributeBatchPaths)
	for index := range pathOnlyBoundary {
		spelling := fmt.Sprintf("%03d/", index) + strings.Repeat("a", 254) + "/" +
			strings.Repeat("b", 254) + "/" + strings.Repeat("c", 254) + "/" + strings.Repeat("d", 255)
		path, err := pathsafe.NewRelativePath(spelling)
		if err != nil {
			t.Fatal(err)
		}
		pathOnlyBoundary[index] = path
	}
	if _, err := validateAttributeBatch(pathOnlyBoundary); !errors.Is(err, ErrExecutorContract) {
		t.Fatalf("path-only 128KiB boundary did not account for fixed argv: %v", err)
	}

	paths := make([]pathsafe.RelativePath, MaximumAttributeBatchPaths)
	for index := range paths {
		spelling := fmt.Sprintf("%03d/", index) + strings.Repeat("a", 238) + "/" +
			strings.Repeat("b", 238) + "/" + strings.Repeat("c", 238) + "/" + strings.Repeat("d", 239)
		paths[index] = mustRelativePath(t, spelling)
	}
	validated, err := validateAttributeBatch(paths)
	if err != nil || len(validated) != MaximumAttributeBatchPaths {
		t.Fatalf("attribute boundary = %d, %v", len(validated), err)
	}
}

func TestRepositoryConfigurationAuditIsClosed(t *testing.T) {
	t.Parallel()

	data := []byte("core.repositoryformatversion\n0\x00core.filemode\ntrue\x00core.bare\nfalse\x00" +
		"core.logallrefupdates\ntrue\x00core.ignorecase\ntrue\x00core.precomposeunicode\ntrue\x00core.symlinks\nfalse\x00")
	configuration, err := AuditLocalConfiguration(data)
	if err != nil || !configuration.Valid() {
		t.Fatalf("configuration = %#v, %v", configuration, err)
	}
	if value, present := configuration.IgnoreCase(); !present || !value {
		t.Fatalf("ignorecase = %t, %t", value, present)
	}
	if value, present := configuration.Symlinks(); !present || value {
		t.Fatalf("symlinks = %t, %t", value, present)
	}
	for _, invalid := range [][]byte{
		nil,
		[]byte("core.repositoryformatversion\n1\x00core.filemode\ntrue\x00core.bare\nfalse\x00core.logallrefupdates\ntrue\x00"),
		[]byte("core.repositoryformatversion\n0\x00core.filemode\ntrue\x00core.bare\nfalse\x00core.logallrefupdates\ntrue\x00remote.origin.url\nhttps://github.com/canary/secret\x00"),
		[]byte("core.repositoryformatversion\n0\x00core.repositoryformatversion\n0\x00core.filemode\ntrue\x00core.bare\nfalse\x00core.logallrefupdates\ntrue\x00"),
		[]byte("core.repositoryformatversion\n0\x00core.filemode\ntrue\x00core.bare\nfalse\x00core.logallrefupdates\ntrue\x00core.ignorecase\nfalse\x00"),
		[]byte("core.repositoryformatversion\n0\x00core.filemode\ntrue\x00core.bare\nfalse\x00core.logallrefupdates\ntrue\x00core.precomposeunicode\nfalse\x00"),
		[]byte("core.repositoryformatversion\n0\x00core.filemode\ntrue\x00core.bare\nfalse\x00core.logallrefupdates\ntrue\x00core.symlinks\ntrue\x00"),
	} {
		if _, err := AuditLocalConfiguration(invalid); err == nil {
			t.Errorf("unsafe config accepted: %q", invalid)
		}
	}
	for _, rendered := range []string{fmt.Sprintf("%v", configuration), fmt.Sprintf("%+v", configuration), fmt.Sprintf("%#v", configuration)} {
		if rendered != "<git-local-configuration:redacted>" {
			t.Fatalf("config render = %q", rendered)
		}
	}
}

func TestIndexAttributesAndStatusMustMatchInventory(t *testing.T) {
	t.Parallel()

	tree := mustTreeOID(t, testObjectB)
	inventory, err := ParseTreeInventory(tree, []byte(
		treeRecord("100644", "blob", testObjectA, 1, "a.txt")+
			treeRecord("100755", "blob", testObjectB, 2, "bin/tool"),
	))
	if err != nil {
		t.Fatal(err)
	}
	validIndex := []byte("100644 " + testObjectA + " 0\ta.txt\x00" + "100755 " + testObjectB + " 0\tbin/tool\x00")
	if err := ValidateIndex(inventory, validIndex); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]byte{
		[]byte("100644 " + testObjectA + " 1\ta.txt\x00" + "100755 " + testObjectB + " 0\tbin/tool\x00"),
		[]byte("100644 " + testObjectB + " 0\ta.txt\x00" + "100755 " + testObjectA + " 0\tbin/tool\x00"),
		[]byte("100644 " + testObjectA + " 0\ta.txt\x00"),
	} {
		if err := ValidateIndex(inventory, invalid); err == nil {
			t.Errorf("invalid index accepted: %q", invalid)
		}
	}

	plan := mustMaterializationPlan(t, inventory, testObjectA)
	batches, err := PlanCheckoutAttributeBatches(plan)
	if err != nil || len(batches) != 1 || !batches[0].Valid() {
		t.Fatalf("attribute batches = %#v, %v", batches, err)
	}
	paths := batches[0].Paths()
	var attributes strings.Builder
	for _, pathValue := range paths {
		path := pathValue.String()
		for _, attribute := range closedCheckoutAttributes {
			attributes.WriteString(path + "\x00" + attribute + "\x00unspecified\x00")
		}
	}
	proof, err := ValidateCheckoutAttributes(batches[0], []byte(attributes.String()))
	if err != nil || !proof.Valid() {
		t.Fatal(err)
	}
	var explicitlyUnset strings.Builder
	for _, pathValue := range paths {
		path := pathValue.String()
		for _, attribute := range closedCheckoutAttributes {
			value := "unspecified"
			if attribute == "text" || attribute == "ident" {
				value = "unset"
			}
			explicitlyUnset.WriteString(path + "\x00" + attribute + "\x00" + value + "\x00")
		}
	}
	if _, err := ValidateCheckoutAttributes(batches[0], []byte(explicitlyUnset.String())); err != nil {
		t.Fatalf("safe unset attributes: %v", err)
	}
	altered := strings.Replace(attributes.String(), "unspecified", "set", 1)
	if _, err := ValidateCheckoutAttributes(batches[0], []byte(altered)); !errors.Is(err, NewExecutorError(OperationCheckAttributes, FailurePolicyRejected)) {
		t.Fatalf("altered attribute error = %v", err)
	}
	approval, err := CompleteCheckoutAttributeCoverage(plan, []CheckoutAttributeBatchProof{proof})
	if err != nil || !approval.Valid() {
		t.Fatalf("checkout approval = %#v, %v", approval, err)
	}
	if command, err := NewCheckoutDetachedCommand(approval); err != nil || !command.Valid() {
		t.Fatalf("approved checkout = %#v, %v", command, err)
	}
	if command, err := NewCheckoutIndexCommand(approval); err != nil || !command.Valid() {
		t.Fatalf("approved checkout-index = %#v, %v", command, err)
	}
	if err := ValidateCleanStatus(nil); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCleanStatus([]byte("? secret\x00")); !errors.Is(err, NewExecutorError(OperationStatus, FailureRepositoryConflict)) {
		t.Fatalf("dirty status error = %v", err)
	}
	gotAttributes := CheckoutAttributeNames()
	gotAttributes[0] = "changed"
	if reflect.DeepEqual(gotAttributes, CheckoutAttributeNames()) {
		t.Fatal("attribute accessor is not defensively copied")
	}
}

func TestObjectOIDTypesRejectUppercaseAndZero(t *testing.T) {
	t.Parallel()

	object, err := NewGitObjectOID(testObjectA)
	blob, blobErr := NewBlobOID(testObjectA)
	if err != nil || blobErr != nil || !object.Valid() || !blob.Valid() || object.String() != blob.String() {
		t.Fatalf("oids = %s/%s, %v/%v", object.String(), blob.String(), err, blobErr)
	}
	for _, value := range []string{"", strings.Repeat("0", 40), strings.ToUpper(testObjectA), testObjectA[:39]} {
		if _, err := NewGitObjectOID(value); !errors.Is(err, ErrExecutorContract) {
			t.Errorf("NewGitObjectOID(%q) error = %v", value, err)
		}
		if _, err := NewBlobOID(value); !errors.Is(err, ErrExecutorContract) {
			t.Errorf("NewBlobOID(%q) error = %v", value, err)
		}
	}
}

func treeRecord(mode, kind, oid string, size uint64, path string) string {
	return fmt.Sprintf("%s %s %s %7d\t%s\x00", mode, kind, oid, size, path)
}

func treeRecordWithoutSize(mode, kind, oid, path string) string {
	return fmt.Sprintf("%s %s %s %7s\t%s\x00", mode, kind, oid, "-", path)
}

func checkoutAttributeOutput(
	batch CheckoutAttributeBatch,
	overridePath string,
	overrideAttribute string,
	overrideValue string,
) []byte {
	var output strings.Builder
	for _, path := range batch.Paths() {
		for _, attribute := range closedCheckoutAttributes {
			value := "unspecified"
			if path.String() == overridePath && attribute == overrideAttribute {
				value = overrideValue
			}
			output.WriteString(path.String() + "\x00" + attribute + "\x00" + value + "\x00")
		}
	}
	return []byte(output.String())
}

func mustTreeOID(t *testing.T, value string) domain.TreeOID {
	t.Helper()
	tree, err := domain.NewTreeOID(value)
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func mustRelativePath(t *testing.T, value string) pathsafe.RelativePath {
	t.Helper()
	path, err := pathsafe.NewRelativePath(value)
	if err != nil {
		t.Fatal(err)
	}
	return path
}
