package history

import "testing"

func TestIdentifierFromPath(t *testing.T) {
	for _, p := range []string{"Formula/a.rb", "Formula/a/a.rb", "Formula/0/a.rb"} {
		if id, ok := IdentifierFromPath(RepoCore, p); !ok || id != "a" {
			t.Errorf("accept %q", p)
		}
	}
	for _, p := range []string{"Formula/A/a.rb", "Formula/é/a.rb", "Formula//a.rb", "Formula/ab/a.rb", "Formula/a/b/a.rb", "Formula/.rb", "Casks/a.rb", "Other/a.rb", "/Formula/a.rb"} {
		if _, ok := IdentifierFromPath(RepoCore, p); ok {
			t.Errorf("accepted %q", p)
		}
	}
	if id, ok := IdentifierFromPath(RepoCask, "Casks/f/font-maple.rb"); !ok || id != "font-maple" {
		t.Fatal(id, ok)
	}
}
