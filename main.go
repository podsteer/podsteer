// Command podsteer is a fast, native desktop client for Kubernetes.
//
// This file is a shim and contains no application logic. The real entry point,
// including all dependency wiring, is app/cmd/main.go.
//
// IT IS AT THE ROOT BY INHERITANCE, NOT BY REQUIREMENT. The Wails v2 CLI
// compiled the package in the repository root — `wails build` invoked
// `go build` with its working directory set here and no package argument, and
// offered no configuration to point it elsewhere — so the `main` package had to
// live here even though every other line of Go in this project lives under
// app/. Wails v3 builds nothing: `make build` is a plain `go build` and
// `wails3 generate bindings` takes an explicit package pattern. Moving the
// entry point under app/ is therefore possible now, and is a change of its
// own — it means making app/cmd `package main` and repointing the Makefile and
// CI at it. Until somebody does that, this file is why the layout looks the
// way it does.
package main

import "github.com/podsteer/podsteer/app/cmd"

func main() {
	cmd.Main()
}
