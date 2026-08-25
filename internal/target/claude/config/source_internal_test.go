package config

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestStartupSourceReadsOnlyFixedNamesOnceAndReturnsSameSnapshot(t *testing.T) {
	t.Parallel()

	values := map[string]struct {
		value   string
		present bool
	}{
		homeEnvironmentName:            {value: "/Users/alex", present: true},
		claudeConfigDirEnvironmentName: {value: "/Users/alex/.claude-work", present: true},
	}
	var names []string
	source := newStartupSource(func(name string) (string, bool) {
		names = append(names, name)
		value := values[name]
		return value.value, value.present
	})
	first, err := source.Capture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	values[homeEnvironmentName] = struct {
		value   string
		present bool
	}{value: "/Users/replaced", present: true}
	second, err := source.Capture(context.Background())
	if err != nil || first != second {
		t.Fatalf("second capture = %v, %v", second, err)
	}
	want := []string{homeEnvironmentName, claudeConfigDirEnvironmentName}
	if len(names) != len(want) || names[0] != want[0] || names[1] != want[1] {
		t.Fatalf("lookup names = %v", names)
	}
}

func TestStartupSourceConcurrentCaptureReadsEachNameOnce(t *testing.T) {
	t.Parallel()

	var calls int
	source := newStartupSource(func(name string) (string, bool) {
		calls++
		if name == homeEnvironmentName {
			return "/Users/alex", true
		}
		return "", false
	})
	const workers = 32
	results := make(chan StartupInput, workers)
	errorsSeen := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := source.Capture(context.Background())
			results <- result
			errorsSeen <- err
		}()
	}
	group.Wait()
	close(results)
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatal(err)
		}
	}
	for result := range results {
		if !result.Valid() || result.HomeState() != PresentStartupValue() || result.OverrideState() != AbsentStartupValue() {
			t.Fatalf("capture = %v", result)
		}
	}
	if calls != 2 {
		t.Fatalf("lookup calls = %d, want 2", calls)
	}
}

func TestStartupSourceCancellationIsDeterministicAndDoesNotReread(t *testing.T) {
	t.Parallel()

	preCancelled, cancel := context.WithCancel(context.Background())
	cancel()
	var calls int
	source := newStartupSource(func(name string) (string, bool) {
		calls++
		if name == homeEnvironmentName {
			return "/Users/alex", true
		}
		return "", false
	})
	if _, err := source.Capture(preCancelled); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("pre-cancel capture = %v, calls %d", err, calls)
	}
	if _, err := source.Capture(context.Background()); err != nil || calls != 2 {
		t.Fatalf("retry = %v, calls %d", err, calls)
	}

	ctx, cancelDuring := context.WithCancel(context.Background())
	calls = 0
	during := newStartupSource(func(name string) (string, bool) {
		calls++
		if name == homeEnvironmentName {
			cancelDuring()
			return "/Users/alex", true
		}
		return "", false
	})
	if _, err := during.Capture(ctx); !errors.Is(err, context.Canceled) || calls != 2 {
		t.Fatalf("during-cancel capture = %v, calls %d", err, calls)
	}
	result, err := during.Capture(context.Background())
	if err != nil || !result.Valid() || calls != 2 {
		t.Fatalf("cached retry = %v, %v, calls %d", result, err, calls)
	}
}

func TestStartupSourceRejectsInvalidConstructionAndCachesValidationFailure(t *testing.T) {
	t.Parallel()

	if _, err := (*environmentStartupSource)(nil).Capture(context.Background()); err == nil {
		t.Fatal("nil source accepted")
	}
	if _, err := newStartupSource(nil).Capture(context.Background()); err == nil {
		t.Fatal("nil lookup accepted")
	}
	source := newStartupSource(func(string) (string, bool) { return "bad\nvalue", true })
	if _, err := source.Capture(nil); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := source.Capture(context.Background()); err == nil {
		t.Fatal("invalid captured value accepted")
	}
	if _, err := source.Capture(context.Background()); err == nil {
		t.Fatal("cached invalid value accepted")
	}
}
