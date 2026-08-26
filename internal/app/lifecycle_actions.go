package app

import (
	"fmt"
	"slices"
	"sort"

	"github.com/alx4j/ai4j/internal/cli"
)

type planActionSpec struct {
	owner         cli.ActionOwner
	kind          cli.ActionKind
	resource      string
	before, after cli.Condition
	recovery      cli.RecoveryRequirement
}

func makeActions(specs []planActionSpec) ([]cli.Action, error) {
	actions := make([]cli.Action, 0, len(specs))
	for index, spec := range specs {
		action, err := cli.NewAction(index+1, spec.owner, spec.kind, spec.resource, spec.before, spec.after, spec.recovery)
		if err != nil {
			return nil, fmt.Errorf("construct lifecycle action: %w", err)
		}
		actions = append(actions, action)
	}
	return actions, nil
}

func contentWithChange(content []cli.ContentItem, change cli.ContentChange) ([]cli.ContentItem, error) {
	changed := make([]cli.ContentItem, 0, len(content))
	for _, item := range content {
		var execution *cli.Execution
		if value, present := item.Execution(); present {
			execution = &value
		}
		value, err := cli.NewContentItem(item.ComponentType(), item.Identifier(), item.SourcePath(), item.Checksum(), change, execution)
		if err != nil {
			return nil, err
		}
		changed = append(changed, value)
	}
	return changed, nil
}

func diffActiveContent(installed, desired []cli.ContentItem) ([]cli.ContentItem, error) {
	installedByKey := make(map[string]cli.ContentItem, len(installed))
	desiredByKey := make(map[string]cli.ContentItem, len(desired))
	keys := make(map[string]struct{}, len(installed)+len(desired))
	for _, item := range installed {
		key := contentKey(item)
		installedByKey[key] = item
		keys[key] = struct{}{}
	}
	for _, item := range desired {
		key := contentKey(item)
		desiredByKey[key] = item
		keys[key] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	items := make([]cli.ContentItem, 0, len(ordered))
	for _, key := range ordered {
		before, hadBefore := installedByKey[key]
		after, hasAfter := desiredByKey[key]
		switch {
		case !hadBefore:
			item, err := contentItemWithChange(after, cli.ContentAdded)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		case !hasAfter:
			item, err := contentItemWithChange(before, cli.ContentRemoved)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		default:
			change := cli.ContentUnchanged
			if !sameContentItem(before, after) {
				change = cli.ContentChanged
			}
			item, err := contentItemWithChange(after, change)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
	}
	return items, nil
}

// recordedTransitionContent reports only content that can be described from
// the newly validated bundle. Earlier state retained asset identifiers, but not the
// type, path, checksum, or execution metadata needed to construct truthful
// removal disclosures for assets that are no longer selected.
func recordedTransitionContent(recordedAssets []string, desired []cli.ContentItem) ([]cli.ContentItem, error) {
	recorded := make(map[string]struct{}, len(recordedAssets))
	for _, asset := range recordedAssets {
		recorded[asset] = struct{}{}
	}
	content := make([]cli.ContentItem, 0, len(desired))
	for _, item := range desired {
		change := cli.ContentAdded
		if _, present := recorded[item.Identifier()]; present {
			change = cli.ContentChanged
		}
		converted, err := contentItemWithChange(item, change)
		if err != nil {
			return nil, err
		}
		content = append(content, converted)
	}
	return content, nil
}

func contentKey(item cli.ContentItem) string {
	return string(item.ComponentType()) + "\x00" + item.Identifier()
}

func sameContentItem(first, second cli.ContentItem) bool {
	if first.SourcePath() != second.SourcePath() || first.Checksum() != second.Checksum() {
		return false
	}
	firstExecution, firstPresent := first.Execution()
	secondExecution, secondPresent := second.Execution()
	if firstPresent != secondPresent {
		return false
	}
	if !firstPresent {
		return true
	}
	return firstExecution.Ownership() == secondExecution.Ownership() &&
		firstExecution.Dependency() == secondExecution.Dependency() &&
		firstExecution.Command() == secondExecution.Command() &&
		firstExecution.CWD() == secondExecution.CWD() &&
		slices.Equal(firstExecution.Args(), secondExecution.Args()) &&
		slices.Equal(firstExecution.SupportedPlaceholders(), secondExecution.SupportedPlaceholders()) &&
		slices.Equal(firstExecution.Environment(), secondExecution.Environment())
}

func contentItemWithChange(item cli.ContentItem, change cli.ContentChange) (cli.ContentItem, error) {
	var execution *cli.Execution
	if value, present := item.Execution(); present {
		execution = &value
	}
	return cli.NewContentItem(item.ComponentType(), item.Identifier(), item.SourcePath(), item.Checksum(), change, execution)
}
