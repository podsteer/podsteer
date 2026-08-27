# Code signing and notarisation

What it takes for PodSteer to open on macOS and install on Windows with no
warning, what it costs, and in what order to do it.

**The ordering matters more than the effort does.** Almost none of this is work;
most of it is waiting for other organisations to verify who we are. Both
identity checks below can run in parallel, and both should be started before any
of the CI work — which is already written and waiting behind the secrets it
needs.

Status as of 2026-08-27:

| | State |
| :--- | :--- |
| D-U-N-S for Cloudresty Ltd | **Held.** Confirmed via Apple's lookup. |
| Apple Developer Program | Not enrolled. Nothing blocks starting it. |
| Azure identity validation | Not started. **Now the critical path**, at 1–20 business days. |
| Signing in CI | Written, inert until the secrets exist. |

Builds are still unsigned, so macOS reports "PodSteer is damaged" and the cask
ships an `xattr -dr com.apple.quarantine` caveat.

## What the warning actually is, on each platform

They are different problems and they are solved separately.

**macOS.** Gatekeeper refuses anything without a *Developer ID Application*
signature **and** an Apple notarisation ticket. Both are required — a signature
alone still warns. There is no reputation system and no gradual improvement: it
either passes or it does not, from the first download.

**Windows.** SmartScreen warns on binaries it has not seen enough of. A
signature attaches our identity to every build so reputation accrues to *us*
rather than to each new file hash. Note the honest caveat: **a signature does
not switch the warning off on day one.** Microsoft dropped the guarantee that an
EV certificate grants instant SmartScreen trust; reputation still builds with
download volume. Signing is what makes that possible at all, and what stops the
warning resetting on every release.

## macOS — Apple Developer Program

### 1. D-U-N-S number for Cloudresty Ltd — **already held**

Apple requires a D-U-N-S number for every organisation enrolment (only
government entities are exempt), and this was expected to be the longest pole
in the whole process — a request to Dun & Bradstreet can take weeks.

**It is not.** CLOUDRESTY LIMITED already has one, confirmed through
[Apple's own lookup](https://developer.apple.com/enroll/duns-lookup/) on
2026-08-27. Enrolment can start immediately, and Microsoft's identity
validation is now the critical path instead.

The number is deliberately not recorded here. It is retrievable from that
lookup in seconds by anyone who needs it, and a business identifier that
appears in a public repository is one more thing to keep accurate.

Enrol as **Cloudresty Ltd**, not as an individual. The trademark sits with
Cloudresty Ltd, and the certificate's subject is what every macOS user sees in
the Gatekeeper dialogue — an individual's personal name there reads as a hobby
project. Moving an app between Apple accounts later is painful, so this is worth
getting right once.

### 2. Enrol — $99/year

<https://developer.apple.com/programs/enroll/>

Requirements Apple checks, all of which Cloudresty already satisfies:

| Requirement | Notes |
| :--- | :--- |
| Legal entity able to contract | A UK Ltd qualifies. DBAs and trading names do not. |
| Legal binding authority | The person enrolling must be able to bind the company. |
| D-U-N-S number | Step 1. |
| Apple Account with two-factor | Use a company address, not a personal one. |
| Email on the company domain | e.g. an address at cloudresty.com. |
| A public, functional website | podsteer.com or cloudresty.com. A social page or a placeholder is rejected. |

### 3. Create the certificate and the API key

Once enrolled, in the Apple Developer portal:

1. Create a **Developer ID Application** certificate. *Not* "Mac Development"
   and *not* "Mac App Distribution" — those are for other distribution channels
   and will not satisfy Gatekeeper for a direct download.
2. Export it as a `.p12` with a strong password.
3. In App Store Connect, create an **API key** for the notary service. Prefer
   this over an Apple ID and app-specific password: it does not belong to a
   person, survives their password changes, and can be revoked on its own.

Repository secrets to add:

| Secret | What it is |
| :--- | :--- |
| `APPLE_CERTIFICATE_P12` | The `.p12`, base64-encoded |
| `APPLE_CERTIFICATE_PASSWORD` | Its export password |
| `APPLE_SIGNING_IDENTITY` | e.g. `Developer ID Application: Cloudresty Ltd (TEAMID)` |
| `APPLE_API_KEY_P8` | The App Store Connect key, base64-encoded |
| `APPLE_API_KEY_ID` | The key ID |
| `APPLE_API_ISSUER_ID` | The issuer ID |

### 4. What the build has to do

In order, after `wails build`:

```sh
codesign --force --timestamp --options runtime \
  --sign "$APPLE_SIGNING_IDENTITY" build/bin/PodSteer.app

# ditto, not zip: the notary service expects this container format.
ditto -c -k --keepParent build/bin/PodSteer.app notarize.zip

xcrun notarytool submit notarize.zip \
  --key key.p8 --key-id "$APPLE_API_KEY_ID" --issuer "$APPLE_API_ISSUER_ID" --wait

# Staples the ticket INTO the bundle, so Gatekeeper needs no network on first
# launch. Without this the app still passes — but only online, and a first
# launch on a plane fails.
xcrun stapler staple build/bin/PodSteer.app

# The gate. This is what a user's Mac will do when the app is double-clicked.
# -t execute, not -t install: `install` assesses an installer package, and
# against a .app it does not test the thing Gatekeeper actually does.
spctl -a -vvv -t execute build/bin/PodSteer.app
```

`--options runtime` is the hardened runtime and is **required** — the notary
service rejects anything without it, and without `--timestamp` the signature
expires with the certificate rather than outliving it.

### 5. Two things not to do

**No entitlements.** Hardened runtime needs none for a Wails application:
WKWebView runs JavaScriptCore out-of-process inside Apple's own signed
WebContent process, so the `allow-jit` and `allow-unsigned-executable-memory`
entitlements that Electron applications need do not apply here. An empty
entitlements set is a security property worth stating in `SECURITY.md`, not a
gap to fill.

**Never enable the App Sandbox.** It redirects `$HOME` into a container, which
hides `~/.kube/config` and blocks the exec credential plugins that every EKS,
GKE and AKS user depends on. It would break the application for most of its
users to satisfy a requirement that direct distribution does not impose.

### 6. The second reason to sign, beyond the warning

An ad-hoc signature makes the binary's designated requirement a bare `cdhash`,
which changes on **any** source edit. macOS TCC treats each rebuild as a
different application and re-prompts for file access every time. A Developer ID
requirement is stable across versions: prompted once, ever.

## Windows — Azure Trusted Signing

Use **Azure Trusted Signing** (rebranded *Azure Artifact Signing* in 2026), not
a traditional certificate from a CA.

Since June 2023 the CA/Browser Forum has required code-signing private keys to
live on FIPS-certified hardware, so a traditional certificate means a physical
USB token — which cannot be plugged into a GitHub-hosted runner. Working around
that means a self-hosted runner or a cloud HSM, both of which cost more than the
certificate. Trusted Signing is Microsoft's managed service, it signs from CI
over an API, and it is roughly two orders of magnitude cheaper.

| | Trusted Signing | Traditional OV/EV certificate |
| :--- | :--- | :--- |
| Cost | **$9.99/month** (Basic, 5,000 signatures) | ~$200–600/year, plus token hardware |
| Key storage | Managed, FIPS-compliant | USB token you physically hold |
| Works on hosted CI | Yes, over an API | No — needs a self-hosted runner or cloud HSM |
| Certificate lifetime | Renewed daily, valid 24h | 1–3 years |

### 1. Eligibility — Cloudresty Ltd qualifies

Public Trust certificates are available to organisations in the UK among others.
The three-year organisation-history requirement that applied when the service
launched **has been removed**.

### 2. Set it up

Needs an Azure subscription and a Microsoft Entra tenant.

1. Register the `Microsoft.CodeSigning` resource provider.
2. Create an **Artifact Signing account**, Basic SKU, in a supported region
   (`westeurope` or `northeurope` for us).
3. Create a **public identity validation** for the organisation. This wants:
   the legal entity name, a website on the company's domain, a **monitored**
   email on that domain, a business identifier, the registered address, and a
   named individual who completes a government-ID check.
4. Create a **Public Trust** certificate profile against that validation.

**Processing takes 1 to 20 business days**, longer if Microsoft asks for more
documents — so start it alongside the D-U-N-S request, not after it.

Two traps worth knowing before filling the form in:

- The details must match public records for the company **exactly**. Correcting
  them afterwards requires an entirely new identity validation, which invalidates
  the certificates already issued against the old one.
- The verification emails expire in seven days and the mailbox must accept links
  from external senders. A quarantined verification email costs a restart.

### 3. Wire it into CI

Use the official `azure/trusted-signing-action`, authenticated with **OIDC
federated credentials** rather than a client secret — GitHub then mints a
short-lived token per run and there is no long-lived credential in the
repository at all.

## What is left after both are done

- Drop the `xattr -dr com.apple.quarantine` caveat from the Homebrew cask and
  from `podsteer.com/download`.
- Remove the "Unsigned for now" note from the macOS platform entry in the
  website's domain model. The site is written so this is a one-line change.
- Say in `SECURITY.md` that releases are signed and notarised, and that the
  hardened runtime carries no entitlements.

## The other release blocker

Unrelated to signing, and much smaller: `HOMEBREW_TAP_TOKEN` is still not
configured on this repository. The tap workflow needs a PAT with
`contents: write` on `podsteer/homebrew-tap`, or `brew install --cask podsteer`
cannot work regardless of signing.
