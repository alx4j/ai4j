package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"github.com/alx4j/ai4j/internal/cli"
	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/installstate"
	"github.com/alx4j/ai4j/internal/target/claude/catalog"
)

type jsonObjectMember struct {
	key                  string
	keyStart, valueStart int
	valueEnd, commaAfter int
}

func projectMarketplaceEntry(record installstate.Record) ([]byte, error) {
	entry := struct {
		Source struct {
			Source  string `json:"source"`
			Name    string `json:"name"`
			Plugins []struct {
				Name   string `json:"name"`
				Source struct {
					Source string `json:"source"`
					URL    string `json:"url"`
					Path   string `json:"path"`
					SHA    string `json:"sha"`
				} `json:"source"`
			} `json:"plugins"`
		} `json:"source"`
	}{}
	entry.Source.Source = "settings"
	entry.Source.Name = record.DeclarationID
	entry.Source.Plugins = make([]struct {
		Name   string `json:"name"`
		Source struct {
			Source string `json:"source"`
			URL    string `json:"url"`
			Path   string `json:"path"`
			SHA    string `json:"sha"`
		} `json:"source"`
	}, len(record.Packages))
	for index, pkg := range record.Packages {
		source, sourceErr := packageStateSource(record, pkg)
		if sourceErr != nil || source.Commit == "" {
			return nil, errors.New("project-shared source must be an exact Git commit")
		}
		remote, remoteErr := storedSourceRemote(source)
		if remoteErr != nil {
			return nil, errors.New("project-shared source must be an exact Git commit")
		}
		entry.Source.Plugins[index].Name = pkg.ID
		entry.Source.Plugins[index].Source.Source = "git-subdir"
		entry.Source.Plugins[index].Source.URL = remote.Endpoint()
		entry.Source.Plugins[index].Source.Path = pkg.Path
		entry.Source.Plugins[index].Source.SHA = source.Commit
	}
	return json.Marshal(entry)
}

func projectSettingsWithMarketplace(contents []byte, declarationID string, entry []byte, replace bool) ([]byte, error) {
	if len(contents) == 0 {
		contents = []byte("{}\n")
	}
	root, err := parseJSONObject(contents)
	if err != nil {
		return nil, err
	}
	marketplaces, present := findJSONMember(root, "extraKnownMarketplaces")
	if !present {
		value := []byte("{\n    " + quotedJSON(declarationID) + ": " + indentJSON(entry, "    ") + "\n  }")
		return insertJSONMember(contents, root, "extraKnownMarketplaces", value), nil
	}
	nested, err := parseJSONObject(contents[marketplaces.valueStart:marketplaces.valueEnd])
	if err != nil {
		return nil, errors.New("extraKnownMarketplaces is not a JSON object")
	}
	owned, exists := findJSONMember(nested, declarationID)
	if exists {
		actual := contents[marketplaces.valueStart+owned.valueStart : marketplaces.valueStart+owned.valueEnd]
		if jsonEqual(actual, entry) {
			if replace {
				return slicesReplace(contents, marketplaces.valueStart+owned.valueStart, marketplaces.valueStart+owned.valueEnd, entry), nil
			}
			return nil, errors.New("project marketplace declaration already exists without AI4J ownership")
		}
		if !replace {
			return nil, errors.New("project marketplace declaration conflicts")
		}
		return slicesReplace(contents, marketplaces.valueStart+owned.valueStart, marketplaces.valueStart+owned.valueEnd, entry), nil
	}
	updatedNested := insertJSONMember(contents[marketplaces.valueStart:marketplaces.valueEnd], nested, declarationID, entry)
	return slicesReplace(contents, marketplaces.valueStart, marketplaces.valueEnd, updatedNested), nil
}

func projectSettingsWithoutMarketplace(contents []byte, declarationID string, removeFile bool) ([]byte, error) {
	root, err := parseJSONObject(contents)
	if err != nil {
		return nil, err
	}
	marketplaces, present := findJSONMember(root, "extraKnownMarketplaces")
	if !present {
		return nil, errors.New("owned project marketplace declaration is missing")
	}
	nestedContents := contents[marketplaces.valueStart:marketplaces.valueEnd]
	nested, err := parseJSONObject(nestedContents)
	if err != nil {
		return nil, errors.New("extraKnownMarketplaces is not a JSON object")
	}
	owned, present := findJSONMember(nested, declarationID)
	if !present {
		return nil, errors.New("owned project marketplace declaration is missing")
	}
	updatedNested := removeJSONMember(nestedContents, nested, owned)
	updatedObject, err := parseJSONObject(updatedNested)
	if err != nil {
		return nil, err
	}
	var updated []byte
	if len(updatedObject.members) == 0 {
		updated = removeJSONMember(contents, root, marketplaces)
	} else {
		updated = slicesReplace(contents, marketplaces.valueStart, marketplaces.valueEnd, updatedNested)
	}
	if removeFile {
		withoutWhitespace := bytes.TrimSpace(updated)
		if bytes.Equal(withoutWhitespace, []byte("{}")) {
			return nil, nil
		}
	}
	return updated, nil
}

type parsedJSONObject struct {
	open, close int
	members     []jsonObjectMember
}

func parseJSONObject(contents []byte) (parsedJSONObject, error) {
	if len(contents) > maximumProjectMetadataBytes || !json.Valid(contents) {
		return parsedJSONObject{}, errors.New("project settings is not valid bounded JSON")
	}
	open := skipJSONSpace(contents, 0)
	if open >= len(contents) || contents[open] != '{' {
		return parsedJSONObject{}, errors.New("project settings must be a JSON object")
	}
	close, err := scanJSONValue(contents, open)
	if err != nil || skipJSONSpace(contents, close) != len(contents) {
		return parsedJSONObject{}, errors.New("project settings has trailing content")
	}
	object := parsedJSONObject{open: open, close: close - 1}
	position := skipJSONSpace(contents, open+1)
	if position == object.close {
		return object, nil
	}
	for position < object.close {
		keyStart := position
		keyEnd, err := scanJSONString(contents, position)
		if err != nil {
			return parsedJSONObject{}, err
		}
		var key string
		if json.Unmarshal(contents[keyStart:keyEnd], &key) != nil {
			return parsedJSONObject{}, errors.New("project settings key is invalid")
		}
		position = skipJSONSpace(contents, keyEnd)
		if position >= object.close || contents[position] != ':' {
			return parsedJSONObject{}, errors.New("project settings member is invalid")
		}
		valueStart := skipJSONSpace(contents, position+1)
		valueEnd, err := scanJSONValue(contents, valueStart)
		if err != nil {
			return parsedJSONObject{}, err
		}
		member := jsonObjectMember{key: key, keyStart: keyStart, valueStart: valueStart, valueEnd: valueEnd, commaAfter: -1}
		position = skipJSONSpace(contents, valueEnd)
		if position < object.close && contents[position] == ',' {
			member.commaAfter = position
			position = skipJSONSpace(contents, position+1)
		} else if position != object.close {
			return parsedJSONObject{}, errors.New("project settings object is invalid")
		}
		object.members = append(object.members, member)
	}
	return object, nil
}

func scanJSONValue(contents []byte, position int) (int, error) {
	if position >= len(contents) {
		return 0, errors.New("JSON value is missing")
	}
	switch contents[position] {
	case '"':
		return scanJSONString(contents, position)
	case '{', '[':
		open := contents[position]
		close := byte('}')
		if open == '[' {
			close = ']'
		}
		depth := 0
		for index := position; index < len(contents); index++ {
			if contents[index] == '"' {
				end, err := scanJSONString(contents, index)
				if err != nil {
					return 0, err
				}
				index = end - 1
				continue
			}
			if contents[index] == open {
				depth++
			} else if contents[index] == close {
				depth--
				if depth == 0 {
					return index + 1, nil
				}
			}
		}
	default:
		for index := position; index < len(contents); index++ {
			if bytes.ContainsRune([]byte(",}] \t\r\n"), rune(contents[index])) {
				return index, nil
			}
		}
	}
	return 0, errors.New("JSON value is incomplete")
}

func scanJSONString(contents []byte, position int) (int, error) {
	if position >= len(contents) || contents[position] != '"' {
		return 0, errors.New("JSON string is missing")
	}
	for index := position + 1; index < len(contents); index++ {
		if contents[index] == '\\' {
			index++
			continue
		}
		if contents[index] == '"' {
			return index + 1, nil
		}
	}
	return 0, errors.New("JSON string is incomplete")
}

func skipJSONSpace(contents []byte, position int) int {
	for position < len(contents) && bytes.ContainsRune([]byte(" \t\r\n"), rune(contents[position])) {
		position++
	}
	return position
}

func findJSONMember(object parsedJSONObject, key string) (jsonObjectMember, bool) {
	for _, member := range object.members {
		if member.key == key {
			return member, true
		}
	}
	return jsonObjectMember{}, false
}

func insertJSONMember(contents []byte, object parsedJSONObject, key string, value []byte) []byte {
	indent := jsonChildIndent(contents, object)
	member := []byte(quotedJSON(key) + ": " + string(value))
	if len(object.members) == 0 {
		return slicesReplace(contents, object.close, object.close, append(append([]byte("\n"+indent), member...), []byte("\n"+jsonParentIndent(contents, object.close))...))
	}
	last := object.members[len(object.members)-1]
	insert := append([]byte(",\n"+indent), member...)
	return slicesReplace(contents, last.valueEnd, last.valueEnd, insert)
}

func removeJSONMember(contents []byte, object parsedJSONObject, owned jsonObjectMember) []byte {
	index := 0
	for index < len(object.members) && object.members[index].keyStart != owned.keyStart {
		index++
	}
	if owned.commaAfter >= 0 {
		return slicesReplace(contents, owned.keyStart, owned.commaAfter+1, nil)
	}
	if index > 0 {
		previous := object.members[index-1]
		return slicesReplace(contents, previous.valueEnd, owned.valueEnd, nil)
	}
	return slicesReplace(contents, owned.keyStart, owned.valueEnd, nil)
}

func jsonParentIndent(contents []byte, position int) string {
	line := bytes.LastIndexByte(contents[:position], '\n')
	start := line + 1
	for start < position && (contents[start] == ' ' || contents[start] == '\t') {
		start++
	}
	return string(contents[line+1 : start])
}

func jsonChildIndent(contents []byte, object parsedJSONObject) string {
	if len(object.members) != 0 {
		member := object.members[0]
		line := bytes.LastIndexByte(contents[:member.keyStart], '\n')
		if line >= object.open {
			return string(contents[line+1 : member.keyStart])
		}
	}
	return jsonParentIndent(contents, object.close) + "  "
}

func quotedJSON(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func indentJSON(value []byte, prefix string) string {
	var output bytes.Buffer
	if json.Indent(&output, value, prefix, "  ") != nil {
		return string(value)
	}
	return output.String()
}

func jsonEqual(left, right []byte) bool {
	var a, b any
	return json.Unmarshal(left, &a) == nil && json.Unmarshal(right, &b) == nil && objectsEqual(a, b)
}

func objectsEqual(left, right any) bool {
	l, _ := json.Marshal(left)
	r, _ := json.Marshal(right)
	return bytes.Equal(l, r)
}

func slicesReplace(contents []byte, start, end int, replacement []byte) []byte {
	result := make([]byte, 0, len(contents)-(end-start)+len(replacement))
	result = append(result, contents[:start]...)
	result = append(result, replacement...)
	return append(result, contents[end:]...)
}

func readProjectSettings(path string) ([]byte, bool, error) {
	contents, _, err := readProjectMetadata(path)
	if err != nil {
		return nil, false, err
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	return contents, true, nil
}

func applyProjectSettings(path string, before, after []byte) error {
	current, present, err := readProjectSettings(path)
	if err != nil {
		return err
	}
	if before == nil {
		if present {
			return errors.New("project settings appeared after planning")
		}
	} else if !present || !bytes.Equal(current, before) {
		return errors.New("project settings changed after planning")
	}
	if after == nil {
		if !present {
			return nil
		}
		return os.Remove(path)
	}
	mode := os.FileMode(0o600)
	if present {
		if info, statErr := os.Stat(path); statErr != nil {
			return statErr
		} else {
			mode = info.Mode().Perm()
		}
	}
	return replaceProjectMetadata(path, after, mode)
}

func projectSettingsPath(record installstate.Record) string {
	return filepath.Join(record.ScopeRoot, ".claude", "settings.json")
}

func projectMarketplaceFromSettings(contents []byte, declarationID string) ([]byte, bool, error) {
	if len(contents) == 0 {
		return nil, false, nil
	}
	root, err := parseJSONObject(contents)
	if err != nil {
		return nil, false, err
	}
	marketplaces, present := findJSONMember(root, "extraKnownMarketplaces")
	if !present {
		return nil, false, nil
	}
	nestedContents := contents[marketplaces.valueStart:marketplaces.valueEnd]
	nested, err := parseJSONObject(nestedContents)
	if err != nil {
		return nil, false, errors.New("extraKnownMarketplaces is not a JSON object")
	}
	owned, present := findJSONMember(nested, declarationID)
	if !present {
		return nil, false, nil
	}
	return slices.Clone(nestedContents[owned.valueStart:owned.valueEnd]), true, nil
}

func canonicalJSONDigest(contents []byte) (string, error) {
	var value any
	if err := json.Unmarshal(contents, &value); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return sha256Digest(canonical), nil
}

func inspectProjectMarketplaceDrift(record installstate.Record) cli.DriftState {
	contents, present, err := readProjectSettings(projectSettingsPath(record))
	if err != nil {
		return cli.DriftConflicting
	}
	if !present {
		return cli.DriftMissing
	}
	entry, present, err := projectMarketplaceFromSettings(contents, record.DeclarationID)
	if err != nil {
		return cli.DriftConflicting
	}
	if !present {
		return cli.DriftMissing
	}
	digest, err := canonicalJSONDigest(entry)
	if err != nil {
		return cli.DriftConflicting
	}
	if digest != record.Catalog.Checksum {
		return cli.DriftModified
	}
	return cli.DriftUnchanged
}

func projectMarketplaceAbsent(record installstate.Record) bool {
	contents, present, err := readProjectSettings(projectSettingsPath(record))
	if err != nil || !present {
		return err == nil
	}
	_, present, err = projectMarketplaceFromSettings(contents, record.DeclarationID)
	return err == nil && !present
}

func (s *lifecycleService) inspectProjectSharedNativeCatalogDrift(record installstate.Record) cli.DriftState {
	owned, err := projectSharedNativeCatalogFile(record)
	if err != nil {
		return cli.DriftConflicting
	}
	return inspectFileDrift(s.projectSharedNativeCatalogPath(record), owned.Checksum)
}

func (s *lifecycleService) projectSharedNativeCatalogAbsent(record installstate.Record) bool {
	_, err := os.Lstat(s.projectSharedNativeCatalogPath(record))
	return errors.Is(err, os.ErrNotExist)
}

// preflightProjectSharedTransition proves every AI4J-owned project-shared
// input before Claude or project settings can be mutated. Project marketplace
// declarations are never replaced when they drift, even under replace-owned;
// the surrounding file belongs to the project and may contain unrelated data.
func (s *lifecycleService) preflightProjectSharedTransition(before, desired *installstate.Record, policy cli.ConflictPolicy) error {
	if desired == nil || desired.Scope != "project-shared" {
		return nil
	}
	newInstallation := before == nil || before.Lifecycle == "archived"
	if newInstallation {
		if desired.Lifecycle != "active" {
			return errors.New("project-shared transition has no active endpoint")
		}
		if _, err := projectSharedNativeCatalogFile(*desired); err != nil {
			return err
		}
		if _, err := os.Lstat(s.projectSharedNativeCatalogPath(*desired)); err == nil {
			return errors.New("project-shared native catalog destination is occupied")
		} else if !errors.Is(err, os.ErrNotExist) {
			return errors.New("project-shared native catalog destination cannot be inspected")
		}
		contents, present, err := readProjectSettings(projectSettingsPath(*desired))
		if err != nil {
			return err
		}
		if present {
			_, declared, declarationErr := projectMarketplaceFromSettings(contents, desired.DeclarationID)
			if declarationErr != nil {
				return declarationErr
			}
			if declared {
				return errors.New("project marketplace declaration destination is occupied")
			}
		}
		return s.preflightProjectSharedRules(nil, *desired, policy)
	}

	if before.Scope != "project-shared" || inspectProjectMarketplaceDrift(*before) != cli.DriftUnchanged {
		return errors.New("project marketplace does not match installation state")
	}
	if desired.Lifecycle == "active" {
		if _, err := projectSharedNativeCatalogFile(*desired); err != nil {
			return err
		}
	}
	if _, err := projectSharedNativeCatalogFile(*before); err != nil {
		return err
	}
	drift := s.inspectProjectSharedNativeCatalogDrift(*before)
	if drift == cli.DriftUnchanged {
		return s.preflightProjectSharedRules(before, *desired, policy)
	}
	if policy == cli.ConflictReplaceOwned && (drift == cli.DriftMissing || drift == cli.DriftModified) {
		return s.preflightProjectSharedRules(before, *desired, policy)
	}
	return errors.New("project-shared native catalog does not match installation state")
}

func (s *lifecycleService) preflightProjectSharedRules(before *installstate.Record, desired installstate.Record, policy cli.ConflictPolicy) error {
	if before == nil || before.Lifecycle == "archived" || before.Rules == (installstate.OwnedFile{}) {
		if desired.Rules == (installstate.OwnedFile{}) {
			return nil
		}
		if _, err := os.Lstat(s.rulesPath(desired)); errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return errors.New("project-shared rules destination is occupied or cannot be inspected")
	}
	drift := inspectFileDrift(s.rulesPath(*before), before.Rules.Checksum)
	if drift == cli.DriftUnchanged {
		return nil
	}
	if policy == cli.ConflictReplaceOwned && (drift == cli.DriftMissing || drift == cli.DriftModified) {
		return nil
	}
	return errors.New("project-shared rules do not match installation state")
}

func (s *lifecycleService) planProjectShared(record *installstate.Record, previous *installstate.Record) ([]byte, []byte, error) {
	if record.Scope != "project-shared" {
		return nil, nil, nil
	}
	if record.DeclarationID == "" {
		record.DeclarationID = record.ToolkitID
	}
	if previous != nil && previous.Lifecycle == "active" && previous.DeclarationID != record.DeclarationID {
		return nil, nil, errors.New("project declaration identity is immutable")
	}
	nativeCatalog, err := projectSharedNativeCatalogFile(*record)
	if err != nil {
		return nil, nil, err
	}
	record.NativeCatalog = nativeCatalog
	record.Catalog.Path = ".claude/settings.json"
	before, present, err := readProjectSettings(projectSettingsPath(*record))
	if err != nil {
		return nil, nil, err
	}
	replace := previous != nil && previous.Lifecycle == "active"
	if replace && (!present || inspectProjectMarketplaceDrift(*previous) != cli.DriftUnchanged) {
		return nil, nil, errors.New("project settings changed outside AI4J")
	}
	entry, err := projectMarketplaceEntry(*record)
	if err != nil {
		return nil, nil, err
	}
	after, err := projectSettingsWithMarketplace(before, record.DeclarationID, entry, replace)
	if err != nil {
		return nil, nil, err
	}
	record.SettingsCreated = !present
	if previous != nil {
		record.SettingsCreated = previous.SettingsCreated
	}
	record.Catalog.Checksum, err = canonicalJSONDigest(entry)
	if err != nil {
		return nil, nil, err
	}
	return before, after, record.Validate()
}

func (s *lifecycleService) planProjectSharedRemoval(record installstate.Record) ([]byte, error) {
	if record.Scope != "project-shared" {
		return nil, nil
	}
	path := projectSettingsPath(record)
	before, present, err := readProjectSettings(path)
	if err != nil || !present || inspectProjectMarketplaceDrift(record) != cli.DriftUnchanged {
		return nil, errors.New("owned project settings changed outside AI4J")
	}
	return projectSettingsWithoutMarketplace(before, record.DeclarationID, record.SettingsCreated)
}

func (s *lifecycleService) planProjectSharedRollback(current installstate.Record, desired *installstate.Record, desiredEntry []byte) ([]byte, []byte, error) {
	path := projectSettingsPath(current)
	before, present, err := readProjectSettings(path)
	if err != nil {
		return nil, nil, err
	}
	if current.Lifecycle == "active" {
		if !present || inspectProjectMarketplaceDrift(current) != cli.DriftUnchanged {
			return nil, nil, errors.New("project settings changed outside AI4J")
		}
	} else if !projectMarketplaceAbsent(current) {
		return nil, nil, errors.New("project marketplace appeared after the rollback point")
	}
	var after []byte
	if desired.Lifecycle == "active" {
		if len(desiredEntry) == 0 {
			return nil, nil, errors.New("owned project marketplace rollback entry is missing")
		}
		after, err = projectSettingsWithMarketplace(before, desired.DeclarationID, desiredEntry, current.Lifecycle == "active")
		if err == nil {
			checksum, digestErr := canonicalJSONDigest(desiredEntry)
			if digestErr != nil {
				return nil, nil, digestErr
			}
			desired.Catalog = installstate.OwnedFile{Path: ".claude/settings.json", Checksum: checksum}
			desired.NativeCatalog, digestErr = projectSharedNativeCatalogFile(*desired)
			if digestErr != nil {
				return nil, nil, digestErr
			}
		}
	} else {
		after, err = projectSettingsWithoutMarketplace(before, current.DeclarationID, desired.SettingsCreated)
		desired.Catalog = installstate.OwnedFile{}
		desired.NativeCatalog = installstate.OwnedFile{}
	}
	if err != nil {
		return nil, nil, err
	}
	return before, after, desired.Validate()
}

func projectSharedOwnedEntry(record *installstate.Record) []byte {
	if record == nil || record.Scope != "project-shared" || record.Lifecycle != "active" {
		return nil
	}
	entry, err := projectMarketplaceEntry(*record)
	if err != nil {
		return nil
	}
	return entry
}

func projectSharedNativeCatalog(record installstate.Record) ([]byte, error) {
	packages := make([]catalog.Package, len(record.Packages))
	for index, pkg := range record.Packages {
		source, err := packageStateSource(record, pkg)
		if err != nil {
			return nil, err
		}
		repository, err := domain.NewRepositoryIdentity(source.Repository)
		if err != nil {
			return nil, err
		}
		commit, err := domain.NewCommitOID(source.Commit)
		if err != nil {
			return nil, err
		}
		transport, err := storedSourceTransport(source)
		if err != nil {
			return nil, err
		}
		packages[index] = catalog.Package{ID: pkg.ID, Path: pkg.Path, Description: "AI4J toolkit package " + pkg.ID, Repository: repository, Transport: transport, Commit: commit}
	}
	document, err := catalog.RenderPackages(record.MarketplaceID, packages)
	if err != nil {
		return nil, err
	}
	return document.Bytes(), nil
}

func packageStateSource(record installstate.Record, pkg installstate.NativePackage) (installstate.Source, error) {
	if len(record.Components) == 0 {
		return record.Source, nil
	}
	for _, component := range record.Components {
		if component.Name == pkg.Component {
			return component.Source, nil
		}
	}
	return installstate.Source{}, errors.New("package component source is unavailable")
}

func projectSharedNativeCatalogFile(record installstate.Record) (installstate.OwnedFile, error) {
	contents, err := projectSharedNativeCatalog(record)
	if err != nil {
		return installstate.OwnedFile{}, err
	}
	file := installstate.OwnedFile{
		Path:     "state/catalogs/" + record.InstallationID + "/.claude-plugin/marketplace.json",
		Checksum: sha256Digest(contents),
	}
	if record.NativeCatalog != (installstate.OwnedFile{}) && record.NativeCatalog != file {
		return installstate.OwnedFile{}, errors.New("project-shared native catalog does not match installation state")
	}
	return file, nil
}

func (s *lifecycleService) projectSharedNativeCatalogPath(record installstate.Record) string {
	relative := record.NativeCatalog.Path
	if relative == "" {
		relative = "state/catalogs/" + record.InstallationID + "/.claude-plugin/marketplace.json"
	}
	return filepath.Join(s.state.DataRoot(), filepath.FromSlash(relative))
}

func (s *lifecycleService) writeProjectSharedNativeCatalog(before, desired *installstate.Record, policy cli.ConflictPolicy) (string, error) {
	contents, err := projectSharedNativeCatalog(*desired)
	if err != nil {
		return "", err
	}
	owned, err := projectSharedNativeCatalogFile(*desired)
	if err != nil || sha256Digest(contents) != owned.Checksum {
		return "", errors.New("project-shared native catalog provenance is invalid")
	}
	path := s.projectSharedNativeCatalogPath(*desired)
	if before == nil || before.Lifecycle == "archived" {
		if err := writeOwnedNew(s.state.DataRoot(), path, contents); err != nil {
			return "", err
		}
		return filepath.Dir(filepath.Dir(path)), nil
	}
	previous, err := projectSharedNativeCatalog(*before)
	if err != nil {
		return "", err
	}
	previousOwned, err := projectSharedNativeCatalogFile(*before)
	if err != nil || sha256Digest(previous) != previousOwned.Checksum {
		return "", errors.New("existing project-shared native catalog provenance is invalid")
	}
	if err := mutateOwned(s.state.DataRoot(), path, previousOwned.Checksum, contents, policy); err != nil {
		return "", err
	}
	return filepath.Dir(filepath.Dir(path)), nil
}

func (s *lifecycleService) removeProjectSharedNativeCatalog(record installstate.Record, policy cli.ConflictPolicy) error {
	if record.NativeCatalog == (installstate.OwnedFile{}) {
		return nil
	}
	owned, err := projectSharedNativeCatalogFile(record)
	if err != nil {
		return err
	}
	path := s.projectSharedNativeCatalogPath(record)
	if err := mutateOwned(s.state.DataRoot(), path, owned.Checksum, nil, policy); err != nil {
		return err
	}
	for _, directory := range []string{filepath.Dir(path), filepath.Dir(filepath.Dir(path))} {
		if err := os.Remove(directory); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func replaceProjectMarketplace(record installstate.Record, entry []byte, expectedChecksum, expectedDirectory string) error {
	path := projectSettingsPath(record)
	before, _, err := readProjectSettings(path)
	if err != nil {
		return err
	}
	current, present, err := projectMarketplaceFromSettings(before, record.DeclarationID)
	if err != nil || !present {
		return errors.New("project marketplace declaration changed before replacement")
	}
	switch {
	case expectedChecksum != "" && expectedDirectory == "":
		digest, digestErr := canonicalJSONDigest(current)
		if digestErr != nil || digest != expectedChecksum {
			return errors.New("project marketplace declaration changed before replacement")
		}
	case expectedChecksum == "" && expectedDirectory != "":
		expected := []byte(`{"source":{"source":"directory","path":` + quotedJSON(expectedDirectory) + `}}`)
		if !jsonEqual(current, expected) {
			return errors.New("native project marketplace declaration is not the catalog AI4J registered")
		}
	default:
		return errors.New("project marketplace replacement expectation is invalid")
	}
	after, err := projectSettingsWithMarketplace(before, record.DeclarationID, entry, true)
	if err != nil {
		return err
	}
	return applyProjectSettings(path, before, after)
}

func removeProjectMarketplace(record installstate.Record) error {
	path := projectSettingsPath(record)
	before, present, err := readProjectSettings(path)
	if err != nil || !present {
		return err
	}
	entry, present, err := projectMarketplaceFromSettings(before, record.DeclarationID)
	if err != nil || !present {
		return err
	}
	digest, err := canonicalJSONDigest(entry)
	if err != nil {
		return err
	}
	if digest != record.Catalog.Checksum {
		return errors.New("project marketplace does not match installation state")
	}
	after, err := projectSettingsWithoutMarketplace(before, record.DeclarationID, record.SettingsCreated)
	if err != nil {
		return err
	}
	return applyProjectSettings(path, before, after)
}

func removeCreatedProjectSettingsIfEmpty(record installstate.Record) error {
	if !record.SettingsCreated {
		return nil
	}
	path := projectSettingsPath(record)
	contents, present, err := readProjectSettings(path)
	if err != nil || !present {
		return err
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(contents, &document); err != nil {
		return err
	}
	for key, raw := range document {
		if key != "enabledPlugins" && key != "extraKnownMarketplaces" {
			return nil
		}
		var entries map[string]json.RawMessage
		if err := json.Unmarshal(raw, &entries); err != nil || len(entries) != 0 {
			return nil
		}
	}
	return applyProjectSettings(path, contents, nil)
}

func (s *lifecycleService) applyProjectSharedTransition(ctx context.Context, before, desired *installstate.Record, settingsBefore, rules []byte, policy cli.ConflictPolicy) error {
	if err := s.preflightProjectSharedTransition(before, desired, policy); err != nil {
		return err
	}
	if desired.Lifecycle == "archived" {
		if before == nil || before.Lifecycle != "active" {
			return nil
		}
		for _, pluginID := range nativePluginIDs(*before) {
			if err := s.runClaudeFor(ctx, *before, []string{"plugin", "uninstall", pluginID, "--scope", nativeScope(*before), "--keep-data"}); err != nil {
				return err
			}
		}
		if err := s.runClaudeFor(ctx, *before, []string{"plugin", "marketplace", "remove", before.MarketplaceID, "--scope", nativeScope(*before)}); err != nil {
			return err
		}
		if !projectMarketplaceAbsent(*before) {
			if err := removeProjectMarketplace(*before); err != nil {
				return err
			}
		}
		if err := removeCreatedProjectSettingsIfEmpty(*before); err != nil {
			return err
		}
		if err := s.removeProjectSharedNativeCatalog(*before, policy); err != nil {
			return err
		}
		if before.Rules != (installstate.OwnedFile{}) {
			return mutateOwned(before.ScopeRoot, s.rulesPath(*before), before.Rules.Checksum, nil, policy)
		}
		return nil
	}

	newInstallation := before == nil || before.Lifecycle == "archived"
	entry, err := projectMarketplaceEntry(*desired)
	if err != nil {
		return err
	}
	if newInstallation {
		current, present, err := readProjectSettings(projectSettingsPath(*desired))
		if err != nil || settingsBefore == nil && present || settingsBefore != nil && (!present || !bytes.Equal(current, settingsBefore)) {
			return errors.New("project settings changed after planning")
		}
	} else if inspectProjectMarketplaceDrift(*before) != cli.DriftUnchanged {
		return errors.New("project marketplace does not match installation state")
	}
	if newInstallation {
		catalogRoot, err := s.writeProjectSharedNativeCatalog(before, desired, policy)
		if err != nil {
			return err
		}
		if !projectMarketplaceAbsent(*desired) {
			return errors.New("project marketplace declaration appeared before native registration")
		}
		if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "marketplace", "add", catalogRoot, "--scope", nativeScope(*desired)}); err != nil {
			return err
		}
		if err := replaceProjectMarketplace(*desired, entry, "", catalogRoot); err != nil {
			return err
		}
		for _, pluginID := range nativePluginIDs(*desired) {
			if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "install", pluginID, "--scope", nativeScope(*desired)}); err != nil {
				return err
			}
		}
	} else if catalogTransitionNeeded(s, *before, *desired) {
		for _, pkg := range before.Packages {
			if err := s.runClaudeFor(ctx, *before, []string{"plugin", "uninstall", nativePluginID(pkg, before.MarketplaceID), "--scope", nativeScope(*before), "--keep-data"}); err != nil {
				return err
			}
		}
		if _, err := s.writeProjectSharedNativeCatalog(before, desired, policy); err != nil {
			return err
		}
		if err := replaceProjectMarketplace(*desired, entry, before.Catalog.Checksum, ""); err != nil {
			return err
		}
		if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "marketplace", "update", desired.MarketplaceID, "--scope", nativeScope(*desired)}); err != nil {
			return err
		}
		for _, pkg := range desired.Packages {
			if err := s.runClaudeFor(ctx, *desired, []string{"plugin", "install", nativePluginID(pkg, desired.MarketplaceID), "--scope", nativeScope(*desired)}); err != nil {
				return err
			}
		}
	}
	switch {
	case newInstallation && desired.Rules != (installstate.OwnedFile{}):
		return writeOwnedNew(desired.ScopeRoot, s.rulesPath(*desired), rules)
	case !newInstallation && before.Rules == (installstate.OwnedFile{}) && desired.Rules != (installstate.OwnedFile{}):
		return writeOwnedNew(desired.ScopeRoot, s.rulesPath(*desired), rules)
	case !newInstallation && before.Rules != (installstate.OwnedFile{}) && desired.Rules == (installstate.OwnedFile{}):
		return mutateOwned(before.ScopeRoot, s.rulesPath(*before), before.Rules.Checksum, nil, policy)
	case !newInstallation && before.Rules != (installstate.OwnedFile{}) && (before.Rules.Checksum != desired.Rules.Checksum || inspectFileDrift(s.rulesPath(*before), before.Rules.Checksum) != cli.DriftUnchanged):
		return mutateOwned(before.ScopeRoot, s.rulesPath(*before), before.Rules.Checksum, rules, policy)
	}
	return nil
}
