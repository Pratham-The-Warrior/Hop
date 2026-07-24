// Package update provides GitHub release version checking for hop.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// GitHubRelease represents the minimal fields from a GitHub releases API response.
type GitHubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// CheckLatestVersion queries the GitHub releases API for the latest release
// of the given repository. Returns the tag name (e.g., "v1.2.3") and the
// release page URL, or an error if the check fails.
//
// The check uses a short timeout to avoid blocking the CLI.
func CheckLatestVersion(ctx context.Context, owner, repo string) (*GitHubRelease, error) {
	// 3-second timeout — never block the user
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "hop-cli-update-check")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	if release.TagName == "" {
		return nil, fmt.Errorf("empty tag_name in response")
	}

	return &release, nil
}

// CompareVersions compares two semver version strings (with or without "v" prefix).
// Returns:
//
//	-1 if current < latest  (update available)
//	 0 if current == latest (up to date)
//	+1 if current > latest  (ahead, e.g., dev build)
func CompareVersions(current, latest string) int {
	curParts := parseVersion(current)
	latParts := parseVersion(latest)

	for i := 0; i < 3; i++ {
		if curParts[i] < latParts[i] {
			return -1
		}
		if curParts[i] > latParts[i] {
			return 1
		}
	}
	return 0
}

// parseVersion extracts [major, minor, patch] from a version string.
// Handles formats like "v1.2.3", "1.2.3", "1.2", "1".
func parseVersion(v string) [3]int {
	v = strings.TrimPrefix(v, "v")

	// Strip any pre-release suffix (e.g., "1.2.3-beta" → "1.2.3")
	if idx := strings.IndexAny(v, "-+"); idx >= 0 {
		v = v[:idx]
	}

	parts := strings.Split(v, ".")
	var result [3]int
	for i := 0; i < len(parts) && i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		if err == nil {
			result[i] = n
		}
	}
	return result
}
