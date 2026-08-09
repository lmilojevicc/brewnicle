package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestEmptyPublishRejected(t *testing.T) {
	in := publishInput(time.Now())
	in.Packages = nil
	if _, e := (Publisher{Path: filepath.Join(t.TempDir(), "x")}).Publish(context.Background(), in); e == nil {
		t.Fatal("want error")
	}
}
func TestCanceledBeforePublication(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := (Publisher{Path: filepath.Join(t.TempDir(), "x")}).Publish(ctx, publishInput(time.Now())); e == nil {
		t.Fatal("want error")
	}
}
