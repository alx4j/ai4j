package repocheck

import (
	"strings"
	"testing"
)

func TestValidateCommit(t *testing.T) {
	t.Parallel()

	valid := Commit{
		Hash:           "0123456789abcdef",
		AuthorName:     RequiredName,
		AuthorEmail:    RequiredEmail,
		CommitterName:  RequiredName,
		CommitterEmail: RequiredEmail,
		Message:        "feat: establish repository policy",
	}

	tests := []struct {
		name      string
		change    func(*Commit)
		wantError string
	}{
		{name: "valid", change: func(*Commit) {}},
		{name: "author name", change: func(c *Commit) { c.AuthorName = "Someone Else" }, wantError: "author is"},
		{name: "author email", change: func(c *Commit) { c.AuthorEmail = "other@example.com" }, wantError: "author is"},
		{name: "committer name", change: func(c *Commit) { c.CommitterName = "Someone Else" }, wantError: "committer is"},
		{name: "committer email", change: func(c *Commit) { c.CommitterEmail = "other@example.com" }, wantError: "committer is"},
		{name: "coauthor trailer", change: func(c *Commit) { c.Message += "\n\nCo-Authored-By: Other <other@example.com>" }, wantError: "co-authored-by"},
		{name: "generated trailer", change: func(c *Commit) { c.Message += "\nGenerated-By: tool" }, wantError: "generated-by"},
		{name: "bot attribution", change: func(c *Commit) { c.Message += "\nSigned by Bot" }, wantError: "bot"},
		{name: "assistant attribution", change: func(c *Commit) { c.Message += "\nAI Assistant" }, wantError: "assistant"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commit := valid
			test.change(&commit)
			err := ValidateCommit(commit, false)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("ValidateCommit() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), test.wantError) {
				t.Fatalf("ValidateCommit() error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}

func TestValidateCommitAllowsOnlyExactGitHubCommitterWhenRequested(t *testing.T) {
	t.Parallel()

	valid := Commit{
		Hash:           "0123456789abcdef",
		AuthorName:     RequiredName,
		AuthorEmail:    RequiredEmail,
		CommitterName:  githubCommitterName,
		CommitterEmail: githubCommitterEmail,
		Message:        "Fix main checks (#11)",
	}
	tests := []struct {
		name                 string
		allowGitHubCommitter bool
		change               func(*Commit)
		wantError            string
	}{
		{name: "explicit exception", allowGitHubCommitter: true, change: func(*Commit) {}},
		{name: "strict default", change: func(*Commit) {}, wantError: "committer is"},
		{name: "different GitHub name", allowGitHubCommitter: true, change: func(c *Commit) { c.CommitterName = "github-actions[bot]" }, wantError: "committer is"},
		{name: "different GitHub email", allowGitHubCommitter: true, change: func(c *Commit) { c.CommitterEmail = "actions@github.com" }, wantError: "committer is"},
		{name: "wrong author", allowGitHubCommitter: true, change: func(c *Commit) { c.AuthorName = "Someone Else" }, wantError: "author is"},
		{name: "forbidden attribution", allowGitHubCommitter: true, change: func(c *Commit) { c.Message += "\n\nCo-Authored-By: Other <other@example.com>" }, wantError: "co-authored-by"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commit := valid
			test.change(&commit)
			err := ValidateCommit(commit, test.allowGitHubCommitter)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("ValidateCommit() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), test.wantError) {
				t.Fatalf("ValidateCommit() error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}

func TestForbiddenAttributionDoesNotMatchSubstrings(t *testing.T) {
	t.Parallel()

	for _, text := range []string{"robotics release", "both identities", "authorship policy"} {
		if got := ForbiddenAttribution(text); got != "" {
			t.Fatalf("ForbiddenAttribution(%q) = %q, want empty", text, got)
		}
	}
}

func TestValidateRevision(t *testing.T) {
	t.Parallel()

	for _, revision := range []string{"HEAD", "main~2..feature", "0123456789abcdef^"} {
		if err := validateRevision(revision); err != nil {
			t.Errorf("validateRevision(%q) error = %v", revision, err)
		}
	}
	for _, revision := range []string{"", "--all", "HEAD;echo", "HEAD main"} {
		if err := validateRevision(revision); err == nil {
			t.Errorf("validateRevision(%q) succeeded, want error", revision)
		}
	}
}
