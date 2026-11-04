package bootstrap

import (
	"context"
	"fmt"
	"strings"

	"github.com/TerrysPOV/Wikory/internal/ids"
)

// PrepStats counts what PrepareWork wrote.
type PrepStats struct{ Files, Redactions int }

func splitSrcRef(ref string) (name, path string, ok bool) {
	rest, found := strings.CutPrefix(ref, "src:")
	if !found {
		return "", "", false
	}
	return strings.Cut(rest, ":")
}

// PrepareWork writes the redacted work file for every extract item of a plan. It reads each
// file again and refuses one whose content no longer matches the hash the inventory scanned
// (the secret scan covered that content and no other).
func PrepareWork(ctx context.Context, readers []Reader, res Result, w WorkFiles, progress func(done, total int)) (PrepStats, error) {
	byName := map[string]Reader{}
	for _, r := range readers {
		byName[r.Name()] = r
	}
	srcOf := map[string]Source{}
	for _, s := range res.Sources {
		srcOf[s.Name+"\x00"+s.Path] = s
	}
	read := func(s Source) ([]byte, error) {
		rd := byName[s.Name]
		if rd == nil {
			return nil, fmt.Errorf("bootstrap: no reader for source %q", s.Name)
		}
		data, err := rd.Read(ctx, Entry{Source: s.Name, Path: s.Path, OID: s.OID, Size: s.Size}, MaxFileBytes)
		if err != nil {
			return nil, err
		}
		if ids.SHA256Hex(data) != s.Hash {
			return nil, fmt.Errorf("bootstrap: %s changed after it was scanned; run wikory bootstrap plan again", s.Path)
		}
		return data, nil
	}
	files := ComponentFiles(res)
	specs := ExtractSpecs(res)
	var st PrepStats
	for i, sp := range specs {
		if err := ctx.Err(); err != nil {
			return st, err
		}
		if progress != nil {
			progress(i, len(specs))
		}
		var text string
		if name, p, ok := splitSrcRef(sp.Ref); ok {
			s, found := srcOf[name+"\x00"+p]
			if !found {
				return st, fmt.Errorf("bootstrap: item %s names an unknown source", sp.Ref)
			}
			data, err := read(s)
			if err != nil {
				return st, err
			}
			text = string(data)
		} else {
			prefix := strings.TrimPrefix(sp.Ref, "comp:")
			var df []DigestFile
			for _, s := range files[prefix] {
				data, err := read(s)
				if err != nil {
					return st, err
				}
				df = append(df, DigestFile{Path: s.Path, Hash: s.Hash, Text: string(data)})
			}
			text, _ = BuildDigest(df, DigestMaxBytes)
		}
		n, err := w.Write(sp.InputHash, text)
		if err != nil {
			return st, err
		}
		st.Files++
		st.Redactions += n
	}
	if progress != nil {
		progress(len(specs), len(specs))
	}
	return st, nil
}
