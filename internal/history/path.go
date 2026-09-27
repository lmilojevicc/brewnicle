package history

import (
	"path"
	"strings"

	"github.com/lmilojevicc/brewnicle/internal/catalog"
)

type RepoKind string

const (
	RepoCore RepoKind = "core"
	RepoCask RepoKind = "cask"
)

func IdentifierFromPath(repo RepoKind, p string) (string, bool) {
	if p == "" || strings.HasPrefix(p, "/") || path.Clean(p) != p {
		return "", false
	}
	parts := strings.Split(p, "/")
	if !validLayout(repo, parts) {
		return "", false
	}
	file := parts[len(parts)-1]
	if !strings.HasSuffix(file, ".rb") {
		return "", false
	}
	name := strings.TrimSuffix(file, ".rb")
	if !catalog.ValidIdentifier(name) {
		return "", false
	}
	if repo == RepoCask && len(parts) == 4 && !strings.HasPrefix(name, "font-") {
		return "", false
	}
	return name, true
}

func validLayout(repo RepoKind, parts []string) bool {
	switch repo {
	case RepoCore:
		if len(parts) == 2 {
			return parts[0] == "Formula"
		}
		return len(parts) == 3 && parts[0] == "Formula" && (singleBucket(parts[1]) || parts[1] == "lib")
	case RepoCask:
		if len(parts) == 2 {
			return parts[0] == "Casks"
		}
		if len(parts) == 3 {
			return parts[0] == "Casks" && singleBucket(parts[1])
		}
		return len(parts) == 4 && parts[0] == "Casks" && parts[1] == "font" && strings.HasPrefix(parts[2], "font-") && singleBucket(strings.TrimPrefix(parts[2], "font-"))
	default:
		return false
	}
}

func singleBucket(bucket string) bool {
	return len(bucket) == 1 && ((bucket[0] >= 'a' && bucket[0] <= 'z') || (bucket[0] >= '0' && bucket[0] <= '9'))
}
