package protocol

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const (
	testOID      = "0123456789abcdef0123456789abcdef01234567"
	testOtherOID = "89abcdef0123456789abcdef0123456789abcdef"
)

func TestParseRemoteAcceptsExactBoundedGrammar(t *testing.T) {
	t.Parallel()

	data := []byte("ref: refs/heads/main\tHEAD\n" + testOID + "\tHEAD\n" +
		testOID + "\trefs/heads/main\n" + testOtherOID + "\trefs/tags/v1\n" +
		testOID + "\trefs/tags/v1^{}\n")
	records, err := ParseRemote(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 5 || records[0].SymrefTarget != "refs/heads/main" || records[0].Reference != "HEAD" ||
		records[1].OID != testOID || records[4].Reference != "refs/tags/v1^{}" {
		t.Fatalf("records = %#v", records)
	}
}

func TestParseRemoteRejectsMalformedAndUnboundedInput(t *testing.T) {
	t.Parallel()

	for _, data := range [][]byte{
		[]byte(testOID + "\trefs/heads/main"),
		[]byte(testOID + "\trefs/heads/main\r\n"),
		[]byte(strings.ToUpper(testOID) + "\trefs/heads/main\n"),
		[]byte(testOID + "\trefs/heads/main\x00\n"),
		[]byte(testOID + "\trefs/heads/main\textra\n"),
		[]byte("ref: refs/heads/main extra\tHEAD\n"),
		[]byte(testOID + "\t" + strings.Repeat("r", MaximumRemoteReferenceBytes+1) + "\n"),
		bytes.Repeat([]byte{'\n'}, maximumRemoteRecords+1),
		bytes.Repeat([]byte{'x'}, MaximumRemoteOutputBytes+1),
	} {
		if _, err := ParseRemote(data); !errors.Is(err, ErrMalformed) {
			t.Errorf("ParseRemote accepted malformed input of length %d", len(data))
		}
	}
}

func TestParseRemoteRecordCountBoundary(t *testing.T) {
	line := []byte(testOID + "\trefs/heads/main\n")
	data := bytes.Repeat(line, maximumRemoteRecords)
	if len(data) > MaximumRemoteOutputBytes {
		t.Fatal("test record does not fit the documented output cap")
	}
	records, err := ParseRemote(data)
	if err != nil || len(records) != maximumRemoteRecords {
		t.Fatalf("ParseRemote boundary = %d, %v", len(records), err)
	}
}

func TestParseTreeAcceptsExactGrammar(t *testing.T) {
	t.Parallel()

	records, err := ParseTree([]byte(fmt.Sprintf("100644 blob %s %7d\tempty.txt\x00", testOID, 0) +
		fmt.Sprintf("100644 blob %s %7d\tone.txt\x00", testOtherOID, 1) +
		fmt.Sprintf("100644 blob %s %7d\tmedium.bin\x00", testOID, 3549) +
		fmt.Sprintf("100755 blob %s %7d\tbin/tool\x00", testOtherOID, 67108864)))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 || records[0].Size != 0 || records[1].Size != 1 || records[2].Size != 3549 ||
		records[3].Mode != "100755" || records[3].Path != "bin/tool" || records[3].Size != 67108864 ||
		!records[0].SizeKnown {
		t.Fatalf("records = %#v", records)
	}
	gitlink, err := ParseTree([]byte(fmt.Sprintf("160000 commit %s %7s\tmodule\x00", testOID, "-")))
	if err != nil || len(gitlink) != 1 || gitlink[0].SizeKnown || gitlink[0].Type != "commit" {
		t.Fatalf("gitlink = %#v, %v", gitlink, err)
	}
}

func TestParseTreeRejectsWhitespaceDelimiterAndRecordAttacks(t *testing.T) {
	t.Parallel()

	valid := fmt.Sprintf("100644 blob %s %7d\tfile\x00", testOID, 1)
	for _, data := range [][]byte{
		[]byte(strings.TrimSuffix(valid, "\x00")),
		[]byte(fmt.Sprintf("100644  blob %s %7d\tfile\x00", testOID, 1)),
		[]byte(fmt.Sprintf("100644\vblob %s %7d\tfile\x00", testOID, 1)),
		[]byte("100644 blob " + testOID + "      01\tfile\x00"),
		[]byte(fmt.Sprintf("100644 blob %s %7d\tfile\x00", strings.ToUpper(testOID), 1)),
		[]byte(fmt.Sprintf("100644 blob %s %7d\tfile\textra\x00", testOID, 1)),
		[]byte("100644 blob " + testOID + " 1\tfile\x00"),
		[]byte(valid + "\x00"),
		bytes.Repeat([]byte{'x'}, MaximumTreeOutputBytes+1),
	} {
		if _, err := ParseTree(data); !errors.Is(err, ErrMalformed) {
			t.Errorf("ParseTree accepted malformed input of length %d", len(data))
		}
	}
}

func TestParseIndexAcceptsStageAndRejectsLooseSpacing(t *testing.T) {
	t.Parallel()

	records, err := ParseIndex([]byte("100644 " + testOID + " 0\tfile.txt\x00"))
	if err != nil || len(records) != 1 || records[0].Stage != 0 || records[0].Path != "file.txt" {
		t.Fatalf("ParseIndex = %#v, %v", records, err)
	}
	for _, data := range [][]byte{
		[]byte("100644  " + testOID + " 0\tfile.txt\x00"),
		[]byte("100644 " + testOID + " 4\tfile.txt\x00"),
		[]byte("100644 " + testOID + " 0 file.txt\x00"),
		bytes.Repeat([]byte{'x'}, MaximumIndexOutputBytes+1),
	} {
		if _, err := ParseIndex(data); !errors.Is(err, ErrMalformed) {
			t.Errorf("ParseIndex accepted malformed input of length %d", len(data))
		}
	}
}

func TestParseAttributesUsesPerBatchBound(t *testing.T) {
	t.Parallel()

	records, err := ParseAttributes([]byte("a.txt\x00filter\x00unspecified\x00"))
	if err != nil || len(records) != 1 || records[0].Value != "unspecified" {
		t.Fatalf("ParseAttributes = %#v, %v", records, err)
	}
	for _, data := range [][]byte{
		[]byte("a.txt\x00filter\x00"),
		[]byte("a.txt\x00filter name\x00unspecified\x00"),
		bytes.Repeat([]byte{'x'}, MaximumAttributeOutputBytes+1),
		bytes.Repeat([]byte("p\x00a\x00v\x00"), maximumAttributeRows+1),
	} {
		if _, err := ParseAttributes(data); !errors.Is(err, ErrMalformed) {
			t.Errorf("ParseAttributes accepted malformed input of length %d", len(data))
		}
	}
}

func TestParseCleanStatus(t *testing.T) {
	t.Parallel()

	if err := ParseCleanStatus(nil); err != nil {
		t.Fatal(err)
	}
	if err := ParseCleanStatus([]byte("? untracked\x00")); !errors.Is(err, ErrMalformed) {
		t.Fatalf("ParseCleanStatus dirty error = %v", err)
	}
}

func TestParseSingleLineIsCanonical(t *testing.T) {
	t.Parallel()

	for _, data := range [][]byte{[]byte("commit"), []byte("commit\n")} {
		value, err := ParseSingleLine(data)
		if err != nil || value != "commit" {
			t.Fatalf("ParseSingleLine = %q, %v", value, err)
		}
	}
	for _, data := range [][]byte{
		nil, []byte("\n"), []byte(" commit\n"), []byte("commit \n"), []byte("commit\r\n"),
		[]byte("commit\nextra\n"), bytes.Repeat([]byte{'x'}, MaximumScalarOutputBytes+1),
	} {
		if _, err := ParseSingleLine(data); !errors.Is(err, ErrMalformed) {
			t.Errorf("ParseSingleLine accepted malformed input of length %d", len(data))
		}
	}
}

func TestParserErrorsNeverRetainInput(t *testing.T) {
	t.Parallel()

	canary := "protocol-secret-canary"
	_, err := ParseTree([]byte(canary))
	if err == nil || strings.Contains(err.Error(), canary) {
		t.Fatalf("error = %v", err)
	}
}

func TestDelimiterFloodsAreRejectedBeforeSplitAllocation(t *testing.T) {
	remoteFlood := bytes.Repeat([]byte{'\n'}, maximumRemoteRecords+1)
	nullFlood := bytes.Repeat([]byte{0}, maximumTreeRecords+1)
	remoteAllocations := testing.AllocsPerRun(20, func() {
		if _, err := ParseRemote(remoteFlood); !errors.Is(err, ErrMalformed) {
			panic("remote delimiter flood accepted")
		}
	})
	nullAllocations := testing.AllocsPerRun(20, func() {
		if _, err := ParseTree(nullFlood); !errors.Is(err, ErrMalformed) {
			panic("NUL delimiter flood accepted")
		}
	})
	if remoteAllocations > 1 || nullAllocations > 1 {
		t.Fatalf("delimiter flood allocations = remote %.1f, NUL %.1f", remoteAllocations, nullAllocations)
	}
}

func FuzzBoundedProtocolParsers(f *testing.F) {
	for _, seed := range [][]byte{
		nil,
		[]byte(testOID + "\trefs/heads/main\n"),
		[]byte(fmt.Sprintf("100644 blob %s %7d\tfile\x00", testOID, 1)),
		[]byte("100644 " + testOID + " 0\tfile\x00"),
		[]byte("file\x00filter\x00unspecified\x00"),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			t.Skip()
		}
		_, _ = ParseRemote(data)
		_, _ = ParseTree(data)
		_, _ = ParseIndex(data)
		_, _ = ParseAttributes(data)
		_, _ = ParseSingleLine(data)
		_ = ParseCleanStatus(data)
	})
}
