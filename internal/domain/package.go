package domain

import "time"

type Kind string

const (
	KindFormula Kind = "formula"
	KindCask    Kind = "cask"
	KindFont    Kind = "font"
)

func (k Kind) Valid() bool { return k == KindFormula || k == KindCask || k == KindFont }

// Package is one current, non-disabled Homebrew catalog entry.
type Package struct {
	Name          string
	Kind          Kind
	Description   string
	Homepage      string
	AddedAt       *time.Time
	InstallTarget string
	FormerNames   []string
	UpdatedAt     time.Time
}

func (p Package) Key() string { return string(p.Kind) + "\x00" + p.Name }

func (p Package) InstallArgs() []string {
	if p.Kind == KindFormula {
		return []string{"install", p.InstallTarget}
	}
	return []string{"install", "--cask", p.InstallTarget}
}
