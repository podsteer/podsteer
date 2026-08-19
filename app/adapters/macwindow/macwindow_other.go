//go:build !darwin

// Package macwindow adjusts native macOS window chrome that Wails has no
// public API for. See macwindow_darwin.go for what it actually does; this
// file is the no-op every other platform gets, so callers never need a
// runtime.GOOS check of their own.
package macwindow

// NudgeTrafficLights does nothing outside macOS — there are no traffic
// lights to nudge.
func NudgeTrafficLights(_ float64) {}
