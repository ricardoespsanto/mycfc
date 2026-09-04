package release

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSnapshotReportsCurrentWhenLatestMatches(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/repos/cfcoimbra/mycfc/releases/latest" {
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
		return jsonResponse(`{"name":"v1.2.0","tag_name":"v1.2.0","html_url":"https://github.com/cfcoimbra/mycfc/releases/tag/v1.2.0","published_at":"2026-08-01T10:00:00Z"}`), nil
	})}
	checker := NewChecker(client, "cfcoimbra/mycfc", "v1.2.0", "", time.Time{}, time.Minute, func() time.Time { return now })

	snapshot := checker.Snapshot(context.Background())

	if snapshot.Status != StatusCurrent || snapshot.Current.Label != "v1.2.0" || snapshot.Latest.URL == "" || !snapshot.CheckedAt.Equal(now) {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestSnapshotReportsAvailableWithoutLeakingTechnicalVersion(t *testing.T) {
	currentReleasedAt := time.Date(2026, 7, 20, 9, 30, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return jsonResponse(`{"name":"v1.3.0","tag_name":"v1.3.0","published_at":"2026-08-01T10:00:00Z"}`), nil
	})}
	checker := NewChecker(client, "cfcoimbra/mycfc", strings.Repeat("a", 40), "", currentReleasedAt, time.Minute, time.Now)

	snapshot := checker.Snapshot(context.Background())

	if snapshot.Status != StatusAvailable || snapshot.Current.Label != "Versão instalada" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestSnapshotReportsAvailableWithoutLeakingInternalReleaseTag(t *testing.T) {
	revision := strings.Repeat("a", 40)
	currentReleasedAt := time.Date(2026, 7, 20, 9, 30, 0, 0, time.UTC)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return jsonResponse(`{"name":"v1.3.0","tag_name":"v1.3.0","published_at":"2026-08-01T10:00:00Z"}`), nil
	})}
	checker := NewChecker(client, "cfcoimbra/mycfc", "release-20260803120000-"+revision, "", currentReleasedAt, time.Minute, time.Now)

	snapshot := checker.Snapshot(context.Background())

	if snapshot.Status != StatusAvailable || snapshot.Current.Label != "Versão instalada" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestSnapshotCachesReleaseChecks(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return jsonResponse(`{"name":"v1.2.0","tag_name":"v1.2.0","published_at":"2026-08-01T10:00:00Z"}`), nil
	})}
	checker := NewChecker(client, "cfcoimbra/mycfc", "v1.2.0", "", time.Time{}, time.Minute, time.Now)

	_ = checker.Snapshot(context.Background())
	_ = checker.Snapshot(context.Background())

	if calls != 1 {
		t.Fatalf("release checks = %d", calls)
	}
}

func TestSnapshotMatchesReleaseByInternalRevision(t *testing.T) {
	revision := strings.Repeat("a", 40)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return jsonResponse(`{"name":"v0.0.1","tag_name":"v0.0.1","target_commitish":"` + revision + `","published_at":"2026-08-03T12:00:00Z"}`), nil
	})}
	checker := NewChecker(client, "cfcoimbra/mycfc", "release-20260803120000-"+revision, revision, time.Time{}, time.Minute, time.Now)

	snapshot := checker.Snapshot(context.Background())

	if snapshot.Status != StatusCurrent || snapshot.Current.Label != "v0.0.1" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestFindCurrentReleaseSkipsDraftsAndPrereleases(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/repos/cfcoimbra/mycfc/releases" || request.URL.Query().Get("per_page") != "30" {
			t.Fatalf("unexpected request=%s", request.URL.String())
		}
		return jsonResponse(`[
			{"name":"v2.0.0-draft","tag_name":"v2.0.0","draft":true},
			{"name":"v2.0.0-rc","tag_name":"v2.0.0-rc","prerelease":true},
			{"name":"v1.9.0","tag_name":"v1.9.0","target_commitish":"abc123"}
		]`), nil
	})}
	checker := NewChecker(client, "cfcoimbra/mycfc", "v1.9.0", "", time.Time{}, time.Minute, time.Now)

	current, err := checker.findCurrentRelease(context.Background())
	if err != nil || current.TagName != "v1.9.0" || current.Draft || current.Prerelease {
		t.Fatalf("release=%#v err=%v", current, err)
	}
}

func TestFindCurrentReleaseReportsMissingPublishedMatch(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(`[{"tag_name":"v2.0.0"}]`), nil
	})}
	_, err := NewChecker(client, "cfcoimbra/mycfc", "v1.0.0", "", time.Time{}, time.Minute, time.Now).findCurrentRelease(context.Background())
	if err == nil || !strings.Contains(err.Error(), "current release not found") {
		t.Fatalf("error=%v", err)
	}
}

func TestSnapshotFailsClosedForUnavailableAndInconclusiveReleaseData(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name        string
		repository  string
		current     string
		transport   roundTripFunc
		want        Status
		unavailable bool
	}{
		{"repository omitted", "", "v1.0.0", nil, StatusUnknown, true},
		{"latest HTTP failure", "cfcoimbra/mycfc", "v1.0.0", func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") }, StatusUnknown, true},
		{"latest non-success", "cfcoimbra/mycfc", "v1.0.0", func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader("unavailable")), Header: make(http.Header)}, nil
		}, StatusUnknown, true},
		{"missing current release makes named version available", "cfcoimbra/mycfc", "v1.0.0", func(r *http.Request) (*http.Response, error) {
			if strings.HasSuffix(r.URL.Path, "/latest") {
				return jsonResponse(`{"name":"v2.0.0","tag_name":"v2.0.0","published_at":"2026-08-01T10:00:00Z"}`), nil
			}
			return jsonResponse(`[]`), nil
		}, StatusAvailable, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := http.DefaultClient
			if tc.transport != nil {
				client = &http.Client{Transport: tc.transport}
			}
			snapshot := NewChecker(client, tc.repository, tc.current, "", time.Time{}, time.Minute, func() time.Time { return now }).Snapshot(t.Context())
			if snapshot.Status != tc.want || snapshot.CheckUnavailable != tc.unavailable {
				t.Fatalf("snapshot=%+v", snapshot)
			}
		})
	}
}

func TestReleaseHelpersPreservePublicLabelsAndHTTPRequestContract(t *testing.T) {
	if got := versionFromRelease(githubRelease{TagName: "  v1.0.0  "}); got.Label != "v1.0.0" {
		t.Fatalf("version=%#v", got)
	}
	if !currentMatches("abc", githubRelease{TargetCommitish: "abc"}) || currentMatches("", githubRelease{TagName: "v1"}) {
		t.Fatal("current matching failed closed")
	}
	checker := NewChecker(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("User-Agent") != "mycfc-release-check" {
			t.Fatalf("headers=%v", r.Header)
		}
		return jsonResponse(`{"tag_name":"v1.0.0"}`), nil
	})}, "cfcoimbra/mycfc", "v1.0.0", "", time.Time{}, time.Minute, time.Now)
	if _, err := checker.release(t.Context(), "latest"); err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
