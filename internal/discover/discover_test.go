package discover

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewClient()
	c.BaseURL = srv.URL
	c.HTTPClient = srv.Client()
	c.MaxRetries = 3
	c.Backoff = time.Millisecond
	return c
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func pkg(name, repo string, depRepos, stars int, fork, archived bool, pushedAgo time.Duration) Package {
	return Package{
		Name:                name,
		Ecosystem:           "go",
		RepositoryURL:       repo,
		DependentReposCount: depRepos,
		RepoMetadata: RepoMetadata{
			HTMLURL:         repo,
			Fork:            fork,
			Archived:        archived,
			StargazersCount: stars,
			PushedAt:        time.Now().Add(-pushedAgo),
			Language:        "Go",
		},
	}
}

func TestDiscoverFiltersAndRanks(t *testing.T) {
	pkgs := []Package{
		pkg("github.com/cli/cli", "https://github.com/cli/cli", 5000, 40000, false, false, 24*time.Hour),
		pkg("github.com/fork/cli", "https://github.com/fork/cli", 100, 5, true, false, 24*time.Hour),
		pkg("github.com/dead/proj", "https://github.com/dead/proj", 50, 2000, false, true, 24*time.Hour),
		pkg("github.com/old/proj", "https://github.com/old/proj", 50, 100, false, false, 5*365*24*time.Hour),
		pkg("github.com/gohugoio/hugo", "https://github.com/gohugoio/hugo", 1200, 70000, false, false, 48*time.Hour),
		pkg("github.com/cli/cli/v2", "https://github.com/cli/cli", 10, 40000, false, false, 24*time.Hour),
		{Name: "github.com/norepo/x", DependentPackagesCount: 9999},
	}

	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/dependent_packages") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("sort") != "dependent_repos_count" {
			t.Errorf("sort = %q, want dependent_repos_count", r.URL.Query().Get("sort"))
		}
		if r.URL.Query().Get("latest") != "true" {
			t.Errorf("latest = %q, want true", r.URL.Query().Get("latest"))
		}
		if !strings.Contains(r.URL.Path, "proxy.golang.org") {
			t.Errorf("path missing go registry: %s", r.URL.Path)
		}
		_, _ = w.Write(mustJSON(t, pkgs))
	})

	got, err := Discover(context.Background(), Options{
		Ecosystem: "go",
		Package:   "github.com/spf13/cobra",
		Limit:     3,
		Pool:      10,
		Client:    client,
		Stderr:    io.Discard,
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2 (only cli/cli and hugo survive filtering)", len(got))
	}
	if got[0].Name != "github.com/cli/cli" {
		t.Errorf("rank[0] = %s, want cli/cli (higher repo count)", got[0].Name)
	}
	if got[1].Name != "github.com/gohugoio/hugo" {
		t.Errorf("rank[1] = %s, want hugo", got[1].Name)
	}

	d := got[0].Dependent()
	if d.Source != "discover" || d.Repo != "https://github.com/cli/cli" {
		t.Errorf("Dependent() = %+v", d)
	}
	if !strings.Contains(d.Comment, "5000 dependent repos") || !strings.Contains(d.Comment, "40000 stars") {
		t.Errorf("Comment missing scores: %q", d.Comment)
	}
}

func TestDiscoverDropsUpstreamSelf(t *testing.T) {
	pkgs := []Package{
		pkg("github.com/spf13/cobra/doc", "https://github.com/spf13/cobra", 100, 30000, false, false, time.Hour),
		pkg("github.com/cli/cli", "https://github.com/cli/cli", 5000, 40000, false, false, time.Hour),
	}
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(mustJSON(t, pkgs))
	})

	got, err := Discover(context.Background(), Options{
		Ecosystem: "go",
		Package:   "github.com/spf13/cobra",
		Limit:     5,
		Client:    client,
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	for _, c := range got {
		if c.Repo == "https://github.com/spf13/cobra" {
			t.Errorf("upstream's own repo should be dropped: %+v", c)
		}
	}
}

func TestDiscoverEmpty(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("[]"))
	})
	_, err := Discover(context.Background(), Options{Ecosystem: "go", Package: "x", Client: client})
	if err == nil || !strings.Contains(err.Error(), "no dependents found") {
		t.Fatalf("error = %v, want no-dependents", err)
	}
}

func TestDiscoverUnknownEcosystem(t *testing.T) {
	_, err := Discover(context.Background(), Options{Ecosystem: "cobol", Package: "x"})
	if err == nil || !strings.Contains(err.Error(), "no configured ecosyste.ms registry") {
		t.Fatalf("error = %v, want unknown-ecosystem", err)
	}
}

func TestClientRetriesOn500(t *testing.T) {
	var attempts int32
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`[{"name":"x","repository_url":"https://x"}]`))
	})

	var out []Package
	err := client.get(context.Background(), client.BaseURL+"/x", &out)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
	if len(out) != 1 || out[0].Name != "x" {
		t.Errorf("out = %+v", out)
	}
}

func TestClientGivesUpAfterRetries(t *testing.T) {
	var attempts int32
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	client.MaxRetries = 2

	var out []Package
	err := client.get(context.Background(), client.BaseURL+"/x", &out)
	if err == nil || !strings.Contains(err.Error(), "after 2 attempts") {
		t.Fatalf("error = %v, want after-N-attempts", err)
	}
	if atomic.LoadInt32(&attempts) != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
}

func TestClientNoRetryOn404(t *testing.T) {
	var attempts int32
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		http.Error(w, "nope", http.StatusNotFound)
	})

	var out []Package
	err := client.get(context.Background(), client.BaseURL+"/x", &out)
	if err == nil {
		t.Fatal("want error")
	}
	if atomic.LoadInt32(&attempts) != 1 {
		t.Errorf("attempts = %d, want 1 (no retry on 4xx)", attempts)
	}
}

func TestRepoMetadataUnmarshal(t *testing.T) {
	for _, body := range []string{`null`, `[]`, `{}`} {
		var r RepoMetadata
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			t.Errorf("unmarshal %s: %v", body, err)
		}
	}
	var r RepoMetadata
	body := `{"full_name":"a/b","fork":true,"stargazers_count":42,"pushed_at":"2026-01-02T03:04:05Z"}`
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.FullName != "a/b" || !r.Fork || r.StargazersCount != 42 || r.PushedAt.IsZero() {
		t.Errorf("r = %+v", r)
	}
}

func TestDependentPackagesPaging(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		switch page {
		case "1":
			batch := make([]Package, 100)
			for i := range batch {
				batch[i] = Package{Name: "p1-" + string(rune('a'+i%26))}
			}
			_, _ = w.Write(mustJSON(t, batch))
		case "2":
			_, _ = w.Write(mustJSON(t, []Package{{Name: "p2-a"}, {Name: "p2-b"}}))
		default:
			_, _ = w.Write([]byte("[]"))
		}
	})

	got, err := client.DependentPackages(context.Background(), "go", "x", 150)
	if err != nil {
		t.Fatalf("DependentPackages: %v", err)
	}
	if len(got) != 102 {
		t.Errorf("got %d packages, want 102", len(got))
	}
}

func TestScorePrefersDownloads(t *testing.T) {
	a := Candidate{DependentRepos: 1000, Stars: 50000}
	b := Candidate{Downloads: 5_000_000}
	if b.Score() <= a.Score() {
		t.Errorf("downloads should win: a=%d b=%d", a.Score(), b.Score())
	}
}
