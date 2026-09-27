package catalog

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/lmilojevicc/brewnicle/internal/domain"
)

var identifierRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+_.@-]*$`)

func ValidIdentifier(s string) bool { return identifierRE.MatchString(s) }

func normalizeHomepage(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return u.String()
}

func former(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, n := range in {
		if ValidIdentifier(n) && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

func normalizeFormulae(in []formulaJSON) ([]domain.Package, int) {
	out := make([]domain.Package, 0, len(in))
	skipped := 0
	for _, f := range in {
		if f.Disabled {
			continue
		}
		if !ValidIdentifier(f.Name) {
			skipped++
			continue
		}
		out = append(out, domain.Package{Name: f.Name, InstallTarget: f.Name, Kind: domain.KindFormula, Description: f.Description, Homepage: normalizeHomepage(f.Homepage), FormerNames: former(f.OldNames), SourcePath: f.RubySourcePath})
	}
	return out, skipped
}

func normalizeCasks(in []caskJSON) ([]domain.Package, int) {
	out := make([]domain.Package, 0, len(in))
	skipped := 0
	for _, c := range in {
		if c.Disabled {
			continue
		}
		if !ValidIdentifier(c.Token) {
			skipped++
			continue
		}
		kind := domain.KindCask
		if strings.HasPrefix(c.Token, "font-") {
			kind = domain.KindFont
		}
		out = append(out, domain.Package{Name: c.Token, InstallTarget: c.Token, Kind: kind, Description: c.Description, Homepage: normalizeHomepage(c.Homepage), FormerNames: former(c.OldTokens), SourcePath: c.RubySourcePath})
	}
	return out, skipped
}
