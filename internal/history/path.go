package history

import (
	"path"
	"strings"

	"github.com/milo/brewnicle/internal/catalog"
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
	root := "Formula"
	if repo == RepoCask {
		root = "Casks"
	}
	if len(parts) != 2 && len(parts) != 3 || parts[0] != root {
		return "", false
	}
	file := parts[len(parts)-1]
	if !strings.HasSuffix(file, ".rb") {
		return "", false
	}
	name := strings.TrimSuffix(file, ".rb")
	if len(parts) == 3 {
		bucket := parts[1]
		if len(bucket) != 1 || !((bucket[0] >= 'a' && bucket[0] <= 'z') || (bucket[0] >= '0' && bucket[0] <= '9')) {
			return "", false
		}
	}
	if !catalog.ValidIdentifier(name) {
		return "", false
	}
	return name, true
}
