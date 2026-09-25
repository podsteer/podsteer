//go:build windows

// Windows links this binary externally, and an external link needs
// runtime/cgo.
//
// Nothing here calls C, and the Windows backend is pure Go. But Go builds
// windows/amd64 as a position-independent executable, the resource object
// carrying the icon and version metadata cannot be read by the internal
// linker, and either one forces the external linker. That linker emits an
// export table referencing `_cgo_stub_export`, a symbol defined in
// runtime/cgo — a package the toolchain links only when something imports
// "C". So a pure-Go program fails to link on a symbol nobody asked for.
//
// The alternative was building without position independence, and that is a
// security regression rather than a workaround: the shipped v0.2.0 binary
// carries DYNAMIC_BASE, HIGH_ENTROPY_VA and NX_COMPAT, and dropping to
// -buildmode=exe would hand Windows users a weaker binary than the release
// it replaces. This file is one import and no code, and it keeps all three.
//
// Under v2 the framework's CLI arranged the same thing on our behalf.
//
// THE BLANK LINE BELOW IS LOAD-BEARING. A comment sitting directly above
// `import "C"` is the cgo preamble and is compiled as C. Without the gap,
// every sentence here becomes a syntax error from the C compiler — which is
// exactly how this file failed the first time it reached a Windows runner.
package main

import "C"
