//go:build server

package cmd

// serverBuild is true in Wails' server mode: the same backend and frontend,
// served over HTTP to a browser, with no native window. See buildmode.go.
const serverBuild = true
