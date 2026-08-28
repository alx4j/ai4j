package git_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/domain"
	gitsource "github.com/alx4j/ai4j/internal/source/git"
	gitremote "github.com/alx4j/ai4j/internal/source/gitremote"
)

const (
	otherCommitOIDText = "1123456789abcdef0123456789abcdef01234567"
	treeOIDText        = "2123456789abcdef0123456789abcdef01234567"
	renderedDigestText = "3123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	buildCommitText    = "4123456789abcdef0123456789abcdef01234567"
)

func TestResolutionRequestCopiesCredentialFreeEffectiveSourceFacts(t *testing.T) {
	t.Parallel()

	effective := effectiveSource(t, "git@github.com:alx4j/ai4j.git", true, "refs/heads/main", true)
	request, err := gitsource.NewResolutionRequest(effective)
	if err != nil {
		t.Fatal(err)
	}
	requested, provided := request.RequestedReference().Value()
	if !request.Valid() || request.SourceSelection() != domain.ExplicitSource() || request.Repository().String() != "github.com/alx4j/ai4j" || request.Transport() != domain.SSHGitTransport() || requested != "refs/heads/main" || !provided {
		t.Fatalf("unexpected request facts: valid=%t selection=%q repository=%q transport=%q requested=%q/%t", request.Valid(), request.SourceSelection(), request.Repository(), request.Transport(), requested, provided)
	}

	omitted, err := gitsource.NewResolutionRequest(effectiveSource(t, "", false, "", false))
	if err != nil {
		t.Fatal(err)
	}
	if omitted.SourceSelection() != domain.BuiltInDefaultSource() {
		t.Fatalf("selection = %q", omitted.SourceSelection())
	}
	if value, present := omitted.RequestedReference().Value(); value != "" || present {
		t.Fatalf("requested = %q/%t", value, present)
	}
	if _, err := gitsource.NewResolutionRequest(gitremote.EffectiveSource{}); !errors.Is(err, gitsource.ErrInvalidResolutionRequest) {
		t.Fatalf("zero source error = %v", err)
	}
}

func TestSourceProvenanceAcceptsOnlyCoherentReferenceAndTrackingFacts(t *testing.T) {
	t.Parallel()

	commit := commitIdentity(t, "github.com/alx4j/ai4j", commitOIDText)
	tree := treeOID(t, treeOIDText)
	tests := []struct {
		name          string
		reference     string
		provided      bool
		advertisement string
		kind          gitsource.ResolvedReferenceKind
		resolved      string
		tracking      gitsource.TrackingPolicy
	}{
		{name: "omitted default branch", advertisement: "ref: refs/heads/main\tHEAD\n" + commitOIDText + "\tHEAD\n" + commitOIDText + "\trefs/heads/main\n", kind: gitsource.ResolvedDefaultBranch, resolved: "main", tracking: gitsource.TrackFastForward},
		{name: "short branch", reference: "main", provided: true, advertisement: commitOIDText + "\trefs/heads/main\n", kind: gitsource.ResolvedBranch, resolved: "main", tracking: gitsource.TrackFastForward},
		{name: "unambiguous short tag", reference: "v1", provided: true, advertisement: commitOIDText + "\trefs/tags/v1\n", kind: gitsource.ResolvedTag, resolved: "v1", tracking: gitsource.TrackPinned},
		{name: "qualified branch", reference: "refs/heads/main", provided: true, advertisement: commitOIDText + "\trefs/heads/main\n", kind: gitsource.ResolvedBranch, resolved: "main", tracking: gitsource.TrackFastForward},
		{name: "qualified tag", reference: "refs/tags/v1", provided: true, advertisement: commitOIDText + "\trefs/tags/v1\n", kind: gitsource.ResolvedTag, resolved: "v1", tracking: gitsource.TrackPinned},
		{name: "commit", reference: commitOIDText, provided: true, kind: gitsource.ResolvedCommit, resolved: commitOIDText, tracking: gitsource.TrackPinned},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			proof := provenanceProof(t, test.reference, test.provided, test.advertisement, commitOIDText, treeOIDText)
			provenance, err := gitsource.NewSourceProvenance(proof)
			if err != nil {
				t.Fatal(err)
			}
			if !provenance.Valid() || provenance.Repository() != commit.Repository() || provenance.Commit() != commit ||
				provenance.RootTree() != tree || provenance.ResolvedReference().Kind() != test.kind ||
				provenance.ResolvedReference().Name() != test.resolved || provenance.TrackingPolicy() != test.tracking {
				t.Fatalf("invalid or changed provenance: %#v", provenance)
			}
		})
	}
}

func TestSourceProvenanceRejectsContradictionsAndSubstitutions(t *testing.T) {
	t.Parallel()

	if _, err := gitsource.NewSourceProvenance(gitsource.CommitTreeProof{}); !errors.Is(err, gitsource.ErrInvalidSourceProvenance) {
		t.Fatalf("zero proof error = %v", err)
	}
	commit := commitIdentity(t, "github.com/alx4j/ai4j", commitOIDText)
	unsupported, err := domain.NewObjectFormat("sha256")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := domain.NewCommitIdentity(commit.Repository(), unsupported, commit.OID()); err == nil {
		t.Fatal("unsupported commit object format was accepted")
	}
}

func TestRenderedProvenanceRequiresDistinctCallerSuppliedIdentities(t *testing.T) {
	t.Parallel()

	source := sourceProvenance(t)
	digest := renderedDigest(t, renderedDigestText)
	build := buildCommit(t, buildCommitText)
	rendered, err := gitsource.NewRenderedProvenance(source, digest, build)
	if err != nil {
		t.Fatal(err)
	}
	if !rendered.Valid() || rendered.Source() != source || rendered.RenderedDigest() != digest || rendered.BuildCommit() != build {
		t.Fatalf("rendered provenance changed typed facts: %#v", rendered)
	}
	for _, test := range []struct {
		name   string
		source gitsource.SourceProvenance
		digest domain.RenderedDigest
		build  domain.BuildCommit
	}{
		{name: "zero source", digest: digest, build: build},
		{name: "zero digest", source: source, build: build},
		{name: "zero build", source: source, digest: digest},
	} {
		if _, err := gitsource.NewRenderedProvenance(test.source, test.digest, test.build); !errors.Is(err, gitsource.ErrInvalidRenderedProvenance) {
			t.Errorf("%s error = %v", test.name, err)
		}
	}

	types := []reflect.Type{
		reflect.TypeOf(source.Commit().OID()),
		reflect.TypeOf(source.RootTree()),
		reflect.TypeOf(rendered.RenderedDigest()),
		reflect.TypeOf(rendered.BuildCommit()),
	}
	for i := range types {
		for j := i + 1; j < len(types); j++ {
			if types[i] == types[j] {
				t.Fatalf("identity types %v and %v are interchangeable", types[i], types[j])
			}
		}
	}
}

func TestUpdateDispositionIsClosedAndStable(t *testing.T) {
	t.Parallel()

	want := []gitsource.UpdateDisposition{
		gitsource.UpdateNoChange,
		gitsource.UpdateAvailable,
		gitsource.UpdatePinned,
		gitsource.UpdateRefRewritten,
		gitsource.UpdateAmbiguous,
		gitsource.UpdateDeleted,
		gitsource.UpdateSourceError,
	}
	for _, disposition := range want {
		if !disposition.Valid() || disposition.String() == "" {
			t.Errorf("disposition %q is invalid", disposition)
		}
	}
	for _, disposition := range []gitsource.UpdateDisposition{"", "unknown", "up_to_date", "not_checked"} {
		if disposition.Valid() {
			t.Errorf("unknown disposition %q is valid", disposition)
		}
	}
}

func effectiveSource(t *testing.T, repository string, repositoryProvided bool, reference string, referenceProvided bool) gitremote.EffectiveSource {
	t.Helper()
	input, err := gitremote.NewSelectionInput(repository, repositoryProvided, reference, referenceProvided)
	if err != nil {
		t.Fatal(err)
	}
	effective, err := gitremote.Resolve(input)
	if err != nil {
		t.Fatal(err)
	}
	return effective
}

func resolutionRequest(t *testing.T, reference string, provided bool) gitsource.ResolutionRequest {
	t.Helper()
	effective := effectiveSource(t, "", false, reference, provided)
	request, err := gitsource.NewResolutionRequest(effective)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func commitIdentity(t *testing.T, repositoryValue, oidValue string) domain.CommitIdentity {
	t.Helper()
	repository, err := domain.NewRepositoryIdentity(repositoryValue)
	if err != nil {
		t.Fatal(err)
	}
	oid, err := domain.NewCommitOID(oidValue)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := domain.NewCommitIdentity(repository, domain.SHA1ObjectFormat(), oid)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func treeOID(t *testing.T, value string) domain.TreeOID {
	t.Helper()
	oid, err := domain.NewTreeOID(value)
	if err != nil {
		t.Fatal(err)
	}
	return oid
}

func renderedDigest(t *testing.T, value string) domain.RenderedDigest {
	t.Helper()
	digest, err := domain.NewRenderedDigest(value)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func buildCommit(t *testing.T, value string) domain.BuildCommit {
	t.Helper()
	commit, err := domain.NewBuildCommit(value)
	if err != nil {
		t.Fatal(err)
	}
	return commit
}

func sourceProvenance(t *testing.T) gitsource.SourceProvenance {
	t.Helper()
	proof := provenanceProof(t, "main", true, commitOIDText+"\trefs/heads/main\n", commitOIDText, treeOIDText)
	provenance, err := gitsource.NewSourceProvenance(proof)
	if err != nil {
		t.Fatal(err)
	}
	return provenance
}

func provenanceProof(
	t *testing.T,
	reference string,
	provided bool,
	advertisementData string,
	commitValue string,
	treeValue string,
) gitsource.CommitTreeProof {
	t.Helper()
	request := resolutionRequest(t, reference, provided)
	advertisement, err := gitsource.ParseRemoteAdvertisement(request, []byte(advertisementData))
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := gitsource.ResolveReference(request, advertisement)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := gitsource.NewSelectedObjectProof(resolution, []byte("commit\n"))
	if err != nil {
		t.Fatal(err)
	}
	commit, err := gitsource.NewDirectProvenCommit(selected)
	if err != nil || commit.Commit().String() != commitValue {
		t.Fatalf("commit proof = %#v, %v", commit, err)
	}
	proof, err := gitsource.NewCommitTreeProof(commit, []byte(treeValue+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	return proof
}

func TestProvenanceErrorsDoNotEchoRejectedReference(t *testing.T) {
	t.Parallel()

	canary := "TOKEN_SUPER_SECRET"
	_, err := gitsource.NewRequestedReference("refs/heads/name^" + canary)
	if err == nil || strings.Contains(err.Error(), canary) || len(err.Error()) > 128 {
		t.Fatalf("unsafe error: %v", err)
	}
}
