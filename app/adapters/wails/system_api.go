package wails

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

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
	// A field rather than a direct call to the Wails save dialog, so a
	// test can stub the chosen path instead of popping a real dialog — which
	// would hang `go test` waiting for an operator who is not there.
	chooseSavePath func(suggestedName string) (string, error)

	// chooseDirectory and chooseFile are the open-directory and open-file
	// dialogs behind ChooseDirectory and ChooseFile, seams for the same
	// reason chooseSavePath is one.
	chooseDirectory func(title string) (string, error)
	chooseFile      func(title string) (string, error)

	// chooseTextPath is the open dialog behind ReadTextFile — filtered to the
	// documents PodSteer imports, where chooseFile is deliberately unfiltered
	// because anything at all can be copied into a container.
	chooseTextPath func(title string) (string, error)
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
	s.chooseDirectory = s.showDirectoryDialog
	s.chooseFile = s.showOpenDialog
	s.chooseTextPath = s.showTextOpenDialog
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

// saveDialogFor describes the save dialog for one suggested filename.
//
// DERIVED FROM THE EXTENSION rather than fixed, because SaveTextFile now
// writes three different things and a dialog restricted to CSV would have
// appended `.csv` to the other two — macOS's save panel treats a filter as
// the extension it will enforce, so a settings document would have arrived as
// `podsteer-settings-….json.csv` and failed to import. The log download was
// already going out through the CSV filter for the same reason; this fixes
// that at the same time as making room for the settings file.
//
// Anything unrecognised gets an unrestricted dialog rather than a guess: the
// operator named the file, and second-guessing them costs more than the tidy
// filter is worth.
func saveDialogFor(suggestedName string) (title string, filters []application.FileFilter) {
	switch strings.ToLower(filepath.Ext(suggestedName)) {
	case ".csv":
		return "Export CSV", []application.FileFilter{
			{DisplayName: "CSV (*.csv)", Pattern: "*.csv"},
		}
	case ".json":
		return "Export", []application.FileFilter{
			{DisplayName: "JSON (*.json)", Pattern: "*.json"},
		}
	case ".log":
		return "Download logs", []application.FileFilter{
			{DisplayName: "Log (*.log)", Pattern: "*.log"},
			{DisplayName: "All files", Pattern: "*"},
		}
	default:
		return "Save", nil
	}
}

// showSaveDialog is chooseSavePath's real implementation: the native save
// dialog, seeded with the suggested filename and filtered by its extension.
func (s *SystemAPI) showSaveDialog(suggestedName string) (string, error) {
	wailsApp, ok := s.app.wailsApp()
	if !ok {
		return "", fmt.Errorf("the window is not running")
	}

	title, filters := saveDialogFor(suggestedName)

	return wailsApp.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{
		Title:    title,
		Filename: suggestedName,
		Filters:  filters,
	}).PromptForSingleSelection()
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

// showDirectoryDialog is chooseDirectory's real implementation: the native
// folder picker, allowed to create a folder on the way, because "a new
// folder for this download" is the commonest answer to the question.
func (s *SystemAPI) showDirectoryDialog(title string) (string, error) {
	wailsApp, ok := s.app.wailsApp()
	if !ok {
		return "", fmt.Errorf("the window is not running")
	}

	// Wails v3 has ONE open dialog where v2 had two functions, so which of
	// the two this is comes from the flags rather than from the name. Both
	// are spelled out: leaving CanChooseFiles unset is what keeps this a
	// folder picker, and an unset flag is easy to read as an oversight.
	return wailsApp.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		Title:                title,
		CanChooseDirectories: true,
		CanChooseFiles:       false,
		CanCreateDirectories: true,
	}).PromptForSingleSelection()
}

// showOpenDialog is chooseFile's real implementation: the native file
// picker, unfiltered, because anything can be copied into a container.
func (s *SystemAPI) showOpenDialog(title string) (string, error) {
	wailsApp, ok := s.app.wailsApp()
	if !ok {
		return "", fmt.Errorf("the window is not running")
	}

	return wailsApp.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		Title:          title,
		CanChooseFiles: true,
	}).PromptForSingleSelection()
}

// showTextOpenDialog is chooseTextPath's real implementation: the native file
// picker, filtered to JSON but offering everything, because an operator who
// renamed their settings file should not be told it does not exist.
func (s *SystemAPI) showTextOpenDialog(title string) (string, error) {
	wailsApp, ok := s.app.wailsApp()
	if !ok {
		return "", fmt.Errorf("the window is not running")
	}

	return wailsApp.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		Title:          title,
		CanChooseFiles: true,
		Filters: []application.FileFilter{
			{DisplayName: "JSON (*.json)", Pattern: "*.json"},
			{DisplayName: "All files", Pattern: "*"},
		},
	}).PromptForSingleSelection()
}

// maxTextFileBytes caps what ReadTextFile will hand the webview.
//
// A settings document is a few kilobytes; a megabyte is already two orders
// past anything PodSteer writes. The cap is not a security boundary — the
// operator picked the file — it is a refusal to marshal an arbitrarily large
// string across the bridge and into a JSON parser running in the UI thread,
// which is how "I chose the wrong file" becomes "the window froze".
const maxTextFileBytes = 1 << 20

// ReadTextFile opens a native file picker and returns what the chosen file
// contains.
//
// The file is read HERE rather than handed to the frontend as a path, for the
// same reason ReadKubeconfigFile is: the webview cannot open files and should
// not be able to. That is also why this exists rather than ChooseFile being
// reused — ChooseFile returns a PATH, which is only ever useful to a Go method
// that will act on it, and nothing in the webview can turn one into content.
//
// An empty returned string means the operator cancelled, which is not an
// error — the same convention as ChooseDirectory and ReadKubeconfigFile. An
// empty FILE is refused instead of being returned as a cancellation, because
// the two would otherwise be indistinguishable to the caller.
func (s *SystemAPI) ReadTextFile(title string) (string, error) {
	if strings.TrimSpace(title) == "" {
		title = "Choose a file"
	}

	path, err := s.chooseTextPath(title)
	if err != nil {
		return "", apiError(s.logger, "ReadTextFile", err)
	}
	if path == "" {
		return "", nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", apiError(s.logger, "ReadTextFile", err)
	}
	if info.Size() > maxTextFileBytes {
		return "", apiError(s.logger, "ReadTextFile", fmt.Errorf(
			"%w: that file is %d bytes; PodSteer reads at most %d",
			errUnreadableTextFile, info.Size(), maxTextFileBytes))
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", apiError(s.logger, "ReadTextFile", err)
	}
	if len(content) == 0 {
		return "", apiError(s.logger, "ReadTextFile", fmt.Errorf("%w: that file is empty",
			errUnreadableTextFile))
	}

	// The PATH is not logged, and neither is the content. What was chosen is
	// the operator's business and the reason for it is on screen in front of
	// them; SECURITY.md's file-transfer rule — one line naming what moved,
	// never a local path — is the same rule.
	s.logger.Debug("read a text file", slog.Int("bytes", len(content)))

	return string(content), nil
}

// ChooseDirectory opens the native folder picker and returns the operator's
// choice — the destination of a download, or a folder to upload.
//
// The path is returned to the frontend rather than acted on here, unlike
// SaveTextFile, because the thing that will use it — FileCopyAPI — is a
// transfer the operator has yet to start and may still change their mind
// about. Handing a path back is safe in this direction: the frontend can
// only ever pass it to a Go method that checks it is a directory and writes
// nothing but what the container sent through the ArchivePort's rules. An
// empty path means the operator cancelled, which is not an error.
func (s *SystemAPI) ChooseDirectory(title string) (string, error) {
	if strings.TrimSpace(title) == "" {
		title = "Choose a folder"
	}

	path, err := s.chooseDirectory(title)
	if err != nil {
		return "", apiError(s.logger, "ChooseDirectory", err)
	}
	return path, nil
}

// ChooseFile opens the native file picker and returns the operator's
// choice — a file to upload into a container. The same conventions as
// ChooseDirectory: the path is only ever consumed by FileCopyAPI, which
// reads it through the ArchivePort, and "" means cancelled.
func (s *SystemAPI) ChooseFile(title string) (string, error) {
	if strings.TrimSpace(title) == "" {
		title = "Choose a file"
	}

	path, err := s.chooseFile(title)
	if err != nil {
		return "", apiError(s.logger, "ChooseFile", err)
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

	wailsApp, ok := s.app.wailsApp()
	if !ok {
		return apiError(s.logger, "OpenURL", fmt.Errorf("%w: the window is not running",
			errInvalidURL))
	}

	// Wails v3 reports whether the OS accepted the hand-off; v2's
	// BrowserOpenURL returned nothing at all. The refusal is surfaced rather
	// than dropped — a link that silently does nothing is indistinguishable
	// from one this method rejected, and the two need different fixes.
	if err := wailsApp.Browser.OpenURL(parsed.String()); err != nil {
		return apiError(s.logger, "OpenURL", err)
	}
	return nil
}
