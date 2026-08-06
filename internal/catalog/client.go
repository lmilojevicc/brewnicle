package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/milo/brewnicle/internal/domain"
)

const (
	FormulaURL             = "https://formulae.brew.sh/api/formula.json"
	CaskURL                = "https://formulae.brew.sh/api/cask.json"
	MaxResponseBytes int64 = 64 << 20
)

type Client struct {
	HTTP       *http.Client
	FormulaURL string
	CaskURL    string
	MaxBytes   int64
}

type Result struct {
	Packages                      []domain.Package
	SkippedFormulae, SkippedCasks int
}

func NewClient(h *http.Client) *Client {
	if h == nil {
		h = http.DefaultClient
	}
	return &Client{HTTP: h, FormulaURL: FormulaURL, CaskURL: CaskURL, MaxBytes: MaxResponseBytes}
}

func (c *Client) Fetch(ctx context.Context) (Result, error) {
	var fs []formulaJSON
	if err := c.get(ctx, c.FormulaURL, &fs); err != nil {
		return Result{}, fmt.Errorf("formula catalog: %w", err)
	}
	var cs []caskJSON
	if err := c.get(ctx, c.CaskURL, &cs); err != nil {
		return Result{}, fmt.Errorf("cask catalog: %w", err)
	}
	fp, fskip := normalizeFormulae(fs)
	cp, cskip := normalizeCasks(cs)
	return Result{Packages: append(fp, cp...), SkippedFormulae: fskip, SkippedCasks: cskip}, nil
}

func (c *Client) get(ctx context.Context, rawURL string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP status %s", resp.Status)
	}
	limit := c.MaxBytes
	if limit <= 0 {
		limit = MaxResponseBytes
	}
	r := io.LimitReader(resp.Body, limit+1)
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if int64(len(b)) > limit {
		return fmt.Errorf("response exceeds %d bytes", limit)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}
