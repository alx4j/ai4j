package app

import (
	"context"
	"reflect"
	"slices"

	"github.com/alx4j/ai4j/internal/installstate"
)

// reconcileInterrupted completes only transaction states that are fully
// explained by the operation's marker and structural history. Mixed, drifted,
// unsupported, or changing observations remain fail-closed.
func (s *lifecycleService) reconcileInterrupted(ctx context.Context) (bool, error) {
	marker, present, err := s.state.LoadMarker()
	if err != nil || !present {
		return !present && err == nil, err
	}
	if marker.Operation == "history_purge" || !slices.Contains(marker.Resources, "history:"+marker.InstallationID) {
		return false, nil
	}
	entry, historyPresent, err := s.state.LoadOperationHistory(marker.InstallationID, marker.OperationID)
	if err != nil {
		return false, err
	}
	if !historyPresent {
		current, statePresent, loadErr := s.state.LoadByID(marker.InstallationID)
		if loadErr != nil {
			return false, loadErr
		}
		if !statePresent || current.Lifecycle != "active" || current.LastOperation.ID == marker.OperationID ||
			!s.recoveryTargetMatches(ctx, &current, nil) || !s.recoverySnapshotMatches(ctx, marker.InstallationID, &current, nil, &current) {
			return false, nil
		}
		if err := s.state.DeleteMarker(); err != nil {
			return false, err
		}
		return true, nil
	}
	if entry.Operation != marker.Operation || entry.OperationID != marker.OperationID || entry.InstallationID != marker.InstallationID ||
		entry.After == nil || recordSourceRevision(*entry.After) != marker.Commit {
		return false, nil
	}
	current, statePresent, err := s.state.LoadByID(marker.InstallationID)
	if err != nil {
		return false, err
	}
	afterState := statePresent && reflect.DeepEqual(current, *entry.After)
	beforeState := entry.Before == nil && !statePresent || entry.Before != nil && statePresent && reflect.DeepEqual(current, *entry.Before)
	afterTarget := s.recoveryTargetMatches(ctx, entry.After, entry.Before)
	beforeTarget := s.recoveryTargetMatches(ctx, entry.Before, entry.After)

	switch {
	case afterState && afterTarget:
		if !s.recoverySnapshotMatches(ctx, marker.InstallationID, entry.After, entry.Before, entry.After) {
			return false, nil
		}
		if !entry.Committed {
			if err := s.state.CommitHistory(entry); err != nil {
				return false, err
			}
		}
		if err := s.state.DeleteMarker(); err != nil {
			return false, err
		}
		return true, nil
	case !entry.Committed && beforeState && afterTarget:
		// Target mutation completed and verified but installation state did not.
		if !s.recoverySnapshotMatches(ctx, marker.InstallationID, entry.Before, entry.Before, entry.After) {
			return false, nil
		}
		if entry.Before == nil {
			err = s.state.SaveNew(*entry.After)
		} else {
			err = s.state.Save(*entry.After)
		}
		if err != nil {
			return false, err
		}
		if err := s.state.CommitHistory(entry); err != nil {
			return false, err
		}
		if err := s.state.DeleteMarker(); err != nil {
			return false, err
		}
		return true, nil
	case !entry.Committed && beforeState && beforeTarget:
		// No durable mutation remains, so the staged journal can be discarded.
		if !s.recoverySnapshotMatches(ctx, marker.InstallationID, entry.Before, entry.After, entry.Before) {
			return false, nil
		}
		if err := s.state.DeleteHistory(marker.InstallationID, []string{marker.OperationID}); err != nil {
			return false, err
		}
		if err := s.state.DeleteMarker(); err != nil {
			return false, err
		}
		return true, nil
	default:
		return false, nil
	}
}

func (s *lifecycleService) recoverySnapshotMatches(ctx context.Context, installationID string, expectedState, counterpart, expectedTarget *installstate.Record) bool {
	current, present, err := s.state.LoadByID(installationID)
	if err != nil || expectedState == nil && present || expectedState != nil && (!present || !reflect.DeepEqual(current, *expectedState)) {
		return false
	}
	return s.recoveryTargetMatches(ctx, expectedTarget, counterpart)
}

func (s *lifecycleService) recoveryTargetMatches(ctx context.Context, expected, counterpart *installstate.Record) bool {
	if expected == nil {
		if counterpart == nil {
			return false
		}
		native, problem := s.inspectNative(ctx, *counterpart)
		if problem != nil || native.MarketplaceRegistered || native.PluginInstalled {
			return false
		}
		return recoveryOwnedAbsent(s, *counterpart)
	}
	if expected.Health == "drifted" || s.verifyDesired(ctx, *expected) != nil {
		return false
	}
	if expected.Lifecycle == "archived" && counterpart != nil {
		return recoveryOwnedAbsent(s, *counterpart)
	}
	return true
}

func recoveryOwnedAbsent(s *lifecycleService, record installstate.Record) bool {
	if record.Scope == "project-shared" {
		return projectMarketplaceAbsent(record) && (record.Rules == (installstate.OwnedFile{}) || ownedFileAbsent(s.rulesPath(record)))
	}
	return (record.Catalog == (installstate.OwnedFile{}) || ownedFileAbsent(s.catalogPath(record))) &&
		(record.Rules == (installstate.OwnedFile{}) || ownedFileAbsent(s.rulesPath(record)))
}
