package discover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to packages.ecosyste.ms. The dependent_packages
// endpoint times out for popular packages until
// ecosyste-ms/packages#1657 is deployed; retries with backoff are
// applied but a hard error is returned once they're exhausted.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
	UserAgent  string
	MaxRetries int
	Backoff    time.Duration // initial backoff; doubles per retry
}

const (
	defaultBaseURL    = "https://packages.ecosyste.ms/api/v1"
	defaultUserAgent  = "downstream (+https://github.com/git-pkgs/downstream)"
	defaultTimeout    = 30 * time.Second
	defaultMaxRetries = 4
	defaultBackoff    = time.Second
)

func NewClient() *Client {
	return &Client{
		BaseURL:    defaultBaseURL,
		HTTPClient: &http.Client{Timeout: defaultTimeout},
		UserAgent:  defaultUserAgent,
		MaxRetries: defaultMaxRetries,
		Backoff:    defaultBackoff,
	}
}

// ecosystem name -> ecosyste.ms registry name
var registryByEcosystem = map[string]string{
	"go":        "proxy.golang.org",
	"golang":    "proxy.golang.org",
	"npm":       "npmjs.org",
	"rubygems":  "rubygems.org",
	"gem":       "rubygems.org",
	"pypi":      "pypi.org",
	"cargo":     "crates.io",
	"packagist": "packagist.org",
	"composer":  "packagist.org",
}

// Sort key per ecosystem. Go has no download counts so popularity is
// approximated by how many repositories use the dependent.
var sortByEcosystem = map[string]string{
	"go":     "dependent_repos_count",
	"golang": "dependent_repos_count",
}

const fallbackSort = "downloads"

// Package is the subset of the packages.ecosyste.ms package object
// we care about.
type Package struct {
	Name                   string       `json:"name"`
	Ecosystem              string       `json:"ecosystem"`
	RepositoryURL          string       `json:"repository_url"`
	DependentPackagesCount int          `json:"dependent_packages_count"`
	DependentReposCount    int          `json:"dependent_repos_count"`
	Downloads              int64        `json:"downloads"`
	LatestRelease          string       `json:"latest_release_number"`
	Status                 string       `json:"status"`
	RepoMetadata           RepoMetadata `json:"repo_metadata"`
}

type RepoMetadata struct {
	FullName        string    `json:"full_name"`
	HTMLURL         string    `json:"html_url"`
	Fork            bool      `json:"fork"`
	Archived        bool      `json:"archived"`
	StargazersCount int       `json:"stargazers_count"`
	PushedAt        time.Time `json:"pushed_at"`
	Language        string    `json:"language"`
	SourceName      string    `json:"source_name"`
}

// UnmarshalJSON tolerates repo_metadata being null or an empty array
// (the API returns [] when the repo hasn't been synced).
func (r *RepoMetadata) UnmarshalJSON(data []byte) error {
	s := string(data)
	if s == "null" || s == "[]" || s == "{}" {
		*r = RepoMetadata{}
		return nil
	}
	type raw RepoMetadata
	var tmp raw
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	*r = RepoMetadata(tmp)
	return nil
}

// DependentPackages fetches up to limit dependents of pkg in the
// given ecosystem, sorted server-side by the ecosystem's popularity
// proxy. It pages until limit is reached or the server returns an
// empty page.
func (c *Client) DependentPackages(ctx context.Context, ecosystem, pkg string, limit int) ([]Package, error) {
	registry, ok := registryByEcosystem[ecosystem]
	if !ok {
		return nil, fmt.Errorf("ecosystem %q has no configured ecosyste.ms registry", ecosystem)
	}
	sortKey := sortByEcosystem[ecosystem]
	if sortKey == "" {
		sortKey = fallbackSort
	}

	const perPage = 100
	var out []Package
	for page := 1; len(out) < limit; page++ {
		q := url.Values{
			"sort":     {sortKey},
			"order":    {"desc"},
			"latest":   {"true"},
			"per_page": {strconv.Itoa(perPage)},
			"page":     {strconv.Itoa(page)},
		}
		endpoint := fmt.Sprintf("%s/registries/%s/packages/%s/dependent_packages?%s",
			c.BaseURL, url.PathEscape(registry), pathSegment(pkg), q.Encode())

		var batch []Package
		if err := c.get(ctx, endpoint, &batch); err != nil {
			return nil, err
		}
		out = append(out, batch...)
		if len(batch) < perPage {
			break
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (c *Client) get(ctx context.Context, endpoint string, dest any) error {
	retries := max(c.MaxRetries, 1)
	backoff := c.Backoff
	var lastErr error
	for attempt := range retries {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}
		err := c.getOnce(ctx, endpoint, dest)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryable(err) {
			return err
		}
	}
	return fmt.Errorf("ecosyste.ms request failed after %d attempts: %w", retries, lastErr)
}

func (c *Client) getOnce(ctx context.Context, endpoint string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return retryableError{err}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError {
		_, _ = io.Copy(io.Discard, resp.Body)
		return retryableError{fmt.Errorf("GET %s: %s", endpoint, resp.Status)}
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, errBodyLimit))
		return fmt.Errorf("GET %s: %s: %s", endpoint, resp.Status, body)
	}

	return json.NewDecoder(resp.Body).Decode(dest)
}

const errBodyLimit = 4 << 10

type retryableError struct{ err error }

func (e retryableError) Error() string { return e.err.Error() }
func (e retryableError) Unwrap() error { return e.err }

func isRetryable(err error) bool {
	var r retryableError
	return errors.As(err, &r)
}

// pathSegment escapes s for use as a single URL path segment.
// url.PathEscape leaves "/" alone since it's the segment separator;
// the ecosyste.ms package route needs it encoded.
func pathSegment(s string) string {
	return strings.ReplaceAll(url.PathEscape(s), "/", "%2F")
}
