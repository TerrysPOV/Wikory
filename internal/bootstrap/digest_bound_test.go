package bootstrap

import (
	"fmt"
	"strings"
	"testing"
)

func manyFiles(n, size int) []DigestFile {
	files := make([]DigestFile, n)
	for i := range files {
		files[i] = DigestFile{Path: fmt.Sprintf("internal/big/sub%03d/file_%05d_with_a_longish_name.go", i%100, i),
			Hash: fmt.Sprintf("%064x", i), Text: strings.Repeat("y", size)}
	}
	return files
}

// A component with thousands of files must still give a digest within DigestMaxBytes, header
// lines and the omitted list included, and so within the limit a work file is read back with: a
// larger file is refused as an infrastructure error and the paid item retried and then lost.
func TestADigestOfThousandsOfFilesStaysWithinItsBoundAndReadsBack(t *testing.T) {
	for name, files := range map[string][]DigestFile{"5000 small": manyFiles(5000, 40), "3000 tiny": manyFiles(3000, 1)} {
		text, omitted := BuildDigest(files, DigestMaxBytes)
		if len(text) > DigestMaxBytes {
			t.Fatalf("%s: the digest is %d bytes, over its bound of %d", name, len(text), DigestMaxBytes)
		}
		if len(omitted) == 0 || !strings.Contains(text, fmt.Sprintf("=== and %d more ===", len(omitted)-listedPaths(text))) {
			t.Fatalf("%s: a long omitted list ends with how many more were left out (%d omitted)", name, len(omitted))
		}
		w, _ := newWork(t)
		if _, _, err := w.WriteDigest("abcdef0123456789", files, DigestMaxBytes); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Read("abcdef0123456789", WorkReadMax); err != nil {
			t.Fatalf("%s: a digest Wikory wrote must read back within WorkReadMax: %v", name, err)
		}
	}
	if DigestMaxBytes > WorkReadMax {
		t.Fatalf("WorkReadMax %d is below the digest bound %d", WorkReadMax, DigestMaxBytes)
	}
}

// listedPaths counts the paths named after the omitted mark, the "and K more" line excluded.
func listedPaths(text string) int {
	_, list, ok := strings.Cut(text, omittedMark+"\n")
	if !ok {
		return 0
	}
	n := 0
	for _, l := range strings.Split(strings.TrimSuffix(list, "\n"), "\n") {
		if l != "" && !strings.HasPrefix(l, "=== and ") {
			n++
		}
	}
	return n
}

// The bound holds for any budget of at least omittedReserve, whatever mix of fitting, oversized
// and unlistable (control character) files comes in.
func TestBuildDigestNeverExceedsItsBudget(t *testing.T) {
	files := append(manyFiles(40, 30), DigestFile{Path: "bad\nname.go", Hash: "h", Text: "x"}, DigestFile{Path: strings.Repeat("p", 3000), Hash: "h", Text: "x"})
	for max := omittedReserve; max < 6000; max += 37 {
		if text, _ := BuildDigest(files, max); len(text) > max {
			t.Fatalf("budget %d: digest is %d bytes", max, len(text))
		}
	}
}

// The estimator sizes the digest BuildDigest writes, header lines included.
func TestDigestEstimateCountsHeaderLines(t *testing.T) {
	var r Result
	for i := 0; i < 200; i++ {
		r.Sources = append(r.Sources, Source{Name: "repo", Path: fmt.Sprintf("internal/c/f%03d.go", i), Tier: TierImplementation, Disposition: DispQueued, Hash: fmt.Sprintf("%064x", i), Size: 2})
	}
	r.Components = []Component{{Name: "internal/c", Prefix: "internal/c", Rank: 1, Inspected: true, Files: 200}}
	specs := ExtractSpecs(r)
	want := int64(200 * (2 + headerBytes("internal/c/f000.go")))
	if got := specs[len(specs)-1].Bytes; got != want {
		t.Fatalf("digest estimate %d bytes, want %d (file bytes plus a header line each)", got, want)
	}
}
