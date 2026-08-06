//go:build integration

package catalog

import (
	"context"
	"testing"
	"time"
)

func TestLiveCatalogCompatibility(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	got, e := NewClient(nil).Fetch(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if len(got.Packages) < 1000 {
		t.Fatalf("unexpectedly small catalog: %d", len(got.Packages))
	}
}
