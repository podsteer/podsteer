// Package config resolves the application's runtime configuration.
//
// Every setting has a working default, so PodSteer starts with no environment
// at all; the variables exist for troubleshooting and for pointing a build at
// a non-standard kubeconfig. Configuration is read once, at startup, in the
// composition root — no other package reads the environment.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// envPrefix namespaces every PodSteer environment variable.
const envPrefix = "PODSTEER_"

// Config is the fully resolved application configuration.
type Config struct {
	// App identifies the application to the OS and to clusters.
	App AppConfig
	// Window sizes the desktop window.
	Window WindowConfig
	// Kubernetes tunes cluster access.
	Kubernetes KubernetesConfig
	// Log controls diagnostics.
	Log LogConfig
}

// AppConfig identifies the application.
type AppConfig struct {
	// Name is the short application name, also sent as the API user agent.
	Name string
	// Title is the window title.
	Title string
	// Version is the release version, set at build time via -ldflags.
	Version string
}

// WindowConfig sizes the desktop window.
type WindowConfig struct {
	// Width and Height are the initial window dimensions.
	Width, Height int
	// MinWidth and MinHeight are the smallest usable dimensions. Below these
	// the pod table's columns collapse into each other.
	MinWidth, MinHeight int
}

// KubernetesConfig tunes cluster access.
type KubernetesConfig struct {
	// KubeconfigPath overrides the kubeconfig location. Empty means the
	// standard resolution order: $KUBECONFIG, then ~/.kube/config.
	KubeconfigPath string
	// QPS is the sustained request rate allowed per cluster.
	QPS float32
	// Burst is how far a momentary spike may exceed QPS.
	Burst int
	// RequestTimeout bounds a single call from the frontend.
	RequestTimeout time.Duration
}

// LogConfig controls diagnostics.
type LogConfig struct {
	// Level is the minimum level emitted.
	Level slog.Level
	// AddSource includes the source file and line in each record. Useful when
	// reproducing a bug, noisy otherwise.
	AddSource bool
}

// Default returns the configuration used when nothing is set.
func Default() Config {
	return Config{
		App: AppConfig{
			Name:    "podsteer",
			Title:   "PodSteer",
			Version: "dev",
		},
		Window: WindowConfig{
			Width:     1440,
			Height:    900,
			MinWidth:  960,
			MinHeight: 600,
		},
		Kubernetes: KubernetesConfig{
			QPS:            50,
			Burst:          100,
			RequestTimeout: 30 * time.Second,
		},
		Log: LogConfig{
			Level: slog.LevelInfo,
		},
	}
}

// Load returns the default configuration with environment overrides applied.
//
// A malformed value is an error rather than a silent fallback: someone who
// sets PODSTEER_QPS=fifty needs to be told, not quietly given 50.
func Load() (Config, error) {
	cfg := Default()

	if value, ok := lookup("KUBECONFIG"); ok {
		cfg.Kubernetes.KubeconfigPath = value
	}

	if value, ok := lookup("QPS"); ok {
		qps, err := strconv.ParseFloat(value, 32)
		if err != nil || qps <= 0 {
			return Config{}, fmt.Errorf("%sQPS: %q is not a positive number", envPrefix, value)
		}
		cfg.Kubernetes.QPS = float32(qps)
	}

	if value, ok := lookup("BURST"); ok {
		burst, err := strconv.Atoi(value)
		if err != nil || burst <= 0 {
			return Config{}, fmt.Errorf("%sBURST: %q is not a positive integer", envPrefix, value)
		}
		cfg.Kubernetes.Burst = burst
	}

	if value, ok := lookup("REQUEST_TIMEOUT"); ok {
		timeout, err := time.ParseDuration(value)
		if err != nil || timeout <= 0 {
			return Config{}, fmt.Errorf("%sREQUEST_TIMEOUT: %q is not a positive duration (e.g. 30s)",
				envPrefix, value)
		}
		cfg.Kubernetes.RequestTimeout = timeout
	}

	if value, ok := lookup("LOG_LEVEL"); ok {
		level, err := parseLevel(value)
		if err != nil {
			return Config{}, err
		}
		cfg.Log.Level = level
	}

	if value, ok := lookup("LOG_SOURCE"); ok {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("%sLOG_SOURCE: %q is not a boolean", envPrefix, value)
		}
		cfg.Log.AddSource = enabled
	}

	return cfg, nil
}

// lookup reads a prefixed environment variable, treating blank as unset.
func lookup(name string) (string, bool) {
	value, ok := os.LookupEnv(envPrefix + name)
	if !ok {
		return "", false
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	return value, true
}

// parseLevel maps a level name onto a slog.Level.
func parseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(value) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("%sLOG_LEVEL: %q is not one of debug, info, warn, error",
			envPrefix, value)
	}
}
