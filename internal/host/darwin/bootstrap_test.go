package darwin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alx4j/ai4j/internal/host/darwin/resource"
	"github.com/alx4j/ai4j/internal/lifecycle"
	"github.com/alx4j/ai4j/internal/pathsafe"
)

const bootstrapCanary = "/Users/bootstrap-secret-canary"

type fakeUserDirectoryAuthority struct {
	mu          sync.Mutex
	home        lifecycle.UserHomeProof
	leaf        lifecycle.DirectoryLeafProof
	inspectCall int
	qualifyCall int
	closeCall   int
	entered     chan struct{}
	release     chan struct{}
	closeErr    error
}

type observedBootstrapCall struct {
	ctx      context.Context
	observed time.Time
}

type deadlineUserDirectoryAuthority struct {
	mu           sync.Mutex
	inspectCalls []observedBootstrapCall
	qualifyCalls []observedBootstrapCall
	waitForDone  bool
	closeCall    int
}

func (f *deadlineUserDirectoryAuthority) InspectUserHome(ctx context.Context) (lifecycle.UserHomeProof, error) {
	f.mu.Lock()
	f.inspectCalls = append(f.inspectCalls, observedBootstrapCall{ctx: ctx, observed: time.Now()})
	waitForDone := f.waitForDone
	f.mu.Unlock()
	if waitForDone {
		<-ctx.Done()
		return lifecycle.UserHomeProof{}, ctx.Err()
	}
	return lifecycle.UserHomeProof{}, nil
}

func (f *deadlineUserDirectoryAuthority) QualifyUserDirectory(
	ctx context.Context,
	_ lifecycle.UserHomeProof,
	_ pathsafe.RelativePath,
) (lifecycle.DirectoryLeafProof, error) {
	f.mu.Lock()
	f.qualifyCalls = append(f.qualifyCalls, observedBootstrapCall{ctx: ctx, observed: time.Now()})
	waitForDone := f.waitForDone
	f.mu.Unlock()
	if waitForDone {
		<-ctx.Done()
		return lifecycle.DirectoryLeafProof{}, ctx.Err()
	}
	return lifecycle.DirectoryLeafProof{}, nil
}

func (f *deadlineUserDirectoryAuthority) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCall++
	return nil
}

func (f *fakeUserDirectoryAuthority) InspectUserHome(context.Context) (lifecycle.UserHomeProof, error) {
	f.mu.Lock()
	f.inspectCall++
	entered, release := f.entered, f.release
	f.mu.Unlock()
	if entered != nil {
		close(entered)
		<-release
	}
	return f.home, nil
}

func (f *fakeUserDirectoryAuthority) QualifyUserDirectory(
	context.Context,
	lifecycle.UserHomeProof,
	pathsafe.RelativePath,
) (lifecycle.DirectoryLeafProof, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.qualifyCall++
	return f.leaf, nil
}

func (f *fakeUserDirectoryAuthority) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCall++
	return f.closeErr
}

func TestBootstrapDelegatesOnlyWhileOpenAndClosesOnce(t *testing.T) {
	t.Parallel()

	authority := &fakeUserDirectoryAuthority{}
	bootstrap, err := newBootstrap(authority)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.InspectUserHome(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := bootstrap.QualifyUserDirectory(t.Context(), lifecycle.UserHomeProof{}, pathsafe.RelativePath{}); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.Close(); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.Close(); err != nil {
		t.Fatal(err)
	}
	if authority.inspectCall != 1 || authority.qualifyCall != 1 || authority.closeCall != 1 {
		t.Fatalf("calls = inspect:%d qualify:%d close:%d", authority.inspectCall, authority.qualifyCall, authority.closeCall)
	}
	if _, err := bootstrap.InspectUserHome(t.Context()); !errors.Is(err, errBootstrapClosed) {
		t.Fatalf("closed inspect error = %v", err)
	}
}

func TestBootstrapCloseWaitsForActiveObservation(t *testing.T) {
	t.Parallel()

	authority := &fakeUserDirectoryAuthority{entered: make(chan struct{}), release: make(chan struct{})}
	bootstrap, err := newBootstrap(authority)
	if err != nil {
		t.Fatal(err)
	}
	inspectDone := make(chan error, 1)
	go func() {
		_, inspectErr := bootstrap.InspectUserHome(context.Background())
		inspectDone <- inspectErr
	}()
	<-authority.entered
	closeDone := make(chan error, 1)
	go func() { closeDone <- bootstrap.Close() }()
	closingDeadline := time.Now().Add(time.Second)
	for {
		bootstrap.state.mu.Lock()
		closing := bootstrap.state.closing
		bootstrap.state.mu.Unlock()
		if closing {
			break
		}
		if time.Now().After(closingDeadline) {
			t.Fatal("Close did not enter closing state")
		}
		time.Sleep(time.Millisecond)
	}
	caller, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	if _, err := bootstrap.InspectUserHome(caller); !errors.Is(err, errBootstrapClosed) {
		cancel()
		t.Fatalf("call queued after Close error = %v", err)
	}
	cancel()
	close(authority.release)
	if err := <-inspectDone; err != nil {
		t.Fatal(err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapRejectsInvalidContextAndTypedNilAuthority(t *testing.T) {
	t.Parallel()

	var typedNil *fakeUserDirectoryAuthority
	for _, authority := range []userDirectoryProofAuthority{nil, typedNil} {
		if bootstrap, err := newBootstrap(authority); bootstrap != nil || !errors.Is(err, errInvalidBootstrap) {
			t.Fatalf("newBootstrap(%T) = %v, %v", authority, bootstrap, err)
		}
	}
	bootstrap, err := newBootstrap(&fakeUserDirectoryAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	defer bootstrap.Close()
	if _, err := bootstrap.InspectUserHome(nil); !errors.Is(err, errInvalidBootstrap) {
		t.Fatalf("nil context error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := bootstrap.InspectUserHome(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled error = %v", err)
	}
	if err := (*Bootstrap)(nil).Close(); !errors.Is(err, errInvalidBootstrap) {
		t.Fatalf("nil close error = %v", err)
	}
}

func TestBootstrapCreatesFreshExactFilesystemBudgetForEveryObservation(t *testing.T) {
	t.Parallel()

	authority := &deadlineUserDirectoryAuthority{}
	bootstrap, err := newBootstrap(authority)
	if err != nil {
		t.Fatal(err)
	}
	defer bootstrap.Close()
	if _, err := bootstrap.InspectUserHome(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := bootstrap.QualifyUserDirectory(
		context.Background(),
		lifecycle.UserHomeProof{},
		pathsafe.RelativePath{},
	); err != nil {
		t.Fatal(err)
	}

	authority.mu.Lock()
	if len(authority.inspectCalls) != 1 || len(authority.qualifyCalls) != 1 {
		authority.mu.Unlock()
		t.Fatalf("observed calls = inspect:%d qualify:%d", len(authority.inspectCalls), len(authority.qualifyCalls))
	}
	inspectCall := authority.inspectCalls[0]
	qualifyCall := authority.qualifyCalls[0]
	authority.mu.Unlock()
	if inspectCall.ctx == qualifyCall.ctx {
		t.Fatal("InspectUserHome and QualifyUserDirectory shared one budget context")
	}
	maximum, ok := resource.MVPPolicy().Timeout(resource.FilesystemBudget)
	if !ok {
		t.Fatal("MVP filesystem budget is unavailable")
	}
	for name, call := range map[string]observedBootstrapCall{
		"inspect": inspectCall,
		"qualify": qualifyCall,
	} {
		deadline, ok := call.ctx.Deadline()
		if !ok {
			t.Fatalf("%s call has no deadline", name)
		}
		remaining := deadline.Sub(call.observed)
		if remaining > maximum || remaining < maximum-time.Second {
			t.Fatalf("%s remaining budget = %v, want fresh %v maximum", name, remaining, maximum)
		}
	}
	inspectDeadline, _ := inspectCall.ctx.Deadline()
	qualifyDeadline, _ := qualifyCall.ctx.Deadline()
	if !qualifyDeadline.After(inspectDeadline) {
		t.Fatalf("qualify deadline %v is not fresh after inspect deadline %v", qualifyDeadline, inspectDeadline)
	}
}

func TestBootstrapFilesystemBudgetPreservesEarlierCallerDeadline(t *testing.T) {
	t.Parallel()

	authority := &deadlineUserDirectoryAuthority{waitForDone: true}
	bootstrap, err := newBootstrap(authority)
	if err != nil {
		t.Fatal(err)
	}
	defer bootstrap.Close()
	callerDeadline := time.Now().Add(25 * time.Millisecond)
	caller, cancel := context.WithDeadline(context.Background(), callerDeadline)
	defer cancel()
	started := time.Now()
	if _, err := bootstrap.InspectUserHome(caller); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("InspectUserHome error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("caller deadline took %v", elapsed)
	}
	authority.mu.Lock()
	observed := authority.inspectCalls[0]
	authority.mu.Unlock()
	deadline, ok := observed.ctx.Deadline()
	if !ok || !deadline.Equal(callerDeadline) {
		t.Fatalf("observed deadline = %v, %t, want caller deadline %v", deadline, ok, callerDeadline)
	}
}

func TestBootstrapConstructionIsBoundedAndClosesLateAuthority(t *testing.T) {
	t.Parallel()

	authority := &deadlineUserDirectoryAuthority{}
	callerDeadline := time.Now().Add(25 * time.Millisecond)
	caller, cancel := context.WithDeadline(context.Background(), callerDeadline)
	defer cancel()
	var observedDeadline time.Time
	directories, err := constructUserDirectoryProofAuthority(caller, func(ctx context.Context) (userDirectoryProofAuthority, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("constructor context has no deadline")
		}
		observedDeadline = deadline
		<-ctx.Done()
		return authority, nil
	})
	if directories != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("construction = %v, %v", directories, err)
	}
	if !observedDeadline.Equal(callerDeadline) {
		t.Fatalf("constructor deadline = %v, want caller deadline %v", observedDeadline, callerDeadline)
	}
	authority.mu.Lock()
	closeCalls := authority.closeCall
	authority.mu.Unlock()
	if closeCalls != 1 {
		t.Fatalf("late authority close calls = %d, want 1", closeCalls)
	}
}

func TestBootstrapConstructionRejectsInvalidInputsAndUsesExactProfile(t *testing.T) {
	t.Parallel()

	if _, err := constructUserDirectoryProofAuthority(context.Background(), nil); !errors.Is(err, errInvalidBootstrap) {
		t.Fatalf("nil constructor error = %v", err)
	}
	if _, _, err := boundedFilesystemContext(context.Background(), resource.Policy{}); !errors.Is(err, errInvalidBootstrap) {
		t.Fatalf("divergent policy error = %v", err)
	}
	authority := &deadlineUserDirectoryAuthority{}
	var observed observedBootstrapCall
	directories, err := constructUserDirectoryProofAuthority(
		context.Background(),
		func(ctx context.Context) (userDirectoryProofAuthority, error) {
			observed = observedBootstrapCall{ctx: ctx, observed: time.Now()}
			return authority, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer directories.Close()
	deadline, ok := observed.ctx.Deadline()
	maximum, maximumOK := resource.MVPPolicy().Timeout(resource.FilesystemBudget)
	if !ok || !maximumOK || deadline.Sub(observed.observed) > maximum ||
		deadline.Sub(observed.observed) < maximum-time.Second {
		t.Fatalf("constructor budget deadline = %v, observed = %v, maximum = %v", deadline, observed.observed, maximum)
	}
}

func TestBootstrapFormattingAndCloseFailureAreRedacted(t *testing.T) {
	t.Parallel()

	authority := &fakeUserDirectoryAuthority{closeErr: errors.New(bootstrapCanary)}
	bootstrap, err := newBootstrap(authority)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	forms := []string{
		fmt.Sprintf("%v", bootstrap), fmt.Sprintf("%+v", bootstrap), fmt.Sprintf("%#v", bootstrap),
		fmt.Sprintf("%q", bootstrap), fmt.Sprintf("%s", bootstrap), string(encoded),
	}
	for _, form := range forms {
		if strings.Contains(form, bootstrapCanary) {
			t.Fatalf("bootstrap disclosed canary in %q", form)
		}
	}
	if err := bootstrap.Close(); !errors.Is(err, errBootstrapClose) || strings.Contains(err.Error(), bootstrapCanary) {
		t.Fatalf("close error = %v", err)
	}
}
