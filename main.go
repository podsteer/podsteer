// Command k8sense is a fast, native desktop client for Kubernetes.
//
// This file is a shim and contains no application logic. The Wails CLI
// compiles the package in the repository root — `wails build` invokes
// `go build` with its working directory set here and no package argument, and
// offers no configuration to point it elsewhere — so the `main` package must
// live at the root even though every other line of Go in this project lives
// under app/.
//
// The real entry point, including all dependency wiring, is app/cmd/main.go.
package main

import "k8sense/app/cmd"

func main() {
	cmd.Main()
}
