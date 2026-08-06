package store

import (
	"context"
	"github.com/milo/brewnicle/internal/domain"
	"path/filepath"
	"testing"
	"time"
)

func TestEmptyPublishRejected(t *testing.T) {
	_, e := (Publisher{Path: filepath.Join(t.TempDir(), "x")}).Publish(context.Background(), nil, time.Now())
	if e == nil {
		t.Fatal("want error")
	}
}
func TestCanceledBeforePublication(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e := (Publisher{Path: filepath.Join(t.TempDir(), "x")}).Publish(ctx, []domain.Package{{Name: "x", Kind: domain.KindFormula, InstallTarget: "x"}}, time.Now())
	if e == nil {
		t.Fatal("want error")
	}
}
