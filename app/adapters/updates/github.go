// Package updates asks GitHub which release of PodSteer is newest.
//
// THIS IS THE ONLY THING IN PODSTEER THAT TALKS TO ANYTHING BUT A CLUSTER, and
// it exists under conditions that are worth stating where somebody changing it
// will read them.
//
//   - It runs ONLY when the operator has left the check on, and never on the
//     startup path. Nothing waits for it.
//   - The request carries NO IDENTIFIER: no installed version, no platform, no
//     machine id, no query string. The comparison happens here, on what comes
//     back. GitHub requires a User-Agent header and refuses requests without
//     one, so "podsteer" is sent and nothing more — not the version, which
//     would turn every check into a report of what somebody is running.
//   - It is UNAUTHENTICATED. A token would be a credential at rest for a
//     public endpoint, and would identify the installation besides.
//   - GITHUB RATHER THAN A PODSTEER SERVER, deliberately. PodSteer has no
//     access to api.github.com's logs, so this produces no dataset for anyone
//     here to hold, correlate with a future paid tier, or be compelled to
//     produce. An updates.podsteer.com would produce all three, and is the
//     documented path by which an update channel becomes a licence check.
//
// See docs/decisions/0005-the-update-check-is-opt-outable-and-tells-github-nothing.md.
package updates

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// latestReleaseURL is the newest production release of PodSteer.
//
// "Latest" here excludes drafts and pre-releases, which is the selection
// wanted: an -rc- tag is published as a pre-release and is not what the
// Homebrew tap serves.
const latestReleaseURL = "https://api.github.com/repos/podsteer/podsteer/releases/latest"

// requestTimeout bounds one call. Short, because nothing is waiting on it and
// a slow answer is worth abandoning rather than holding a socket open for.
const requestTimeout = 8 * time.Second

// Client reads releases from the GitHub REST API.
type Client struct {
	http *http.Client
	// url is overridable for tests only.
	url string
}

// NewClient returns a client with its own timeout.
//
// Its own http.Client rather than http.DefaultClient: the default has no
// timeout at all, which is how a hung connection becomes a goroutine that
// never returns.
func NewClient() *Client {
	return &Client{
		http: &http.Client{Timeout: requestTimeout},
		url:  latestReleaseURL,
	}
}

// release is the sliver of GitHub's response that matters.
type release struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// Latest returns the tag and page of the newest production release.
func (c *Client) Latest(ctx context.Context) (string, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return "", "", fmt.Errorf("building the request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	// Required by GitHub, and deliberately without the version — see the
	// package comment.
	request.Header.Set("User-Agent", "podsteer")

	response, err := c.http.Do(request)
	if err != nil {
		return "", "", fmt.Errorf("asking GitHub: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	// 403 IS THE EXPECTED FAILURE, not an exotic one. Unauthenticated requests
	// are limited to 60 an hour PER IP, and behind a corporate NAT every
	// PodSteer user in the building shares that budget with every other tool
	// on the network hitting GitHub. It has to read as "we do not know",
	// never as a fault.
	if response.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("GitHub answered %s", response.Status)
	}

	var latest release
	if err := json.NewDecoder(response.Body).Decode(&latest); err != nil {
		return "", "", fmt.Errorf("decoding the response: %w", err)
	}
	if latest.Draft || latest.Prerelease {
		return "", "", fmt.Errorf("the latest release is a draft or pre-release")
	}
	if latest.TagName == "" {
		return "", "", fmt.Errorf("the release carries no tag")
	}
	return latest.TagName, latest.HTMLURL, nil
}
