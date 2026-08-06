package ui

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func TestProgramRendersStartupFrameBeforeRefresh(t *testing.T) {
	for _, tc := range []struct {
		name      string
		bootstrap bool
		stale     bool
		want      string
	}{
		{name: "cached stale", stale: true, want: "STALE"},
		{name: "bootstrap", bootstrap: true, want: "building first index"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output lockedBuffer
			started := make(chan string, 1)
			deps := Dependencies{Refresh: func() <-chan RefreshEvent {
				started <- output.String()
				ch := make(chan RefreshEvent, 1)
				ch <- RefreshEvent{Done: true, Err: errors.New("test stop")}
				close(ch)
				return ch
			}}
			packages := uiPkgs()
			if tc.bootstrap {
				packages = nil
			}
			program := tea.NewProgram(
				New(packages, tc.bootstrap, tc.stale, true, deps),
				tea.WithInput(nil),
				tea.WithOutput(&output),
				tea.WithoutSignals(),
			)
			done := make(chan error, 1)
			go func() {
				_, err := program.Run()
				done <- err
			}()
			program.Send(tea.WindowSizeMsg{Width: 100, Height: 20})

			select {
			case beforeRefresh := <-started:
				if !strings.Contains(beforeRefresh, tc.want) {
					t.Fatalf("refresh started before %q was rendered; output=%q", tc.want, beforeRefresh)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("refresh did not start")
			}
			program.Quit()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("program did not quit")
			}
		})
	}
}
