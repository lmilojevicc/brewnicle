package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/milo/brewnicle/internal/domain"
)

func TestFetchNormalize(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "formula") {
			w.Write([]byte(`[{"name":"ok","desc":"Needle","homepage":"https://example.test","oldnames":["old","bad name"]},{"name":"off","disabled":true},{"name":"bad name"}]`))
			return
		}
		w.Write([]byte(`[{"token":"font-maple","desc":"Font","homepage":"file:///bad","old_tokens":["font-old"]},{"token":"app","disabled":false}]`))
	}))
	defer s.Close()
	c := NewClient(s.Client())
	c.FormulaURL = s.URL + "/formula"
	c.CaskURL = s.URL + "/cask"
	got, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Packages) != 3 || got.SkippedFormulae != 1 {
		t.Fatalf("%+v", got)
	}
	if got.Packages[1].Kind != domain.KindFont || got.Packages[1].Homepage != "" {
		t.Fatalf("%+v", got.Packages[1])
	}
	if len(got.Packages[0].FormerNames) != 1 {
		t.Fatal(got.Packages[0].FormerNames)
	}
}

func TestFetchFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body    string
		status, limit int
	}{
		{"status", "[]", 500, 100}, {"json", "{", 200, 100}, {"large", "[{}]", 200, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); w.Write([]byte(tc.body)) }))
			defer s.Close()
			c := NewClient(s.Client())
			c.FormulaURL = s.URL
			c.CaskURL = s.URL
			c.MaxBytes = int64(tc.limit)
			if _, err := c.Fetch(context.Background()); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := NewClient(http.DefaultClient)
	c.FormulaURL = "http://127.0.0.1:1"
	if _, err := c.Fetch(ctx); err == nil {
		t.Fatal("want error")
	}
}
