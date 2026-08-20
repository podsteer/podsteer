package wails

import (
	"fmt"
	"log/slog"
	"net/url"
	"runtime"
	"strings"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"podsteer/app/adapters/notices"
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
}

// NewSystemAPI returns the bound system API.
func NewSystemAPI(name, version string, app *App, logger *slog.Logger) (*SystemAPI, error) {
	if app == nil {
		return nil, fmt.Errorf("wails: SystemAPI requires an App")
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &SystemAPI{
		info: AppInfo{
			Name:     name,
			Version:  version,
			Platform: runtime.GOOS + "/" + runtime.GOARCH,
			Website:  website,
		},
		app:    app,
		logger: logger.With(slog.String("api", "system")),
	}, nil
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
