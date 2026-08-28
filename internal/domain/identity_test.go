package domain_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/domain"
)

const (
	sha1   = "0123456789abcdef0123456789abcdef01234567"
	sha256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

func TestDistinctIdentityTypesAndCanonicalRoundTrip(t *testing.T) {
	t.Parallel()

	repository, err := domain.NewRepositoryIdentity("github.com/alx4j/ai4j")
	if err != nil {
		t.Fatal(err)
	}
	commit, err := domain.NewCommitOID(sha1)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := domain.NewTreeOID(sha1)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := domain.NewRenderedDigest(sha256)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := domain.NewExecutableDigest(sha256)
	if err != nil {
		t.Fatal(err)
	}
	build, err := domain.NewBuildCommit(sha1)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := domain.NewCommitIdentity(repository, domain.SHA1ObjectFormat(), commit)
	if err != nil {
		t.Fatal(err)
	}

	if identity.Repository() != repository || identity.OID() != commit || identity.ObjectFormat() != domain.SHA1ObjectFormat() {
		t.Fatalf("commit identity did not preserve its typed components: %#v", identity)
	}
	for name, got := range map[string]string{
		"commit": commit.String(), "tree": tree.String(), "rendered": rendered.String(), "executable": executable.String(), "build": build.String(),
	} {
		want := sha1
		if name == "rendered" || name == "executable" {
			want = sha256
		}
		if got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	types := []reflect.Type{reflect.TypeOf(commit), reflect.TypeOf(tree), reflect.TypeOf(rendered), reflect.TypeOf(executable), reflect.TypeOf(build)}
	for i := range types {
		for j := i + 1; j < len(types); j++ {
			if types[i] == types[j] {
				t.Fatalf("identity types %v and %v are interchangeable", types[i], types[j])
			}
		}
	}
}

func TestIdentityConstructorsRejectNonCanonicalValues(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"https://github.com/alx4j/ai4j",
		"github.com/Alx4j/ai4j",
		"github.com/alx4j/ai4j.git",
	} {
		if _, err := domain.NewRepositoryIdentity(value); err == nil {
			t.Errorf("NewRepositoryIdentity(%q) succeeded", value)
		}
	}
	for _, value := range []string{"", strings.ToUpper(sha1), sha1[:39], strings.Repeat("0", 40)} {
		if _, err := domain.NewCommitOID(value); err == nil {
			t.Errorf("NewCommitOID(%q) succeeded", value)
		}
	}
	for _, value := range []string{"", strings.ToUpper(sha256), sha256[:63], strings.Repeat("0", 64)} {
		if _, err := domain.NewRenderedDigest(value); err == nil {
			t.Errorf("NewRenderedDigest(%q) succeeded", value)
		}
		if _, err := domain.NewExecutableDigest(value); err == nil {
			t.Errorf("NewExecutableDigest(%q) succeeded", value)
		}
	}
}

func TestRepositoryIdentitySupportsEnterpriseHostAndNestedNamespace(t *testing.T) {
	t.Parallel()

	value := "gitlab.barclays.example/division/team/Barclays"
	repository, err := domain.NewRepositoryIdentity(value)
	if err != nil || !repository.Valid() || repository.String() != value {
		t.Fatalf("enterprise repository = %q, %v", repository.String(), err)
	}
	shortHost, err := domain.NewRepositoryIdentity("gitlab/division/team/toolkit")
	if err != nil || !shortHost.Valid() {
		t.Fatalf("single-label enterprise host = %q, %v", shortHost.String(), err)
	}
	for _, invalid := range []string{
		"GitLab.barclays.example/division/team/repository",
		"127.0.0.1/division/team/repository",
		"gitlab.barclays.example/division/../repository",
		"gitlab.barclays.example/division//repository",
	} {
		if _, err := domain.NewRepositoryIdentity(invalid); err == nil {
			t.Errorf("NewRepositoryIdentity(%q) succeeded", invalid)
		}
	}
}

func TestTypedIdentifiersRemainDistinct(t *testing.T) {
	t.Parallel()

	operation, err := domain.NewOperationID("op-123")
	if err != nil {
		t.Fatal(err)
	}
	installation, err := domain.NewInstallationID("install-123")
	if err != nil {
		t.Fatal(err)
	}
	if operation.String() != "op-123" || installation.String() != "install-123" {
		t.Fatalf("unexpected identifiers: %q %q", operation.String(), installation.String())
	}
	if reflect.TypeOf(operation) == reflect.TypeOf(installation) {
		t.Fatal("operation and installation IDs must be distinct")
	}
	token, err := domain.NewArtifactToken("0123456789abcdef0123456789abcdef")
	if err != nil || token.String() != "0123456789abcdef0123456789abcdef" || !token.Valid() {
		t.Fatalf("artifact token = %q, %v", token.String(), err)
	}
	for _, value := range []string{"", "0123456789ABCDEF0123456789ABCDEF", strings.Repeat("0", 32)} {
		if _, err := domain.NewArtifactToken(value); err == nil {
			t.Errorf("NewArtifactToken(%q) succeeded", value)
		}
	}
	for _, value := range []string{"", "UPPER", "../escape", strings.Repeat("a", 65)} {
		if _, err := domain.NewOperationID(value); err == nil {
			t.Errorf("NewOperationID(%q) succeeded", value)
		}
	}
}
