package release

import (
	"context"
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
