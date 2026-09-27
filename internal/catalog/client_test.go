package catalog

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/lmilojevicc/brewnicle/internal/domain"
)

func TestFetchNormalize(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "formula") {
			w.Write([]byte(`[{"name":"ok","desc":"Needle","homepage":"https://example.test","oldnames":["old","bad name"],"ruby_source_path":"Formula/o/ok.rb"},{"name":"off","disabled":true},{"name":"bad name"}]`))
			return
		}
		w.Write([]byte(`[{"token":"font-maple","desc":"Font","homepage":"file:///bad","old_tokens":["font-old"],"ruby_source_path":"Casks/font/font-m/font-maple.rb"},{"token":"app","disabled":false,"ruby_source_path":"Casks/a/app.rb"}]`))
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
	if len(got.Packages[0].FormerNames) != 1 || got.Packages[0].SourcePath != "Formula/o/ok.rb" || got.Packages[1].SourcePath != "Casks/font/font-m/font-maple.rb" {
		t.Fatal(got.Packages[0], got.Packages[1])
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

// These tests use only in-memory transports. synctest.Wait observes blocked
// workers without sleeps or wall-clock timing assumptions.
func TestFetchConcurrentOrdering(t *testing.T) {
	for _, first := range []string{"formula", "cask"} {
		t.Run(first+" first", func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				started := make(chan string, 2)
				release := map[string]chan struct{}{"formula": make(chan struct{}), "cask": make(chan struct{})}
				closed := map[string]chan struct{}{"formula": make(chan struct{}), "cask": make(chan struct{})}
				bodies := map[string]string{
					"formula": `[{"name":"formula-ok"},{"name":"bad name"},{"name":"off","disabled":true}]`,
					"cask":    `[{"token":"cask-ok"},{"token":"bad name"},{"token":"also bad"},{"token":"off","disabled":true}]`,
				}
				c := fetchTestClient(func(r *http.Request) (*http.Response, error) {
					name := strings.TrimPrefix(r.URL.Path, "/")
					started <- name
					select {
					case <-release[name]:
					case <-r.Context().Done():
						return nil, r.Context().Err()
					}
					return fetchTestResponse(&fetchTestBody{
						Reader: strings.NewReader(bodies[name]),
						close:  func() { close(closed[name]) },
					}), nil
				})
				done := startTestFetch(ctx, c)
				synctest.Wait()
				assertFetchRequests(t, started)
				assertFetchPending(t, done)
				close(release[first])
				<-closed[first]
				synctest.Wait()
				assertFetchPending(t, done)
				second := "formula"
				if first == second {
					second = "cask"
				}
				close(release[second])
				got := <-done
				if got.err != nil {
					t.Fatal(got.err)
				}
				if len(got.result.Packages) != 2 || got.result.Packages[0].Name != "formula-ok" || got.result.Packages[1].Name != "cask-ok" || got.result.SkippedFormulae != 1 || got.result.SkippedCasks != 2 {
					t.Fatalf("unexpected result: %+v", got.result)
				}
				select {
				case <-closed[second]:
				default:
					t.Fatal("Fetch returned before closing both bodies")
				}
				if len(started) != 0 {
					t.Fatal("unexpected extra request")
				}
			})
		})
	}
}

func TestFetchFailureCancelsAndJoinsSibling(t *testing.T) {
	transportErr := errors.New("transport failure")
	for _, endpoint := range []string{"formula", "cask"} {
		for _, failure := range []string{"transport", "status", "json", "large"} {
			t.Run(endpoint+"/"+failure, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					started := make(chan string, 2)
					siblingReading := make(chan struct{})
					siblingCanceled := make(chan struct{})
					siblingClosed := make(chan struct{})
					finishSibling := make(chan struct{})
					defer func() {
						// Also unblock the worker if an assertion fails early.
						select {
						case <-finishSibling:
						default:
							close(finishSibling)
						}
					}()
					c := fetchTestClient(func(r *http.Request) (*http.Response, error) {
						name := strings.TrimPrefix(r.URL.Path, "/")
						started <- name
						if name != endpoint {
							return fetchTestResponse(&fetchTestBody{
								Reader: fetchTestReader(func([]byte) (int, error) {
									close(siblingReading)
									<-r.Context().Done()
									close(siblingCanceled)
									<-finishSibling
									return 0, r.Context().Err()
								}),
								close: func() { close(siblingClosed) },
							}), nil
						}
						select {
						case <-siblingReading:
						case <-r.Context().Done():
							return nil, r.Context().Err()
						}
						if failure == "transport" {
							return nil, transportErr
						}
						body := "[]"
						if failure == "json" {
							body = "{"
						} else if failure == "large" {
							body = "[{}]"
						}
						resp := fetchTestResponse(io.NopCloser(strings.NewReader(body)))
						if failure == "status" {
							resp.StatusCode, resp.Status = http.StatusServiceUnavailable, "503 Service Unavailable"
						}
						return resp, nil
					})
					c.MaxBytes = 2
					done := startTestFetch(ctx, c)
					synctest.Wait()
					assertFetchRequests(t, started)
					select {
					case <-siblingCanceled:
					default:
						t.Fatal("failed request did not cancel sibling body read")
					}
					assertFetchPending(t, done)
					close(finishSibling)
					got := <-done
					assertEmptyFetchResult(t, got.result)
					want := map[string]string{"transport": "transport failure", "status": "HTTP status 503", "json": "invalid JSON", "large": "response exceeds 2 bytes"}[failure]
					if got.err == nil || !strings.HasPrefix(got.err.Error(), endpoint+" catalog: ") || !strings.Contains(got.err.Error(), want) || errors.Is(got.err, context.Canceled) {
						t.Fatalf("want endpoint-wrapped %s cause, got %v", failure, got.err)
					}
					if failure == "transport" && !errors.Is(got.err, transportErr) {
						t.Fatalf("transport cause lost: %v", got.err)
					}
					select {
					case <-siblingClosed:
					default:
						t.Fatal("Fetch returned before sibling body closed")
					}
				})
			})
		}
	}
}

func TestFetchFailureAfterSiblingSuccess(t *testing.T) {
	for _, endpoint := range []string{"formula", "cask"} {
		t.Run(endpoint, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				succeeded := make(chan struct{})
				c := fetchTestClient(func(r *http.Request) (*http.Response, error) {
					if r.URL.Path != "/"+endpoint {
						return fetchTestResponse(&fetchTestBody{
							Reader: strings.NewReader(`[{"name":"formula-ok","token":"cask-ok"}]`),
							close:  func() { close(succeeded) },
						}), nil
					}
					select {
					case <-succeeded:
					case <-r.Context().Done():
						return nil, r.Context().Err()
					}
					return nil, errors.New("failed after sibling succeeded")
				})
				got := <-startTestFetch(ctx, c)
				assertEmptyFetchResult(t, got.result)
				if got.err == nil || !strings.HasPrefix(got.err.Error(), endpoint+" catalog: ") {
					t.Fatalf("want %s failure, got %v", endpoint, got.err)
				}
			})
		})
	}
}

func TestFetchCallerCancellation(t *testing.T) {
	for _, stage := range []string{"request", "body"} {
		t.Run(stage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				started := make(chan string, 2)
				canceled := make(chan struct{}, 2)
				closed := make(chan struct{}, 2)
				c := fetchTestClient(func(r *http.Request) (*http.Response, error) {
					block := func() error {
						started <- strings.TrimPrefix(r.URL.Path, "/")
						<-r.Context().Done()
						canceled <- struct{}{}
						return r.Context().Err()
					}
					if stage == "request" {
						return nil, block()
					}
					return fetchTestResponse(&fetchTestBody{
						Reader: fetchTestReader(func([]byte) (int, error) { return 0, block() }),
						close:  func() { closed <- struct{}{} },
					}), nil
				})
				done := startTestFetch(ctx, c)
				synctest.Wait()
				assertFetchRequests(t, started)
				assertFetchPending(t, done)
				cancel()
				got := <-done
				assertEmptyFetchResult(t, got.result)
				if !errors.Is(got.err, context.Canceled) {
					t.Fatalf("want caller cancellation, got %v", got.err)
				}
				if len(canceled) != 2 || (stage == "body" && len(closed) != 2) {
					t.Fatalf("Fetch did not join cleanup: canceled=%d closed=%d", len(canceled), len(closed))
				}
			})
		})
	}
}

type fetchTestTransport func(*http.Request) (*http.Response, error)

func (f fetchTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fetchTestClient(transport fetchTestTransport) *Client {
	c := NewClient(&http.Client{Transport: transport})
	c.FormulaURL = "https://catalog.test/formula"
	c.CaskURL = "https://catalog.test/cask"
	return c
}

func fetchTestResponse(body io.ReadCloser) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: body}
}

type fetchTestBody struct {
	io.Reader
	close func()
}

func (b *fetchTestBody) Close() error { b.close(); return nil }

type fetchTestReader func([]byte) (int, error)

func (f fetchTestReader) Read(p []byte) (int, error) { return f(p) }

type fetchTestOutcome struct {
	result Result
	err    error
}

func startTestFetch(ctx context.Context, c *Client) <-chan fetchTestOutcome {
	done := make(chan fetchTestOutcome, 1)
	go func() {
		result, err := c.Fetch(ctx)
		done <- fetchTestOutcome{result, err}
	}()
	return done
}

func assertFetchRequests(t *testing.T, started <-chan string) {
	t.Helper()
	if len(started) != 2 {
		t.Fatalf("want both requests started, got %d", len(started))
	}
	counts := map[string]int{}
	counts[<-started]++
	counts[<-started]++
	if counts["formula"] != 1 || counts["cask"] != 1 {
		t.Fatalf("want exactly one request per endpoint, got %v", counts)
	}
}

func assertFetchPending(t *testing.T, done <-chan fetchTestOutcome) {
	t.Helper()
	select {
	case got := <-done:
		t.Fatalf("Fetch returned before both workers completed: %+v", got)
	default:
	}
}

func assertEmptyFetchResult(t *testing.T, got Result) {
	t.Helper()
	if got.Packages != nil || got.SkippedFormulae != 0 || got.SkippedCasks != 0 {
		t.Fatalf("failure returned partial result: %+v", got)
	}
}
