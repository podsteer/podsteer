package safego

import (
	"strings"
	"sync"
	"testing"
)

func TestRecoverContainsAPanic(t *testing.T) {
	tests := []struct {
		name string
		body func()
	}{
		{"nil dereference", func() { var p *int; _ = *p }},
		{"explicit panic", func() { panic("boom") }},
		{"error value", func() { panic(strings.NewReader("")) }},
		{"no panic", func() {}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer Recover("test")
				tt.body()
			}()
			wg.Wait() // reaching here means the process survived
		})
	}
}

func TestRunReportsWhetherItPanicked(t *testing.T) {
	if !Run("test", func() { panic("x") }) {
		t.Error("Run() = false after a panic")
	}
	if Run("test", func() {}) {
		t.Error("Run() = true without a panic")
	}
}

func TestErrorCarriesThePanic(t *testing.T) {
	err := Error("read", "boom")
	if err == nil || !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "read") {
		t.Fatalf("Error() = %v", err)
	}
}
