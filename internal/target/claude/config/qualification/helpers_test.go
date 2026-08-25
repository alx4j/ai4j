package qualification_test

import (
	"bytes"
	"context"
	"io/fs"
	"testing"

	"github.com/alx4j/ai4j/internal/environment"
	"github.com/alx4j/ai4j/internal/lifecycle"
	"github.com/alx4j/ai4j/internal/pathsafe"
	claudeconfig "github.com/alx4j/ai4j/internal/target/claude/config"
)

const qualificationHomeCanary = "/Users/qualification-secret-canary"

type proofSource struct {
	home       lifecycle.UserHomeProof
	homeErr    error
	proofs     map[string]lifecycle.DirectoryLeafProof
	proofErrs  map[string]error
	calls      []string
	passedHome []lifecycle.UserHomeProof
}

func (s *proofSource) InspectUserHome(context.Context) (lifecycle.UserHomeProof, error) {
	s.calls = append(s.calls, "home")
	return s.home, s.homeErr
}

func (s *proofSource) QualifyUserDirectory(
	_ context.Context,
	home lifecycle.UserHomeProof,
	relative pathsafe.RelativePath,
) (lifecycle.DirectoryLeafProof, error) {
	s.calls = append(s.calls, relative.String())
	s.passedHome = append(s.passedHome, home)
	if err := s.proofErrs[relative.String()]; err != nil {
		return lifecycle.DirectoryLeafProof{}, err
	}
	return s.proofs[relative.String()], nil
}

func newProofFixture(t *testing.T, configPresence, rulesPresence lifecycle.DirectoryLeafPresence) (*proofSource, lifecycle.ProofIssuerSeal) {
	t.Helper()
	seal := mustSeal(t, 0x71)
	home := mustHome(t, seal, qualificationHomeCanary, 3)
	configurationLeaf := lifecycle.DirectoryObjectProof{}
	if configPresence == lifecycle.PresentDirectoryLeaf() {
		configurationLeaf = mustObject(t, 4, lifecycle.CurrentUserOwner, 0o700)
	}
	configuration := mustLeaf(
		t, seal, home, ".claude", qualificationHomeCanary+"/.claude", configPresence, home.Home(), configurationLeaf,
	)
	proofs := map[string]lifecycle.DirectoryLeafProof{".claude": configuration}
	if configPresence == lifecycle.PresentDirectoryLeaf() {
		rulesLeaf := lifecycle.DirectoryObjectProof{}
		if rulesPresence == lifecycle.PresentDirectoryLeaf() {
			rulesLeaf = mustObject(t, 5, lifecycle.CurrentUserOwner, 0o700)
		}
		proofs[".claude/rules"] = mustLeaf(
			t, seal, home, ".claude/rules", qualificationHomeCanary+"/.claude/rules",
			rulesPresence, configurationLeaf, rulesLeaf,
		)
	}
	return &proofSource{home: home, proofs: proofs, proofErrs: make(map[string]error)}, seal
}

func mustSeal(t *testing.T, value byte) lifecycle.ProofIssuerSeal {
	t.Helper()
	seal, err := lifecycle.NewProofIssuerSeal(bytes.Repeat([]byte{value}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return seal
}

func mustLocator(t *testing.T, value string) lifecycle.HostDirectoryLocator {
	t.Helper()
	locator, err := lifecycle.NewHostDirectoryLocator(value)
	if err != nil {
		t.Fatal(err)
	}
	return locator
}

func mustObject(t *testing.T, object uint64, owner lifecycle.OwnerClass, mode fs.FileMode) lifecycle.DirectoryObjectProof {
	t.Helper()
	proof, err := lifecycle.NewDirectoryObjectProof(lifecycle.ObjectIdentity{Filesystem: 11, Object: object}, owner, mode)
	if err != nil {
		t.Fatal(err)
	}
	return proof
}

func mustHome(t *testing.T, seal lifecycle.ProofIssuerSeal, locator string, object uint64) lifecycle.UserHomeProof {
	t.Helper()
	proof, err := lifecycle.NewUserHomeProof(
		seal,
		mustLocator(t, locator),
		mustObject(t, object-1, lifecycle.SystemOwner, 0o755),
		mustObject(t, object, lifecycle.CurrentUserOwner, 0o700),
	)
	if err != nil {
		t.Fatal(err)
	}
	return proof
}

func mustLeaf(
	t *testing.T,
	seal lifecycle.ProofIssuerSeal,
	home lifecycle.UserHomeProof,
	relative string,
	locator string,
	presence lifecycle.DirectoryLeafPresence,
	parent lifecycle.DirectoryObjectProof,
	leaf lifecycle.DirectoryObjectProof,
) lifecycle.DirectoryLeafProof {
	t.Helper()
	path, err := pathsafe.NewRelativePath(relative)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := lifecycle.NewDirectoryLeafProof(
		seal, home, path, mustLocator(t, locator), presence, home.Home(), parent, leaf,
	)
	if err != nil {
		t.Fatal(err)
	}
	return proof
}

func mustVersion(t *testing.T) environment.ToolVersion {
	t.Helper()
	semantic, err := environment.NewSemanticVersion("2.1.211")
	if err != nil {
		t.Fatal(err)
	}
	version, err := environment.NewSemanticToolVersion(environment.ClaudeTool(), semantic)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func mustPolicy(t *testing.T, decision claudeconfig.OverrideDecision) claudeconfig.OverridePolicy {
	t.Helper()
	policy, err := claudeconfig.NewOverridePolicy(mustVersion(t), decision)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func mustStartup(t *testing.T, override string, overridePresent bool) claudeconfig.StartupInput {
	t.Helper()
	input, err := claudeconfig.NewStartupInput(qualificationHomeCanary, true, override, overridePresent)
	if err != nil {
		t.Fatal(err)
	}
	return input
}
