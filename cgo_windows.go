//go:build windows

package main

/*
Windows links this binary EXTERNALLY, and an external link needs runtime/cgo.

Nothing here calls C, and the Windows backend is pure Go. But Go builds
windows/amd64 as a position-independent executable, the resource object the
build generates for the icon and version metadata cannot be read by the
internal linker, and both of those force the external linker. The external
linker then emits an export table referencing `_cgo_stub_export`, a symbol
defined in runtime/cgo — a package the toolchain links only when something
imports "C". A pure-Go program therefore fails to link with an error naming
a symbol nobody asked for.

The alternative was building without position independence, and that is a
security regression rather than a workaround: the shipped v0.2.0 binary
carries DYNAMIC_BASE, HIGH_ENTROPY_VA and NX_COMPAT, and dropping to
-buildmode=exe would hand Windows users a weaker binary than the release it
replaces. This file is one import and no code, and it keeps all three.

Under v2 the framework's CLI arranged the same thing on our behalf.
*/
import "C"
