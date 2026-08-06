package history

import "testing"

func TestIdentifierFromPath(t *testing.T) {
	accepted := []struct {
		repo RepoKind
		path string
		want string
	}{
		{RepoCore, "Formula/a.rb", "a"},
		{RepoCore, "Formula/a/a.rb", "a"},
		{RepoCore, "Formula/0/a.rb", "a"},
		{RepoCore, "Formula/lib/libjaylink.rb", "libjaylink"},
		{RepoCask, "Casks/app.rb", "app"},
		{RepoCask, "Casks/a/app.rb", "app"},
		{RepoCask, "Casks/font/font-n/font-nexon-lv2-gothic.rb", "font-nexon-lv2-gothic"},
		{RepoCask, "Casks/font/font-0/font-0xproto.rb", "font-0xproto"},
	}
	for _, tc := range accepted {
		if id, ok := IdentifierFromPath(tc.repo, tc.path); !ok || id != tc.want {
			t.Errorf("IdentifierFromPath(%q) = %q, %v; want %q, true", tc.path, id, ok, tc.want)
		}
	}

	rejected := []struct {
		repo RepoKind
		path string
	}{
		{RepoCore, "Formula/A/a.rb"},
		{RepoCore, "Formula/é/a.rb"},
		{RepoCore, "Formula//a.rb"},
		{RepoCore, "Formula/ab/a.rb"},
		{RepoCore, "Formula/Lib/a.rb"},
		{RepoCore, "Formula/lib/a/b.rb"},
		{RepoCore, "Formula/a/b/a.rb"},
		{RepoCore, "Formula/.rb"},
		{RepoCore, "Casks/a.rb"},
		{RepoCore, "Other/a.rb"},
		{RepoCore, "/Formula/a.rb"},
		{RepoCask, "Casks/font/font-A/font-a.rb"},
		{RepoCask, "Casks/font/font-é/font-a.rb"},
		{RepoCask, "Casks/font/font-ab/font-a.rb"},
		{RepoCask, "Casks/font/font-a/not-a-font.rb"},
		{RepoCask, "Casks/font/font-a/deeper/font-a.rb"},
		{RepoCask, "Casks/font/a/font-a.rb"},
	}
	for _, tc := range rejected {
		if _, ok := IdentifierFromPath(tc.repo, tc.path); ok {
			t.Errorf("accepted %q", tc.path)
		}
	}
}
