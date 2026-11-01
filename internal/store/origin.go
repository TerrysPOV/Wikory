package store

import (
	"net/url"
	"strings"

	"github.com/TerrysPOV/Wikory/internal/ids"
)

// CanonicalOrigin normalises a remote URL to lower-case host/owner/repo with scheme,
// credentials, port and a .git suffix removed, so SSH and HTTPS forms are equal (spec 4.3).
// Local-path remotes have no canonical origin.
func CanonicalOrigin(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}
	var host, p string
	switch {
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		if err != nil || u.Scheme == "file" {
			return "", false
		}
		host, p = u.Hostname(), u.Path
	case strings.Contains(s, "@") && strings.Contains(s[strings.Index(s, "@"):], ":"):
		rest := s[strings.Index(s, "@")+1:]
		c := strings.Index(rest, ":")
		host, p = rest[:c], rest[c+1:]
	case strings.Index(s, ":") > 1 && !strings.Contains(s[:strings.Index(s, ":")], "/"):
		c := strings.Index(s, ":")
		host, p = s[:c], s[c+1:]
	default:
		return "", false
	}
	if len(host) < 2 {
		return "", false // empty, or a Windows drive letter
	}
	p = strings.TrimSuffix(strings.Trim(p, "/"), ".git")
	segs := strings.Split(p, "/")
	if len(segs) < 2 {
		return "", false
	}
	for _, sg := range segs {
		if sg == "" || sg == "." || sg == ".." {
			return "", false
		}
	}
	return strings.ToLower(host + "/" + strings.Join(segs, "/")), true
}

// StoreKey is project-id plus canonical origin, or project-id plus the git common
// directory when there is no remote (spec 4.3).
func StoreKey(projectID, origin, commonDir string) string {
	if origin != "" {
		return ids.Digest("origin", projectID, origin)
	}
	return ids.Digest("path", projectID, commonDir)
}
