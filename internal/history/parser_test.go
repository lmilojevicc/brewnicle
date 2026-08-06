package history

import (
	"strings"
	"testing"
	"time"
)

func TestParseLogStrictFramingAndEarliestEvent(t *testing.T) {
	raw := "\x1e200\x00\nFormula/a.rb\x00Other/x.rb\x00\x1e100\x00\nFormula/a/a.rb\x00"
	got, err := ParseLog(strings.NewReader(raw), RepoCore)
	if err != nil {
		t.Fatal(err)
	}
	if !got["a"].Equal(time.Unix(100, 0)) {
		t.Fatal(got)
	}
}

func TestParseLogRejectsMalformedOrTruncatedFraming(t *testing.T) {
	for _, raw := range []string{
		"Formula/a.rb\x00",
		"\x1ebad\x00\nFormula/a.rb\x00",
		"\x1e",
		"\x1e1\x00\nFormula/a.rb",
		"\x1e1\x00Formula/a.rb\x00",
		"\x1e1\x00\x00\nFormula/a.rb\x00",
		"\x1e1\x00",
		"\x1e1\x00\nFormula/a.rb\x00\x1e2\x00",
	} {
		if _, err := ParseLog(strings.NewReader(raw), RepoCore); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

func TestParseLogDoesNotNormalizeFilenameBytes(t *testing.T) {
	raw := "\x1e1\x00\nFormula/a.rb\n\x00Formula/b.rb\r\x00Formula/good.rb\x00"
	got, err := ParseLog(strings.NewReader(raw), RepoCore)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["a"]; ok {
		t.Fatal("newline-suffixed filename was aliased")
	}
	if _, ok := got["b"]; ok {
		t.Fatal("carriage-return-suffixed filename was aliased")
	}
	if _, ok := got["good"]; !ok {
		t.Fatal(got)
	}
}
