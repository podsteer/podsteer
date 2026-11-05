package wails

import (
	"slices"
	"testing"
)

// SetBackgroundRefresh only carries the frontend's choice to the platform
// switch the composition root installed; the switch itself is native and is
// not exercised here.

func TestSetBackgroundRefreshCallsTheInstalledSwitch(t *testing.T) {
	t.Parallel()

	api := newTestSystemAPI(t)
	var got []bool
	api.app.SetBackgroundRunner(func(enabled bool) { got = append(got, enabled) })

	api.SetBackgroundRefresh(false)
	api.SetBackgroundRefresh(true)

	if want := []bool{false, true}; !slices.Equal(got, want) {
		t.Fatalf("switch calls = %v, want %v", got, want)
	}
}

func TestSetBackgroundRefreshWithoutASwitchDoesNothing(t *testing.T) {
	t.Parallel()

	// Every platform but macOS: nothing to install, and the call must still
	// be safe, because the frontend makes it on every boot.
	newTestSystemAPI(t).SetBackgroundRefresh(true)
}
