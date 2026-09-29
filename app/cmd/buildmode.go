//go:build !server

package cmd

// serverBuild is true only in Wails' server mode (the `server` build tag).
//
// It exists for one reason: server mode raises no platform lifecycle events,
// so nothing hung off ApplicationStarted ever runs there — and the history
// sampler is hung off exactly that. Server mode is how the website's
// screenshots are taken, and without this the overview's Trend panel sat on
// "Collecting" in every one of them.
const serverBuild = false
