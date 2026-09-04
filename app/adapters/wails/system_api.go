package wails

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"runtime"
	"strings"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/podsteer/podsteer/app/adapters/notices"
)

// AppInfo describes the running application to the UI.
//
// It exists so the status bar can show a version without the frontend
// hard-coding one — a hard-coded version is a version that is wrong the moment
// somebody cuts a release.
type AppInfo struct {
	// Name is the short application name.
	Name string `json:"name"`
	// Version is the release version, set at build time via -ldflags.
	Version string `json:"version"`
	// Platform is the OS and architecture the binary was built for.
	Platform string `json:"platform"`
	// Website is the project's home page.
	Website string `json:"website"`
}

// website is the project's home page, offered from the status bar.
const website = "https://podsteer.com"

// SystemAPI exposes application-level facts and shell integration.
type SystemAPI struct {
	info   AppInfo
	app    *App
	logger *slog.Logger

	// chooseSavePath opens the native save dialog and returns the operator's
	// choice, or "" if they cancelled.
	//
	// A field rather than a direct call to wailsruntime.SaveFileDialog, so a
	// test can stub the chosen path instead of popping a real dialog — which
	// would hang `go test` waiting for an operator who is not there.
	chooseSavePath func(suggestedName string) (string, error)
}

// NewSystemAPI returns the bound system API.
func NewSystemAPI(name, version string, app *App, logger *slog.Logger) (*SystemAPI, error) {
	if app == nil {
		return nil, fmt.Errorf("wails: SystemAPI requires an App")
	}
	if logger == nil {
		logger = slog.Default()
	}

	s := &SystemAPI{
		info: AppInfo{
			Name:     name,
			Version:  version,
			Platform: runtime.GOOS + "/" + runtime.GOARCH,
			Website:  website,
		},
		app:    app,
		logger: logger.With(slog.String("api", "system")),
	}
	s.chooseSavePath = s.showSaveDialog
	return s, nil
}

// Info returns the running application's identity.
func (s *SystemAPI) Info() AppInfo {
	return s.info
}

// Credit is one shipped dependency, as the Credits pane shows it.
type Credit struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Ecosystem string `json:"ecosystem"`
	Licence   string `json:"licence"`
	Copyright string `json:"copyright"`
	// TextID keys into the licence texts returned by LicenceText. Empty when
	// the project publishes no licence file.
	TextID string `json:"textId"`
	// NoticeTextID keys into the same texts, for the NOTICE that Apache-2.0
	// section 4(d) requires to be reproduced alongside the licence.
	NoticeTextID string `json:"noticeTextId"`
	// Expression is the original SPDX expression when a package offers a
	// choice and one arm was elected, so the pane never silently asserts one
	// licence for something dual-licensed.
	Expression string `json:"expression"`
}

// Credits returns every dependency PodSteer ships, with its licence.
//
// Not decoration: MIT, BSD, ISC and Apache-2.0 all require the licence and its
// copyright notice to be distributed with the binary, and a desktop
// application has nowhere else to put them. The inventory is generated from
// what actually ships and embedded at build time, so it cannot drift away from
// the dependencies it describes.
func (s *SystemAPI) Credits() ([]Credit, error) {
	packages, err := notices.Packages()
	if err != nil {
		return nil, apiError(s.logger, "Credits", err)
	}

	credits := make([]Credit, 0, len(packages))
	for _, entry := range packages {
		credits = append(credits, Credit{
			Name:         entry.Name,
			Version:      entry.Version,
			Ecosystem:    entry.Ecosystem,
			Licence:      entry.Licence,
			Copyright:    entry.Copyright,
			TextID:       entry.TextID,
			NoticeTextID: entry.NoticeTextID,
			Expression:   entry.Expression,
		})
	}
	return credits, nil
}

// LicenceText returns one licence's full text.
//
// Fetched on demand rather than sent with the list: the texts total far more
// than the summary does, and nobody reads more than one at a time.
func (s *SystemAPI) LicenceText(textID string) (string, error) {
	text, ok := notices.Text(textID)
	if !ok {
		return "", apiError(s.logger, "LicenceText", fmt.Errorf("%w: no licence text %q",
			errNotFound, textID))
	}
	return text, nil
}

// showSaveDialog is chooseSavePath's real implementation: the native save
// dialog, seeded with the suggested filename and restricted to CSV.
func (s *SystemAPI) showSaveDialog(suggestedName string) (string, error) {
	ctx, ok := s.app.runtimeContext()
	if !ok {
		return "", fmt.Errorf("the window is not running")
	}

	return wailsruntime.SaveFileDialog(ctx, wailsruntime.SaveDialogOptions{
		Title:           "Export CSV",
		DefaultFilename: suggestedName,
		Filters: []wailsruntime.FileFilter{
			{DisplayName: "CSV (*.csv)", Pattern: "*.csv"},
		},
	})
}

// SaveTextFile opens a native save dialog seeded with suggestedName and
// writes content to wherever the operator chose.
//
// This is the one place PodSteer writes a file the OPERATOR picked the
// location for, rather than one of the fixed per-user paths — history and
// display preferences — everything else in SECURITY.md enumerates. The write
// happens here, in Go, because the webview cannot touch the filesystem and
// should not be able to: handing the frontend a path instead would mean
// trusting whatever content it sent, unauthenticated, to land wherever it
// said.
//
// An empty returned path means the operator cancelled the dialog, which is
// not an error — see ReadKubeconfigFile for the same convention on the way
// in.
func (s *SystemAPI) SaveTextFile(suggestedName, content string) (string, error) {
	if strings.TrimSpace(suggestedName) == "" {
		return "", apiError(s.logger, "SaveTextFile", errEmptySuggestedName)
	}

	path, err := s.chooseSavePath(suggestedName)
	if err != nil {
		return "", apiError(s.logger, "SaveTextFile", err)
	}
	if path == "" {
		return "", nil
	}

	// 0o600: the export can hold whatever the cluster returned, including
	// values another local account has no business reading — the same reasoning
	// behind every other write in SECURITY.md's enumeration.
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", apiError(s.logger, "SaveTextFile", err)
	}

	return path, nil
}

// allowedURLSchemes are the only schemes OpenURL will hand to the OS.
//
// http/https cover ordinary links; mailto covers "share by email", which
// hands off to the operator's mail client the same way a browser link hands
// off to their browser — both are the OS's job, never the webview's.
var allowedURLSchemes = map[string]bool{
	"http":   true,
	"https":  true,
	"mailto": true,
}

// OpenURL opens an address in the operator's default browser or mail client.
//
// The webview must never navigate away from the bundled application: doing so
// would replace the UI with a web page and leave no way back, since there is
// no address bar. Handing the URL to the OS is the only correct way to follow
// a link from a desktop app.
//
// Only the schemes in allowedURLSchemes are accepted. Without that check a
// crafted "file://" or "javascript:" URL reaching this method would ask the
// shell to open something it should not — and this method is callable from
// the webview.
func (s *SystemAPI) OpenURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return apiError(s.logger, "OpenURL", fmt.Errorf("%w: %q is not a URL",
			errInvalidURL, raw))
	}

	if !allowedURLSchemes[parsed.Scheme] {
		return apiError(s.logger, "OpenURL", fmt.Errorf("%w: %q is not an allowed URL scheme",
			errInvalidURL, raw))
	}

	ctx, ok := s.app.runtimeContext()
	if !ok {
		return apiError(s.logger, "OpenURL", fmt.Errorf("%w: the window is not running",
			errInvalidURL))
	}

	wailsruntime.BrowserOpenURL(ctx, parsed.String())
	return nil
}
