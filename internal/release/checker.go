package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	lowerHex40        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	internalReleaseID = regexp.MustCompile(`^release-\d{14}-[0-9a-f]{40}$`)
)

type Checker struct {
	client     *http.Client
	repository string
	current    string
	revision   string
	releasedAt time.Time
	now        func() time.Time
	cacheTTL   time.Duration

	mu       sync.Mutex
	cached   Snapshot
	cachedAt time.Time
}

type Snapshot struct {
	Current          Version
	Latest           Version
	CheckedAt        time.Time
	Status           Status
	CheckUnavailable bool
}

type Version struct {
	Label       string
	PublishedAt time.Time
	URL         string
}

type Status string

const (
	StatusUnknown   Status = "unknown"
	StatusCurrent   Status = "current"
	StatusAvailable Status = "available"
)

type githubRelease struct {
	Name            string    `json:"name"`
	TagName         string    `json:"tag_name"`
	HTMLURL         string    `json:"html_url"`
	PublishedAt     time.Time `json:"published_at"`
	TargetCommitish string    `json:"target_commitish"`
	Draft           bool      `json:"draft"`
	Prerelease      bool      `json:"prerelease"`
}

func NewChecker(client *http.Client, repository, current, revision string, releasedAt time.Time, cacheTTL time.Duration, now func() time.Time) *Checker {
	if client == nil {
		client = http.DefaultClient
	}
	if now == nil {
		now = time.Now
	}
	return &Checker{client: client, repository: repository, current: strings.TrimSpace(current), revision: strings.TrimSpace(revision), releasedAt: releasedAt, cacheTTL: cacheTTL, now: now}
}

func (c *Checker) Snapshot(ctx context.Context) Snapshot {
	c.mu.Lock()
	if !c.cachedAt.IsZero() && c.now().Sub(c.cachedAt) < c.cacheTTL {
		snapshot := c.cached
		c.mu.Unlock()
		return snapshot
	}
	c.mu.Unlock()

	snapshot := c.fetch(ctx)

	c.mu.Lock()
	c.cached, c.cachedAt = snapshot, snapshot.CheckedAt
	c.mu.Unlock()
	return snapshot
}

func (c *Checker) fetch(ctx context.Context) Snapshot {
	checkedAt := c.now()
	snapshot := Snapshot{
		Current:   Version{Label: displayLabel(c.current), PublishedAt: c.releasedAt},
		CheckedAt: checkedAt,
		Status:    StatusUnknown,
	}
	if c.repository == "" {
		snapshot.CheckUnavailable = true
		return snapshot
	}
	latest, err := c.release(ctx, "latest")
	if err != nil {
		snapshot.CheckUnavailable = true
		return snapshot
	}
	snapshot.Latest = versionFromRelease(latest)
	if c.matchesCurrent(latest) {
		snapshot.Current = snapshot.Latest
		snapshot.Status = StatusCurrent
		return snapshot
	}
	if !snapshot.Current.PublishedAt.IsZero() && latest.PublishedAt.After(snapshot.Current.PublishedAt) {
		snapshot.Status = StatusAvailable
		return snapshot
	}
	current, err := c.findCurrentRelease(ctx)
	if err != nil {
		if c.current != "" && !technicalVersion(c.current) && latest.TagName != c.current {
			snapshot.Status = StatusAvailable
		}
		return snapshot
	}
	snapshot.Current = versionFromRelease(current)
	if latest.PublishedAt.After(current.PublishedAt) && latest.TagName != current.TagName {
		snapshot.Status = StatusAvailable
	} else {
		snapshot.Status = StatusCurrent
	}
	return snapshot
}

func (c *Checker) findCurrentRelease(ctx context.Context) (githubRelease, error) {
	releases, err := c.releases(ctx)
	if err != nil {
		return githubRelease{}, err
	}
	for _, release := range releases {
		if release.Draft || release.Prerelease {
			continue
		}
		if c.matchesCurrent(release) {
			return release, nil
		}
	}
	return githubRelease{}, errors.New("current release not found")
}

func (c *Checker) matchesCurrent(release githubRelease) bool {
	return currentMatches(c.current, release) || currentMatches(c.revision, release)
}

func (c *Checker) release(ctx context.Context, id string) (githubRelease, error) {
	var release githubRelease
	if err := c.get(ctx, fmt.Sprintf("https://api.github.com/repos/%s/releases/%s", c.repository, id), &release); err != nil {
		return githubRelease{}, err
	}
	return release, nil
}

func (c *Checker) releases(ctx context.Context) ([]githubRelease, error) {
	var releases []githubRelease
	if err := c.get(ctx, fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=30", c.repository), &releases); err != nil {
		return nil, err
	}
	return releases, nil
}

func (c *Checker) get(ctx context.Context, url string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "mycfc-release-check")
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("github release status %d", response.StatusCode)
	}
	return json.NewDecoder(response.Body).Decode(target)
}

func versionFromRelease(release githubRelease) Version {
	label := strings.TrimSpace(release.Name)
	if label == "" {
		label = strings.TrimSpace(release.TagName)
	}
	return Version{Label: displayLabel(label), PublishedAt: release.PublishedAt, URL: release.HTMLURL}
}

func currentMatches(current string, release githubRelease) bool {
	current = strings.TrimSpace(current)
	return current != "" && (current == release.TagName || current == release.Name || current == release.TargetCommitish)
}

func displayLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || technicalVersion(value) {
		return "Versão instalada"
	}
	return value
}

func technicalVersion(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return lowerHex40.MatchString(value) || internalReleaseID.MatchString(value)
}
