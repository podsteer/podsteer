# Dependency Licence Policy

PodSteer is distributed as a compiled desktop binary, is licensed Apache-2.0,
and is intended to support a commercial edition. Those three facts decide
everything below.

This document is the reasoning. The machine-readable half is
[`build/licence-policy.json`](../build/licence-policy.json), enforced by
`make notices` and by CI on every pull request. Neither is authoritative alone:
the file cannot explain itself, and this page cannot stop a build.

## The decision, in one paragraph

Every dependency PodSteer **distributes** must be under a permissive licence.
Copyleft is not accepted in the shipped binary at any tier, and no exception
process exists for it — not because copyleft is bad, but because a desktop
binary statically links everything it uses, so the reciprocal obligations of
GPL or AGPL would attach to PodSteer itself. That is a decision about the
product's licensing, not about a dependency, and it is not one to discover
during a release.

Build-time tooling is judged separately and more leniently, because it is
never distributed and therefore triggers no obligation at all.

## Why static linking is the crux

Go links statically. A Go binary is a single artefact containing the machine
code of every module it imports, with no dynamic loading and no separable
library boundary. This removes the argument that keeps LGPL workable in other
ecosystems — "we only link dynamically, the user can replace the library" —
because here there is nothing to replace.

The practical consequence: for Go dependencies, *shipped* and *linked into our
binary* are the same statement. The npm side is the same in effect, since the
frontend is bundled into one JavaScript file and embedded in that binary.

## Tiers

### Allowed

`MIT` · `Apache-2.0` · `BSD-2-Clause` · `BSD-3-Clause` · `ISC` · `0BSD` ·
`Zlib` · `BlueOak-1.0.0` · `Unicode-3.0` · `Unicode-DFS-2016` · `CC0-1.0` ·
`Python-2.0`

Permissive, no reciprocal obligation, no restriction on commercial use or
field of use. All of them except `0BSD` and `CC0-1.0` still require the licence
and its copyright notice to travel with the binary — which is not optional and
is why the Credits pane exists.

`Apache-2.0` additionally carries an explicit patent grant, which is a reason
to *prefer* it over MIT for a commercial product, and a NOTICE duty (§4(d))
that the generator captures separately from the licence text.

### Review required

`MPL-2.0` · `MPL-1.1` · `EPL-1.0` · `EPL-2.0` · `CDDL-1.0` · `CDDL-1.1` ·
`LGPL-2.1` · `LGPL-3.0` · `BSD-4-Clause` · `Unlicense` · `WTFPL` ·
`CC-BY-3.0` · `CC-BY-4.0` · `Artistic-2.0` · `OFL-1.1` · `UNKNOWN`

These are not refused; they are refused **by default**. Each needs a recorded
exception explaining why it is acceptable in the specific way it is used.

- **Weak copyleft** (`MPL-2.0`, `EPL`, `CDDL`) is file-level: obligations
  attach to the covered files, not to the program they are combined with. It
  is usually acceptable *shipped*, provided we do not modify those files, and
  is unproblematic build-only. It still needs a decision, because "we do not
  modify it" is a commitment somebody has to keep.
- **`LGPL`** is the case static linking ruins, as above. Shipped, treat it as
  effectively copyleft.
- **`BSD-4-Clause`** carries the advertising clause — every promotional
  mention must credit the authors. That is a marketing obligation nobody would
  remember, so it needs to be a conscious choice.
- **`Unlicense` / `WTFPL` / `CC0-1.0`** are public-domain dedications of
  varying legal robustness. `CC0-1.0` is allowed above because it is drafted
  as a licence with a fallback grant; the other two are not, and some
  jurisdictions do not recognise a bare dedication.
- **`CC-BY` and `OFL`** are content licences, not software licences. Finding
  one on a code dependency usually means something is mislabelled.
- **`UNKNOWN`** is first-class here on purpose — see below.

### Forbidden

`GPL-2.0` · `GPL-3.0` · `AGPL-3.0` · `SSPL-1.0` · `BUSL-1.1` · `Elastic-2.0` ·
`Commons-Clause` · `CC-BY-NC-4.0` · `JSON` · `EUPL-1.2`

No exception process. Strong copyleft would extend to PodSteer through static
linking; `SSPL`, `BUSL`, `Elastic` and `Commons-Clause` restrict commercial or
service use directly; `CC-BY-NC` forbids commercial use outright; the `JSON`
licence's "shall be used for Good, not Evil" clause is unenforceably vague and
is rejected by most corporate legal reviews, which makes it a procurement
problem regardless of its intent.

Note that `GPL` and `LGPL` are downgraded to review-required **for build-time
tooling only** (`buildOverrides`), because a compiler we run is not a library
we ship.

## `UNKNOWN` is a tier, not an error

A dependency whose licence cannot be established is treated exactly like a
weak-copyleft one: it stops the build until a person records what it actually
is. This matters more than it sounds. The failure mode of a licence scanner is
not misclassifying GPL as MIT — it is finding no licence file and quietly
omitting the package. Making `UNKNOWN` a first-class, blocking classification
is what turns that silence into a question.

The same logic covers **disagreement**: when a package's declared `license`
field and its actual licence text classify differently, the result is
`UNKNOWN` rather than whichever is friendlier. A package declaring MIT while
shipping GPL text is either mislabelled or relicensed, and both need a human.

## Shipped versus build-only

The distinction carries real weight, so it is computed rather than asserted.

**Go** — the union of `go list -deps` across all three release platforms
(`darwin/arm64`, `windows/amd64`, `linux/amd64`), with `CGO_ENABLED=1` forced.
Running it once on the host is a trap that has already caught us: three
modules — under Wails v3 the Windows toast stack, `go-toast`, `go-ole` and
`go-colorable` — are reached only under `GOOS=windows`, so a macOS-generated
inventory omitted modules the Windows binary actually contains.

**npm** — the `--omit=dev` tree, **cross-checked against what `web/src`
actually imports**. This is the only silent way shipped scope goes wrong: a
package imported by shipped source but declared as a `devDependency` is
bundled into the app while being absent from every inventory built from
`dependencies`. That has also already happened here, to CodeMirror, xterm.js
and Svelte. The check now fails the build.

### Amendment, 2026-09-05: build scope is membership, not cache presence

**Build scope is now `go list -deps -test` membership across the release
platforms, minus the shipped set.** A Go module is build-only when some build
or test on `darwin/arm64`, `windows/amd64` or `linux/amd64` compiles it and no
platform's binary links it. Shipped scope is unchanged, and so are the
inventory and the SBOM: they are still built from the same `go list -deps`
union, and this amendment landed with no diff to
[`app/adapters/notices/notices.json`](../app/adapters/notices/notices.json).

Modules that appear in `go list -m all` and that neither listing reaches are
**graph-only**: reported by count and **not classified**. They are
requirements of our requirements that nothing here imports — a fact about
somebody else's `go.mod` — so recording a licence verdict on them would
describe an inventory PodSteer does not distribute. Anything that *is*
classified must be present in the module cache, and its absence is now an
error naming `go mod download` rather than a silent reclassification.

**Why the previous test was wrong.** Build scope used to mean "present in the
local Go module cache". That asks whether a machine happened to download
something, which is not the same question as whether it participates. It was
machine-dependent in both directions: on the machine this was measured on, 133
of 210 graph modules were called build-scope purely because they had been
downloaded, while on a cold cache a module that genuinely does participate
would have been dropped from the inventory instead of classified. CI made this
concrete rather than theoretical — the `setup-go` cache is keyed on `go.sum`
and shared between the quality, bindings and packaging jobs, so whichever job
last wrote it decided the verdict, and the gate flipped between red and green
without a line of the repository changing. The 2026-08-20 decision said scope
is *computed, never asserted*; cache presence asserted it.

The module that forced the issue is `github.com/konoui/go-qsort`, which
publishes no licence file, appears in zero builds, and is in the cache only
because `go install .../cmd/wails3` pulled it in. A narrow exception was
rejected: an exception matching nothing fails as stale, so on a cold cache it
would have gone red for the opposite reason, and it would have recorded the
module in a scope it does not occupy — which is the one thing an auditor reads
an exception for.

**Test-only modules are build scope, not a fourth scope.** They are never
distributed, so they carry the same obligations a compiler does.

**The toolchain is outside the inventory entirely.** The `wails3` CLI, Node,
`gcc` and the rest are tools PodSteer is built *with*, not dependencies it is
built *from*; nothing obliges you to credit them, and they are not listed.
That is why the CLI's own requirements are not ours even when they land in the
same module graph — `wails3` ships from the same module as the Wails library,
so its dependencies arrive in `go list -m all` without ever entering a build.

## Exceptions

An exception is a record in `build/licence-policy.json` with:

| Field | Meaning |
| :--- | :--- |
| `package` | Name, with an optional single trailing `*` |
| `ecosystem` | `go` or `npm` |
| `licence` | The identifier being excused — an exception is licence-specific |
| `scope` | `build` or `shipped`; a `build` exception never excuses a shipped package |
| `justification` | Why this is acceptable **for this use**, in prose |
| `approvedBy`, `approvedOn` | Who decided, and when |
| `reviewBy` | When the reasoning must be re-examined |

Three properties keep them honest:

1. **Stale exceptions fail the build.** One that matches no current dependency
   is deleted, because an exception nobody needs is an exception nobody
   reviews.
2. **A broken premise fails the build.** If a `build`-scoped exception's
   package later appears in the shipped tree, its justification — "this is
   never distributed" — is no longer true, and the build says so by name.
3. **An expired `reviewBy` warns locally and fails in CI.** Building on your
   own machine should not stop; shipping should.

## Current exceptions

Two, both for `lightningcss` (Tailwind v4's CSS engine) and its
platform-specific native binary, both `MPL-2.0`, both `build`-scoped. They
transform our CSS at build time and contribute no MPL-covered code to the
bundle; MPL-2.0's obligations attach to distributing covered code, which does
not occur. The cross-check above is what stops that reasoning going stale.

They were kept rather than eliminated because the alternative is abandoning
Tailwind v4, and the obligation being avoided is nil. Should they ever become
shipped, the build fails until somebody re-argues it.

## A package that ships no licence text

MIT and BSD both require the notice to be **reproduced** in distributions. A
package that omits the file from its published artefact does not thereby remove
that duty — it only means the text has to come from wherever the project
actually keeps it.

`app/adapters/notices/notices_test.go` fails the build on any shipped package
with no licence text, so this cannot pass unnoticed. There are two outcomes:

- **The notice genuinely does not exist.** Some small packages declare a
  licence in `package.json` and publish no file anywhere. Reproducing a notice
  that was never written is impossible, and recording that is the honest
  alternative — those are named in the test.
- **The notice exists, elsewhere in the same project.** A monorepo publishing
  a dozen packages under one licence, where some tarballs include the file and
  some do not. Here the obligation *can* be met, and
  [`build/licences/notice-sources.json`](../build/licences/notice-sources.json)
  is how: the entry names a **sibling package already in this inventory** whose
  licence file is the same project's, and the collector copies it across.

**It is a sibling, never prose somebody typed out.** The point is to reproduce
a notice we can point at, not to assert from memory what a licence probably
says. The collector fails rather than fudges if the sibling is absent from the
tree, if the sibling has no text either, or if the package has **since started
shipping its own** — that last one so an exception is removed when it stops
being true rather than quietly shadowing the real file.

Each entry carries a justification, an approver and a date, like any other
exception. The generated inventory records `noticeFrom`, so
**Settings → Credits** never implies a package shipped something it did not.

&nbsp;

## SBOM

`make sbom` emits CycloneDX 1.6 to `build/bin/sbom/podsteer.cdx.json`, and CI
attaches it to every GitHub Release. It shares its collector with the notices
generator, so the SBOM and the Credits pane describe provably the same set of
packages — two documents disagreeing about what ships would be worse than one.

This is increasingly a procurement requirement rather than a courtesy (the EU
Cyber Resilience Act; US Executive Order 14028), and publishing one per release
removes a question from every future enterprise conversation.

## Changing this policy

Edit both halves in the same change: this document for the reasoning, the JSON
for the enforcement. Adding an identifier to `allowed` is a decision about what
PodSteer may become, not a formality — the tiers above are the argument, and it
should be updated rather than merely appended to.
