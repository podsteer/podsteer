// Command releasegen writes the Kubernetes support-window table.
//
// The dates come from the release team's own schedule in kubernetes/website,
// which is the file the published support matrix is rendered from. They were
// maintained by hand here before this existed, and four of the ten entries
// were wrong — 1.31 by a fortnight, 1.30 by two and a half weeks — which is
// exactly the failure mode a hand-copied table has: it looks right, nobody
// checks it, and it is used to tell somebody their cluster is unsupported.
//
// Generated at build time rather than fetched at runtime, deliberately. This
// is a desktop tool that must work on an aircraft and must not phone anywhere
// simply for being opened; baking the table in keeps it accurate as of each
// PodSteer release and costs no network at all afterwards. The generated file
// records when it was compiled so the application can say how old it is.
//
// Run with: make releases
package main

import (
	"flag"
	"fmt"
	"go/format"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"sigs.k8s.io/yaml"
)

const (
	// scheduleURL holds the releases still receiving patches.
	scheduleURL = "https://raw.githubusercontent.com/kubernetes/website/main/data/releases/schedule.yaml"
	// eolURL holds the releases that have stopped.
	eolURL = "https://raw.githubusercontent.com/kubernetes/website/main/data/releases/eol.yaml"
)

// schedule is the shape of schedule.yaml, reduced to what is used.
type schedule struct {
	Schedules []struct {
		Release       string `json:"release"`
		ReleaseDate   string `json:"releaseDate"`
		EndOfLifeDate string `json:"endOfLifeDate"`
	} `json:"schedules"`
}

// eol is the shape of eol.yaml.
type eol struct {
	Branches []struct {
		Release           string `json:"release"`
		EndOfLifeDate     string `json:"endOfLifeDate"`
		FinalPatchRelease string `json:"finalPatchRelease"`
	} `json:"branches"`
}

// release is one row of the generated table.
type release struct {
	Minor      string
	EndOfLife  string
	FinalPatch string
	// Year, Month and Day are the end-of-life date as literals.
	//
	// Emitted as integers rather than by slicing the date string in the
	// template: "08" and "09" are not valid Go integer literals, so a release
	// ending in August or September would produce a file that does not
	// compile — twice a year, silently, in a generator nobody runs often.
	Year, Month, Day int
	// sort keys, not rendered
	major, minor int
}

func main() {
	out := flag.String("out", "app/domain/release_schedule.go", "file to write")
	flag.Parse()

	releases, err := collect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "releasegen: %v\n", err)
		os.Exit(1)
	}
	if len(releases) == 0 {
		fmt.Fprintln(os.Stderr, "releasegen: the upstream schedule produced no releases")
		os.Exit(1)
	}

	source, err := render(releases)
	if err != nil {
		fmt.Fprintf(os.Stderr, "releasegen: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(*out, source, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "releasegen: writing %s: %v\n", *out, err)
		os.Exit(1)
	}
	fmt.Printf("releasegen: wrote %d releases to %s\n", len(releases), *out)
}

// collect fetches both files and merges them, newest first.
func collect() ([]release, error) {
	var current schedule
	if err := fetchYAML(scheduleURL, &current); err != nil {
		return nil, err
	}
	var past eol
	if err := fetchYAML(eolURL, &past); err != nil {
		return nil, err
	}

	byMinor := make(map[string]release, 16)
	for _, entry := range current.Schedules {
		if entry.Release == "" || entry.EndOfLifeDate == "" {
			continue
		}
		byMinor[entry.Release] = release{Minor: entry.Release, EndOfLife: entry.EndOfLifeDate}
	}
	// The end-of-life file wins where both mention a release: it records what
	// actually happened, where the schedule records what was planned.
	for _, entry := range past.Branches {
		if entry.Release == "" || entry.EndOfLifeDate == "" {
			continue
		}
		byMinor[entry.Release] = release{
			Minor:      entry.Release,
			EndOfLife:  entry.EndOfLifeDate,
			FinalPatch: entry.FinalPatchRelease,
		}
	}

	releases := make([]release, 0, len(byMinor))
	for _, entry := range byMinor {
		major, minor, ok := parseMinor(entry.Minor)
		if !ok {
			continue
		}
		ends, err := time.Parse(time.DateOnly, entry.EndOfLife)
		if err != nil {
			return nil, fmt.Errorf("release %s has an unparseable end of life %q", entry.Minor, entry.EndOfLife)
		}
		entry.Year, entry.Month, entry.Day = ends.Year(), int(ends.Month()), ends.Day()
		entry.major, entry.minor = major, minor
		releases = append(releases, entry)
	}

	sort.Slice(releases, func(i, j int) bool {
		if releases[i].major != releases[j].major {
			return releases[i].major > releases[j].major
		}
		return releases[i].minor > releases[j].minor
	})
	return releases, nil
}

func fetchYAML(url string, into any) error {
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("fetching %s: %w", url, err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("fetching %s: %s", url, response.Status)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("reading %s: %w", url, err)
	}
	if err := yaml.Unmarshal(body, into); err != nil {
		return fmt.Errorf("parsing %s: %w", url, err)
	}
	return nil
}

func parseMinor(version string) (int, int, bool) {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

func render(releases []release) ([]byte, error) {
	var buffer strings.Builder
	now := time.Now().UTC()
	data := struct {
		Year, Month, Day int
		Releases         []release
		ScheduleURL,
		EOLURL string
	}{
		// Date only. A timestamp to the second would make every regeneration
		// a diff even when no release moved.
		Year:        now.Year(),
		Month:       int(now.Month()),
		Day:         now.Day(),
		Releases:    releases,
		ScheduleURL: scheduleURL,
		EOLURL:      eolURL,
	}
	if err := generated.Execute(&buffer, data); err != nil {
		return nil, fmt.Errorf("rendering: %w", err)
	}

	source, err := format.Source([]byte(buffer.String()))
	if err != nil {
		return nil, fmt.Errorf("formatting: %w", err)
	}
	return source, nil
}

var generated = template.Must(template.New("schedule").Parse(`// Code generated by tools/releasegen. DO NOT EDIT.
//
// Source, both maintained by the Kubernetes release team:
//   {{ .ScheduleURL }}
//   {{ .EOLURL }}
//
// Regenerate with: make releases

package domain

import "time"

// scheduleCompiledAt is when this table was generated.
//
// Carried so the application can say how old its answer is rather than
// implying it is current. A build from a year ago knows nothing about releases
// made since it, and the difference between "not in the table" and "does not
// exist" is the whole reason an unknown version is reported as unknown.
var scheduleCompiledAt = time.Date({{ .Year }}, {{ .Month }}, {{ .Day }}, 0, 0, 0, 0, time.UTC)

// endOfLife maps a Kubernetes minor version to the day its patches stop.
var endOfLife = map[string]time.Time{
{{- range .Releases }}
	{{ printf "%q" .Minor }}: time.Date({{ .Year }}, {{ .Month }}, {{ .Day }}, 0, 0, 0, 0, time.UTC),{{ if .FinalPatch }} // final patch {{ .FinalPatch }}{{ end }}
{{- end }}
}
`))
