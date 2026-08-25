package executableprofile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/alx4j/ai4j/internal/domain"
	"github.com/alx4j/ai4j/internal/lifecycle"
)

const (
	proofReadBytes       = 32 << 10
	maximumEvidenceBytes = 1 << 20
	maximumEvidenceReads = maximumArchitectures*(maximumLoadCommands+4) + 32
)

// ErrUnstableEvidence reports that captured executable bytes no longer support
// the bounded classification plan or exact size used for the content pass. No
// profile/digest pair is returned in that case.
var ErrUnstableEvidence = errors.New("executable classification evidence changed")

// Proof binds the static profile and SHA-256 digest derived from one sequential
// read of an already-opened executable descriptor.
type Proof struct {
	Profile lifecycle.StaticExecutableProfile
	Digest  domain.ExecutableDigest
}

// Valid reports whether both members are independently valid. Issue profiles
// remain valid observation evidence even though they are not launchable.
func (p Proof) Valid() bool { return p.Profile.Valid() && p.Digest.Valid() }

// Prover exposes a synchronization hook only for deterministic host race
// tests. Production callers use the zero value.
type Prover struct {
	BeforeContentPass func()
}

// Prove discovers only the bounded ranges needed by Classify, then performs
// one complete sequential content pass. The pass hashes every byte and retains
// exactly those evidence ranges. Classification is repeated over the retained
// bytes so the returned profile and digest always describe the same read.
func (p Prover) Prove(ctx context.Context, reader io.ReaderAt, size, maximumBytes int64) (Proof, error) {
	if ctx == nil || reader == nil || size < 0 || maximumBytes <= 0 || size > maximumBytes {
		return Proof{}, fmt.Errorf("invalid executable proof input")
	}
	if err := ctx.Err(); err != nil {
		return Proof{}, err
	}

	recorder := &evidenceRecorder{ctx: ctx, reader: reader}
	discovered, err := Classify(recorder, size)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return Proof{}, ErrUnstableEvidence
		}
		return Proof{}, err
	}
	ranges, err := mergeEvidenceRanges(recorder.ranges)
	if err != nil {
		return Proof{}, err
	}
	if p.BeforeContentPass != nil {
		p.BeforeContentPass()
	}

	captured, digest, err := captureAndHash(ctx, reader, size, ranges)
	if err != nil {
		return Proof{}, err
	}
	profile, err := Classify(sparseEvidenceReader{ranges: captured}, size)
	if err != nil {
		if errors.Is(err, ErrUnstableEvidence) {
			return Proof{}, ErrUnstableEvidence
		}
		return Proof{}, err
	}
	if profile != discovered {
		return Proof{}, ErrUnstableEvidence
	}
	proof := Proof{Profile: profile, Digest: digest}
	if !proof.Valid() {
		return Proof{}, fmt.Errorf("invalid executable proof")
	}
	return proof, nil
}

type evidenceRange struct {
	start int64
	end   int64
	data  []byte
}

type evidenceRecorder struct {
	ctx    context.Context
	reader io.ReaderAt
	ranges []evidenceRange
	bytes  int64
}

func (r *evidenceRecorder) ReadAt(buffer []byte, offset int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(buffer) == 0 {
		return 0, nil
	}
	if offset < 0 || int64(len(buffer)) > maximumEvidenceBytes-r.bytes || len(r.ranges) >= maximumEvidenceReads {
		return 0, fmt.Errorf("record executable evidence: %w", ErrUnstableEvidence)
	}
	end := offset + int64(len(buffer))
	if end < offset {
		return 0, fmt.Errorf("record executable evidence: %w", ErrUnstableEvidence)
	}
	r.ranges = append(r.ranges, evidenceRange{start: offset, end: end})
	r.bytes += int64(len(buffer))
	return r.reader.ReadAt(buffer, offset)
}

func mergeEvidenceRanges(ranges []evidenceRange) ([]evidenceRange, error) {
	if len(ranges) == 0 {
		return nil, nil
	}
	ordered := append([]evidenceRange(nil), ranges...)
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].start == ordered[right].start {
			return ordered[left].end < ordered[right].end
		}
		return ordered[left].start < ordered[right].start
	})
	merged := make([]evidenceRange, 0, len(ordered))
	for _, current := range ordered {
		if current.start < 0 || current.end < current.start {
			return nil, ErrUnstableEvidence
		}
		if len(merged) == 0 || current.start > merged[len(merged)-1].end {
			merged = append(merged, evidenceRange{start: current.start, end: current.end})
			continue
		}
		if current.end > merged[len(merged)-1].end {
			merged[len(merged)-1].end = current.end
		}
	}
	var total int64
	for index := range merged {
		length := merged[index].end - merged[index].start
		if length > maximumEvidenceBytes-total {
			return nil, fmt.Errorf("merge executable evidence: %w", ErrUnstableEvidence)
		}
		total += length
		merged[index].data = make([]byte, length)
	}
	return merged, nil
}

func captureAndHash(ctx context.Context, reader io.ReaderAt, size int64, ranges []evidenceRange) ([]evidenceRange, domain.ExecutableDigest, error) {
	hash := sha256.New()
	buffer := make([]byte, proofReadBytes)
	rangeIndex := 0
	for offset := int64(0); offset < size; {
		if err := ctx.Err(); err != nil {
			return nil, domain.ExecutableDigest{}, err
		}
		count := int64(len(buffer))
		if remaining := size - offset; remaining < count {
			count = remaining
		}
		read, err := reader.ReadAt(buffer[:int(count)], offset)
		if read != int(count) {
			if err == nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil, domain.ExecutableDigest{}, ErrUnstableEvidence
			}
			return nil, domain.ExecutableDigest{}, fmt.Errorf("read executable content: %w", err)
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, domain.ExecutableDigest{}, fmt.Errorf("read executable content: %w", err)
		}
		chunk := buffer[:read]
		if _, err := hash.Write(chunk); err != nil {
			return nil, domain.ExecutableDigest{}, err
		}
		chunkEnd := offset + int64(read)
		for rangeIndex < len(ranges) && ranges[rangeIndex].start < chunkEnd {
			current := &ranges[rangeIndex]
			overlapStart := current.start
			if overlapStart < offset {
				overlapStart = offset
			}
			overlapEnd := current.end
			if overlapEnd > chunkEnd {
				overlapEnd = chunkEnd
			}
			if overlapStart < overlapEnd {
				copy(current.data[overlapStart-current.start:overlapEnd-current.start], chunk[overlapStart-offset:overlapEnd-offset])
			}
			if current.end > chunkEnd {
				break
			}
			rangeIndex++
		}
		offset = chunkEnd
	}
	if rangeIndex != len(ranges) {
		return nil, domain.ExecutableDigest{}, ErrUnstableEvidence
	}
	if err := ctx.Err(); err != nil {
		return nil, domain.ExecutableDigest{}, err
	}
	var growthProbe [1]byte
	read, probeErr := reader.ReadAt(growthProbe[:], size)
	if read != 0 {
		return nil, domain.ExecutableDigest{}, ErrUnstableEvidence
	}
	if !errors.Is(probeErr, io.EOF) {
		if probeErr == nil {
			probeErr = io.ErrUnexpectedEOF
		}
		return nil, domain.ExecutableDigest{}, fmt.Errorf("close executable content size: %w", probeErr)
	}
	digest, err := domain.NewExecutableDigest(hex.EncodeToString(hash.Sum(nil)))
	if err != nil {
		return nil, domain.ExecutableDigest{}, err
	}
	return ranges, digest, nil
}

type sparseEvidenceReader struct{ ranges []evidenceRange }

func (r sparseEvidenceReader) ReadAt(buffer []byte, offset int64) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if offset < 0 {
		return 0, ErrUnstableEvidence
	}
	end := offset + int64(len(buffer))
	if end < offset {
		return 0, ErrUnstableEvidence
	}
	index := sort.Search(len(r.ranges), func(index int) bool { return r.ranges[index].end > offset })
	if index == len(r.ranges) || r.ranges[index].start > offset || r.ranges[index].end < end {
		return 0, ErrUnstableEvidence
	}
	copy(buffer, r.ranges[index].data[offset-r.ranges[index].start:end-r.ranges[index].start])
	return len(buffer), nil
}
