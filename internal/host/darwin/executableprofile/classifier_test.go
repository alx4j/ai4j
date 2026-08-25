package executableprofile_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/alx4j/ai4j/internal/host/darwin/executableprofile"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

const (
	machOMagic64 = 0xfeedfacf
	fatMagic32   = 0xcafebabe
	fatMagic64   = 0xcafebabf
	cpuARM64     = 0x0100000c
	cpuX8664     = 0x01000007
)

func TestClassifyThinNativeHeader(t *testing.T) {
	t.Parallel()

	profile, err := executableprofile.Classify(bytes.NewReader(thin(binary.LittleEndian, cpuARM64, 2)), 32)
	if err != nil {
		t.Fatal(err)
	}
	native, ok := profile.Native()
	if !ok || profile.Kind() != lifecycle.StaticExecutableNative || native.Layout() != lifecycle.NativeSingleImage ||
		native.Role() != lifecycle.NativeExecutable || native.Architectures() != lifecycle.ExecutableARM64 {
		t.Fatalf("unexpected native profile: %#v", profile)
	}
}

func TestClassifyFatNativeHeaderProjectsArchitecturesAndRole(t *testing.T) {
	t.Parallel()

	profile, err := executableprofile.Classify(bytes.NewReader(fat(cpuARM64, cpuX8664, 2, 2)), 128)
	if err != nil {
		t.Fatal(err)
	}
	native, ok := profile.Native()
	wantArchitectures := lifecycle.ExecutableARM64 | lifecycle.ExecutableX8664
	if !ok || native.Layout() != lifecycle.NativeMultiImage || native.Role() != lifecycle.NativeExecutable ||
		native.Architectures() != wantArchitectures {
		t.Fatalf("unexpected fat profile: %#v", profile)
	}
}

func TestClassifyRejectsFat64Header(t *testing.T) {
	t.Parallel()

	content := fat64(cpuARM64, cpuX8664, 2, 2)
	profile, err := executableprofile.Classify(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	if issue, ok := profile.Issue(); !ok || issue != lifecycle.StaticIssueUnsupportedFormat {
		t.Fatalf("unexpected fat64 profile: %#v", profile)
	}
}

func TestClassifyRejectsReverseEndianAndUnsupportedNativeWrappers(t *testing.T) {
	t.Parallel()

	littleFat := fat(cpuARM64, cpuX8664, 2, 2)
	for offset := 0; offset < 48; offset += 4 {
		value := binary.BigEndian.Uint32(littleFat[offset : offset+4])
		binary.LittleEndian.PutUint32(littleFat[offset:offset+4], value)
	}
	tests := []struct {
		name    string
		content []byte
		issue   lifecycle.StaticExecutableIssue
	}{
		{name: "reverse-endian thin", content: thin(binary.BigEndian, cpuARM64, 2), issue: lifecycle.StaticIssueUnsupportedFormat},
		{name: "reverse-endian fat", content: littleFat, issue: lifecycle.StaticIssueUnsupportedFormat},
		{name: "fat64", content: fat64(cpuARM64, cpuX8664, 2, 2), issue: lifecycle.StaticIssueUnsupportedFormat},
	}
	for _, test := range tests {
		profile, err := executableprofile.Classify(bytes.NewReader(test.content), int64(len(test.content)))
		if err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		if issue, ok := profile.Issue(); !ok || issue != test.issue {
			t.Fatalf("%s: issue=%q ok=%t", test.name, issue, ok)
		}
	}
}

func TestClassifyRequiresSupportedAndExactlyBoundNativeSubtypes(t *testing.T) {
	t.Parallel()

	for _, subtype := range []uint32{0, 1, 2, 0x80000002, 0x81000002} {
		content := thinWithSubtype(binary.LittleEndian, cpuARM64, subtype, 2)
		profile, err := executableprofile.Classify(bytes.NewReader(content), int64(len(content)))
		if err != nil || profileArchitecture(profile) != lifecycle.ExecutableARM64 {
			t.Fatalf("supported arm64 subtype %#x: profile=%#v err=%v", subtype, profile, err)
		}
	}
	for _, subtype := range []uint32{3, 8} {
		content := thinWithSubtype(binary.LittleEndian, cpuX8664, subtype, 2)
		profile, err := executableprofile.Classify(bytes.NewReader(content), int64(len(content)))
		if err != nil || profileArchitecture(profile) != lifecycle.ExecutableX8664 {
			t.Fatalf("supported x86_64 subtype %#x: profile=%#v err=%v", subtype, profile, err)
		}
	}
	for _, test := range []struct {
		cpu, subtype uint32
	}{
		{cpu: cpuARM64, subtype: 3},
		{cpu: cpuARM64, subtype: 0x82000002},
		{cpu: cpuX8664, subtype: 4},
		{cpu: cpuX8664, subtype: 0x80000003},
	} {
		content := thinWithSubtype(binary.LittleEndian, test.cpu, test.subtype, 2)
		profile, err := executableprofile.Classify(bytes.NewReader(content), int64(len(content)))
		if err != nil {
			t.Fatal(err)
		}
		if issue, ok := profile.Issue(); !ok || issue != lifecycle.StaticIssueUnsupportedArchitecture {
			t.Fatalf("unsupported subtype cpu=%#x subtype=%#x issue=%q ok=%t", test.cpu, test.subtype, issue, ok)
		}
	}

	mismatch := fat(cpuARM64, cpuX8664, 2, 2)
	binary.BigEndian.PutUint32(mismatch[12:16], 1)
	profile, err := executableprofile.Classify(bytes.NewReader(mismatch), int64(len(mismatch)))
	if err != nil {
		t.Fatal(err)
	}
	if issue, ok := profile.Issue(); !ok || issue != lifecycle.StaticIssueMalformedHeader {
		t.Fatalf("outer/inner subtype mismatch issue=%q ok=%t", issue, ok)
	}

	distinctARM := make([]byte, 128)
	binary.BigEndian.PutUint32(distinctARM[0:4], fatMagic32)
	binary.BigEndian.PutUint32(distinctARM[4:8], 2)
	writeFatEntryWithSubtype(distinctARM[8:28], cpuARM64, 0, 64, 32, 0)
	writeFatEntryWithSubtype(distinctARM[28:48], cpuARM64, 2, 96, 32, 0)
	copy(distinctARM[64:96], thinWithSubtype(binary.LittleEndian, cpuARM64, 0, 2))
	copy(distinctARM[96:128], thinWithSubtype(binary.LittleEndian, cpuARM64, 2, 2))
	profile, err = executableprofile.Classify(bytes.NewReader(distinctARM), int64(len(distinctARM)))
	if err != nil || profileArchitecture(profile) != lifecycle.ExecutableARM64 {
		t.Fatalf("distinct arm64/arm64e slices: profile=%#v err=%v", profile, err)
	}
}

func TestClassifyValidatesFatSliceAlignment(t *testing.T) {
	t.Parallel()

	valid := fat(cpuARM64, cpuX8664, 2, 2)
	binary.BigEndian.PutUint32(valid[24:28], 5)
	binary.BigEndian.PutUint32(valid[44:48], 5)
	if profile, err := executableprofile.Classify(bytes.NewReader(valid), int64(len(valid))); err != nil || profile.Kind() != lifecycle.StaticExecutableNative {
		t.Fatalf("aligned fat profile=%#v err=%v", profile, err)
	}

	for _, mutate := range []func([]byte){
		func(content []byte) { binary.BigEndian.PutUint32(content[24:28], 32) },
		func(content []byte) { binary.BigEndian.PutUint32(content[24:28], 7) },
	} {
		content := fat(cpuARM64, cpuX8664, 2, 2)
		mutate(content)
		profile, err := executableprofile.Classify(bytes.NewReader(content), int64(len(content)))
		if err != nil {
			t.Fatal(err)
		}
		if issue, ok := profile.Issue(); !ok || issue != lifecycle.StaticIssueMalformedHeader {
			t.Fatalf("invalid alignment issue=%q ok=%t", issue, ok)
		}
	}
}

func TestClassifyRejectsInconsistentAndDuplicateFatImages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		bytes []byte
		issue lifecycle.StaticExecutableIssue
	}{
		{name: "inconsistent role", bytes: fat(cpuARM64, cpuX8664, 2, 6), issue: lifecycle.StaticIssueInconsistentFileRole},
		{name: "duplicate architecture", bytes: fat(cpuARM64, cpuARM64, 2, 2), issue: lifecycle.StaticIssueMalformedHeader},
	}
	for _, test := range tests {
		profile, err := executableprofile.Classify(bytes.NewReader(test.bytes), int64(len(test.bytes)))
		if err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		issue, ok := profile.Issue()
		if !ok || issue != test.issue {
			t.Fatalf("%s: issue=%q ok=%t", test.name, issue, ok)
		}
	}
}

func TestClassifyBoundsFatCountsAndRanges(t *testing.T) {
	t.Parallel()

	tooMany := make([]byte, 8)
	binary.BigEndian.PutUint32(tooMany[0:4], fatMagic32)
	binary.BigEndian.PutUint32(tooMany[4:8], 17)
	profile, err := executableprofile.Classify(bytes.NewReader(tooMany), int64(len(tooMany)))
	if err != nil {
		t.Fatal(err)
	}
	if issue, ok := profile.Issue(); !ok || issue != lifecycle.StaticIssueTooManyArchitectures {
		t.Fatalf("too-many profile issue=%q ok=%t", issue, ok)
	}

	truncated := fat(cpuARM64, cpuX8664, 2, 2)[:60]
	profile, err = executableprofile.Classify(bytes.NewReader(truncated), int64(len(truncated)))
	if err != nil {
		t.Fatal(err)
	}
	if issue, ok := profile.Issue(); !ok || issue != lifecycle.StaticIssueTruncatedHeader {
		t.Fatalf("truncated profile issue=%q ok=%t", issue, ok)
	}
}

func TestClassifyShebangFormsAndIssues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		content   string
		form      lifecycle.ShebangForm
		ambiguity lifecycle.ShebangAmbiguity
		kind      lifecycle.StaticExecutableKind
		issue     lifecycle.StaticExecutableIssue
	}{
		{name: "direct", content: "#!/usr/local/bin/node --no-warnings\n", form: lifecycle.ShebangDirect, kind: lifecycle.StaticExecutableScript},
		{name: "simple env", content: "#!/usr/bin/env node\n", form: lifecycle.ShebangEnv, kind: lifecycle.StaticExecutableScript},
		{name: "env option", content: "#!/usr/bin/env -S node --flag\n", form: lifecycle.ShebangEnvAmbiguous, ambiguity: lifecycle.ShebangEnvOption, kind: lifecycle.StaticExecutableScript},
		{name: "env assignment", content: "#!/usr/bin/env NODE_OPTIONS=x\n", form: lifecycle.ShebangEnvAmbiguous, ambiguity: lifecycle.ShebangEnvAssignment, kind: lifecycle.StaticExecutableScript},
		{name: "env multiple", content: "#!/usr/bin/env node argument\n", form: lifecycle.ShebangEnvAmbiguous, ambiguity: lifecycle.ShebangEnvMultipleTokens, kind: lifecycle.StaticExecutableScript},
		{name: "env missing", content: "#!/usr/bin/env\n", form: lifecycle.ShebangEnvAmbiguous, ambiguity: lifecycle.ShebangEnvMissingTarget, kind: lifecycle.StaticExecutableScript},
		{name: "other env path is direct", content: "#!/opt/bin/env node\n", form: lifecycle.ShebangDirect, kind: lifecycle.StaticExecutableScript},
		{name: "noncanonical", content: "#!//usr/bin/node\n", kind: lifecycle.StaticExecutableMalformed, issue: lifecycle.StaticIssueMalformedShebang},
		{name: "nul", content: "#!/usr/bin/no\x00de\n", kind: lifecycle.StaticExecutableMalformed, issue: lifecycle.StaticIssueMalformedShebang},
		{name: "crlf", content: "#!/usr/bin/node\r\n", kind: lifecycle.StaticExecutableMalformed, issue: lifecycle.StaticIssueMalformedShebang},
	}
	for _, test := range tests {
		profile, err := executableprofile.Classify(strings.NewReader(test.content), int64(len(test.content)))
		if err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		if profile.Kind() != test.kind {
			t.Fatalf("%s: kind=%q want=%q", test.name, profile.Kind(), test.kind)
		}
		if test.issue != "" {
			issue, ok := profile.Issue()
			if !ok || issue != test.issue {
				t.Fatalf("%s: issue=%q ok=%t", test.name, issue, ok)
			}
			continue
		}
		shebang, ok := profile.Shebang()
		if !ok || shebang.Form() != test.form || shebang.Ambiguity() != test.ambiguity {
			t.Fatalf("%s: shebang=%#v ok=%t", test.name, shebang, ok)
		}
	}
}

func TestClassifyShebangLimitIsDeterministic(t *testing.T) {
	t.Parallel()

	content := "#!/usr/bin/node " + strings.Repeat("x", 600)
	profile, err := executableprofile.Classify(strings.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	if issue, ok := profile.Issue(); !ok || issue != lifecycle.StaticIssueTruncatedShebang {
		t.Fatalf("issue=%q ok=%t", issue, ok)
	}
}

func TestClassifyMatchesDarwinShebangTerminationAndArgumentBounds(t *testing.T) {
	t.Parallel()

	lastIndexEOL := "#!/usr/bin/node" + strings.Repeat(" ", 511-len("#!/usr/bin/node")) + "\n"
	afterLimitEOL := "#!/usr/bin/node" + strings.Repeat(" ", 512-len("#!/usr/bin/node")) + "\n"
	tests := []struct {
		name    string
		content string
		kind    lifecycle.StaticExecutableKind
		form    lifecycle.ShebangForm
		issue   lifecycle.StaticExecutableIssue
	}{
		{name: "hash comment", content: "#!/usr/bin/env node # ignored tokens\n", kind: lifecycle.StaticExecutableScript, form: lifecycle.ShebangEnv},
		{name: "last in-buffer newline", content: lastIndexEOL, kind: lifecycle.StaticExecutableScript, form: lifecycle.ShebangDirect},
		{name: "newline after buffer", content: afterLimitEOL, kind: lifecycle.StaticExecutableTruncated, issue: lifecycle.StaticIssueTruncatedShebang},
		{name: "no newline", content: "#!/usr/bin/node", kind: lifecycle.StaticExecutableTruncated, issue: lifecycle.StaticIssueTruncatedShebang},
		{name: "multiple direct arguments", content: "#!/usr/bin/node --first --second\n", kind: lifecycle.StaticExecutableMalformed, issue: lifecycle.StaticIssueMalformedShebang},
	}
	for _, test := range tests {
		profile, err := executableprofile.Classify(strings.NewReader(test.content), int64(len(test.content)))
		if err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		if profile.Kind() != test.kind {
			t.Fatalf("%s: kind=%q want=%q", test.name, profile.Kind(), test.kind)
		}
		if test.issue != "" {
			if issue, ok := profile.Issue(); !ok || issue != test.issue {
				t.Fatalf("%s: issue=%q ok=%t", test.name, issue, ok)
			}
			continue
		}
		shebang, ok := profile.Shebang()
		if !ok || shebang.Form() != test.form {
			t.Fatalf("%s: shebang=%#v ok=%t", test.name, shebang, ok)
		}
	}
}

func TestClassifyUsesOnlyBoundedRandomAccessReads(t *testing.T) {
	t.Parallel()

	reader := &readSpy{content: thin(binary.LittleEndian, cpuARM64, 2)}
	profile, err := executableprofile.Classify(reader, 1<<40)
	if err != nil || profile.Kind() != lifecycle.StaticExecutableNative {
		t.Fatalf("profile=%#v err=%v", profile, err)
	}
	if reader.maximum > 32 || reader.calls > 3 {
		t.Fatalf("unbounded reads: maximum=%d calls=%d", reader.maximum, reader.calls)
	}
}

func TestClassifyValidatesLoadCommandBoundsAndFatTableOverlap(t *testing.T) {
	t.Parallel()

	malformed := thin(binary.LittleEndian, cpuARM64, 2)
	binary.LittleEndian.PutUint32(malformed[16:20], 1)
	profile, err := executableprofile.Classify(bytes.NewReader(malformed), int64(len(malformed)))
	if err != nil {
		t.Fatal(err)
	}
	if issue, ok := profile.Issue(); !ok || issue != lifecycle.StaticIssueMalformedHeader {
		t.Fatalf("malformed load commands issue=%q ok=%t", issue, ok)
	}

	truncated := append(thin(binary.LittleEndian, cpuARM64, 2), make([]byte, 7)...)
	binary.LittleEndian.PutUint32(truncated[16:20], 1)
	binary.LittleEndian.PutUint32(truncated[20:24], 8)
	profile, err = executableprofile.Classify(bytes.NewReader(truncated), int64(len(truncated)))
	if err != nil {
		t.Fatal(err)
	}
	if issue, ok := profile.Issue(); !ok || issue != lifecycle.StaticIssueTruncatedHeader {
		t.Fatalf("truncated load commands issue=%q ok=%t", issue, ok)
	}

	invalidCommand := append(thin(binary.LittleEndian, cpuARM64, 2), make([]byte, 8)...)
	binary.LittleEndian.PutUint32(invalidCommand[16:20], 1)
	binary.LittleEndian.PutUint32(invalidCommand[20:24], 8)
	binary.LittleEndian.PutUint32(invalidCommand[36:40], 4)
	profile, err = executableprofile.Classify(bytes.NewReader(invalidCommand), int64(len(invalidCommand)))
	if err != nil {
		t.Fatal(err)
	}
	if issue, ok := profile.Issue(); !ok || issue != lifecycle.StaticIssueMalformedHeader {
		t.Fatalf("invalid command issue=%q ok=%t", issue, ok)
	}

	overlap := fat(cpuARM64, cpuX8664, 2, 2)
	writeFatEntry(overlap[8:28], cpuARM64, 16, 32)
	profile, err = executableprofile.Classify(bytes.NewReader(overlap), int64(len(overlap)))
	if err != nil {
		t.Fatal(err)
	}
	if issue, ok := profile.Issue(); !ok || issue != lifecycle.StaticIssueMalformedHeader {
		t.Fatalf("fat-table overlap issue=%q ok=%t", issue, ok)
	}
}

func TestClassifyReturnsUnexpectedReaderFailures(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("sentinel read failure")
	_, err := executableprofile.Classify(errorReader{err: sentinel}, 32)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error=%v", err)
	}
}

func TestProveBindsProfileAndDigestToOneContentPass(t *testing.T) {
	t.Parallel()

	content := append(thin(binary.LittleEndian, cpuARM64, 2), bytes.Repeat([]byte{'a'}, 64)...)
	reader := bytes.NewReader(content)
	proof, err := (executableprofile.Prover{}).Prove(context.Background(), reader, int64(len(content)), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256(content)
	if !proof.Valid() || proof.Digest.String() != hex.EncodeToString(wantHash[:]) || profileArchitecture(proof.Profile) != lifecycle.ExecutableARM64 {
		t.Fatalf("unexpected proof: %#v", proof)
	}
}

func TestProveHashesBodyMutationFromTheSamePassAsProfileEvidence(t *testing.T) {
	t.Parallel()

	content := append(thin(binary.LittleEndian, cpuARM64, 2), bytes.Repeat([]byte{'a'}, 64)...)
	proof, err := (executableprofile.Prover{BeforeContentPass: func() {
		content[len(content)-1] = 'b'
	}}).Prove(context.Background(), bytes.NewReader(content), int64(len(content)), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256(content)
	if proof.Digest.String() != hex.EncodeToString(wantHash[:]) || profileArchitecture(proof.Profile) != lifecycle.ExecutableARM64 {
		t.Fatalf("proof did not describe the captured content pass: %#v", proof)
	}
}

func TestProveRejectsChangedClassificationEvidence(t *testing.T) {
	t.Parallel()

	content := thin(binary.LittleEndian, cpuARM64, 2)
	_, err := (executableprofile.Prover{BeforeContentPass: func() {
		binary.LittleEndian.PutUint32(content[4:8], cpuX8664)
	}}).Prove(context.Background(), bytes.NewReader(content), int64(len(content)), 1<<20)
	if !errors.Is(err, executableprofile.ErrUnstableEvidence) {
		t.Fatalf("error = %v", err)
	}
}

func TestProveHonorsBoundsAndCancellation(t *testing.T) {
	t.Parallel()

	content := thin(binary.LittleEndian, cpuARM64, 2)
	if _, err := (executableprofile.Prover{}).Prove(context.Background(), bytes.NewReader(content), int64(len(content)), int64(len(content)-1)); err == nil {
		t.Fatal("oversized executable proof accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (executableprofile.Prover{}).Prove(ctx, bytes.NewReader(content), int64(len(content)), 1<<20); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled proof error = %v", err)
	}
}

func TestProveChecksCancellationDuringEvidenceDiscovery(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelingReader{data: thin(binary.LittleEndian, cpuARM64, 2), cancel: cancel, cancelAfter: 1}
	_, err := (executableprofile.Prover{}).Prove(ctx, reader, int64(len(reader.data)), 1<<20)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestProveRequiresExactStableSizeAndFullReads(t *testing.T) {
	t.Parallel()

	growing := &mutableReader{data: append(thin(binary.LittleEndian, cpuARM64, 2), bytes.Repeat([]byte{'a'}, 32)...)}
	originalSize := int64(len(growing.data))
	_, err := (executableprofile.Prover{BeforeContentPass: func() {
		growing.data = append(growing.data, 'b')
	}}).Prove(context.Background(), growing, originalSize, 1<<20)
	if !errors.Is(err, executableprofile.ErrUnstableEvidence) {
		t.Fatalf("growth error = %v", err)
	}

	short := &mutableReader{data: thin(binary.LittleEndian, cpuARM64, 2)}
	_, err = (executableprofile.Prover{}).Prove(context.Background(), short, int64(len(short.data)+1), 1<<20)
	if !errors.Is(err, executableprofile.ErrUnstableEvidence) {
		t.Fatalf("short read error = %v", err)
	}
}

func TestProveContentPassIsGapFreeMonotonicAndBounded(t *testing.T) {
	t.Parallel()

	content := append(thin(binary.LittleEndian, cpuARM64, 2), bytes.Repeat([]byte{'x'}, 70<<10)...)
	phase := 0
	reader := &phaseTraceReader{data: content, phase: &phase}
	_, err := (executableprofile.Prover{BeforeContentPass: func() { phase = 1 }}).Prove(
		context.Background(), reader, int64(len(content)), 1<<20,
	)
	if err != nil {
		t.Fatal(err)
	}
	var contentReads []readRecord
	for _, record := range reader.records {
		if record.phase == 1 {
			contentReads = append(contentReads, record)
		}
	}
	if len(contentReads) != 4 {
		t.Fatalf("content reads = %#v", contentReads)
	}
	offset := int64(0)
	for index, record := range contentReads[:len(contentReads)-1] {
		if record.offset != offset || record.length <= 0 || record.length > 32<<10 {
			t.Fatalf("content read %d = %#v, expected offset %d", index, record, offset)
		}
		offset += int64(record.length)
	}
	probe := contentReads[len(contentReads)-1]
	if offset != int64(len(content)) || probe.offset != int64(len(content)) || probe.length != 1 {
		t.Fatalf("content closure offset=%d probe=%#v size=%d", offset, probe, len(content))
	}
}

func TestProveHandlesUnorderedFatImagesAndSplitEvidence(t *testing.T) {
	t.Parallel()

	content := unorderedFatWithSplitLoadCommand()
	proof, err := (executableprofile.Prover{}).Prove(context.Background(), bytes.NewReader(content), int64(len(content)), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	native, ok := proof.Profile.Native()
	if !ok || native.Architectures() != lifecycle.ExecutableARM64|lifecycle.ExecutableX8664 {
		t.Fatalf("profile = %#v", proof.Profile)
	}
}

func TestProveRejectsFatOffsetAndLoadPlanDrift(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func([]byte)
	}{
		{name: "fat offset", mutate: func(content []byte) {
			binary.BigEndian.PutUint32(content[36:40], uint32(len(content)-16))
		}},
		{name: "load command size", mutate: func(content []byte) {
			binary.LittleEndian.PutUint32(content[32768:32772], 16)
		}},
	}
	for _, test := range tests {
		content := unorderedFatWithSplitLoadCommand()
		_, err := (executableprofile.Prover{BeforeContentPass: func() { test.mutate(content) }}).Prove(
			context.Background(), bytes.NewReader(content), int64(len(content)), 1<<20,
		)
		if !errors.Is(err, executableprofile.ErrUnstableEvidence) {
			t.Fatalf("%s error = %v", test.name, err)
		}
	}
}

func profileArchitecture(profile lifecycle.StaticExecutableProfile) lifecycle.ExecutableArchitectureSet {
	native, _ := profile.Native()
	return native.Architectures()
}

func thin(order binary.ByteOrder, cpu, role uint32) []byte {
	return thinWithSubtype(order, cpu, defaultSubtype(cpu), role)
}

func thinWithSubtype(order binary.ByteOrder, cpu, subtype, role uint32) []byte {
	bytes := make([]byte, 32)
	order.PutUint32(bytes[0:4], machOMagic64)
	order.PutUint32(bytes[4:8], cpu)
	order.PutUint32(bytes[8:12], subtype)
	order.PutUint32(bytes[12:16], role)
	return bytes
}

func fat(firstCPU, secondCPU, firstRole, secondRole uint32) []byte {
	bytes := make([]byte, 128)
	binary.BigEndian.PutUint32(bytes[0:4], fatMagic32)
	binary.BigEndian.PutUint32(bytes[4:8], 2)
	writeFatEntry(bytes[8:28], firstCPU, 64, 32)
	writeFatEntry(bytes[28:48], secondCPU, 96, 32)
	copy(bytes[64:96], thin(binary.LittleEndian, firstCPU, firstRole))
	copy(bytes[96:128], thin(binary.LittleEndian, secondCPU, secondRole))
	return bytes
}

func fat64(firstCPU, secondCPU, firstRole, secondRole uint32) []byte {
	bytes := make([]byte, 144)
	binary.BigEndian.PutUint32(bytes[0:4], fatMagic64)
	binary.BigEndian.PutUint32(bytes[4:8], 2)
	writeFat64Entry(bytes[8:40], firstCPU, 80, 32)
	writeFat64Entry(bytes[40:72], secondCPU, 112, 32)
	copy(bytes[80:112], thin(binary.LittleEndian, firstCPU, firstRole))
	copy(bytes[112:144], thin(binary.LittleEndian, secondCPU, secondRole))
	return bytes
}

func writeFatEntry(bytes []byte, cpu, offset, size uint32) {
	writeFatEntryWithSubtype(bytes, cpu, defaultSubtype(cpu), offset, size, 0)
}

func writeFatEntryWithSubtype(bytes []byte, cpu, subtype, offset, size, alignment uint32) {
	binary.BigEndian.PutUint32(bytes[0:4], cpu)
	binary.BigEndian.PutUint32(bytes[4:8], subtype)
	binary.BigEndian.PutUint32(bytes[8:12], offset)
	binary.BigEndian.PutUint32(bytes[12:16], size)
	binary.BigEndian.PutUint32(bytes[16:20], alignment)
}

func writeFat64Entry(bytes []byte, cpu uint32, offset, size uint64) {
	binary.BigEndian.PutUint32(bytes[0:4], cpu)
	binary.BigEndian.PutUint32(bytes[4:8], defaultSubtype(cpu))
	binary.BigEndian.PutUint64(bytes[8:16], offset)
	binary.BigEndian.PutUint64(bytes[16:24], size)
}

func defaultSubtype(cpu uint32) uint32 {
	if cpu == cpuX8664 {
		return 3
	}
	return 0
}

func unorderedFatWithSplitLoadCommand() []byte {
	const (
		armOffset = 32732
		armSize   = 40
		x86Offset = 32800
		x86Size   = 32
	)
	content := make([]byte, x86Offset+x86Size)
	binary.BigEndian.PutUint32(content[0:4], fatMagic32)
	binary.BigEndian.PutUint32(content[4:8], 2)
	writeFatEntry(content[8:28], cpuX8664, x86Offset, x86Size)
	writeFatEntry(content[28:48], cpuARM64, armOffset, armSize)
	copy(content[armOffset:armOffset+32], thin(binary.LittleEndian, cpuARM64, 2))
	binary.LittleEndian.PutUint32(content[armOffset+16:armOffset+20], 1)
	binary.LittleEndian.PutUint32(content[armOffset+20:armOffset+24], 8)
	binary.LittleEndian.PutUint32(content[armOffset+32:armOffset+36], 1)
	binary.LittleEndian.PutUint32(content[armOffset+36:armOffset+40], 8)
	copy(content[x86Offset:x86Offset+x86Size], thin(binary.LittleEndian, cpuX8664, 2))
	return content
}

type readSpy struct {
	content []byte
	maximum int
	calls   int
}

func (r *readSpy) ReadAt(bytes []byte, offset int64) (int, error) {
	r.calls++
	if len(bytes) > r.maximum {
		r.maximum = len(bytes)
	}
	if offset >= int64(len(r.content)) {
		return 0, io.EOF
	}
	read := copy(bytes, r.content[offset:])
	if read < len(bytes) {
		return read, io.EOF
	}
	return read, nil
}

type errorReader struct{ err error }

func (r errorReader) ReadAt([]byte, int64) (int, error) { return 0, r.err }

type mutableReader struct{ data []byte }

func (r *mutableReader) ReadAt(buffer []byte, offset int64) (int, error) {
	if offset >= int64(len(r.data)) {
		return 0, io.EOF
	}
	read := copy(buffer, r.data[offset:])
	if read != len(buffer) {
		return read, io.EOF
	}
	return read, nil
}

type cancelingReader struct {
	data        []byte
	cancel      context.CancelFunc
	cancelAfter int
	calls       int
}

func (r *cancelingReader) ReadAt(buffer []byte, offset int64) (int, error) {
	r.calls++
	read, err := (&mutableReader{data: r.data}).ReadAt(buffer, offset)
	if r.calls == r.cancelAfter {
		r.cancel()
	}
	return read, err
}

type readRecord struct {
	phase  int
	offset int64
	length int
}

type phaseTraceReader struct {
	data    []byte
	phase   *int
	records []readRecord
}

func (r *phaseTraceReader) ReadAt(buffer []byte, offset int64) (int, error) {
	r.records = append(r.records, readRecord{phase: *r.phase, offset: offset, length: len(buffer)})
	return (&mutableReader{data: r.data}).ReadAt(buffer, offset)
}
