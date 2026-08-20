# K8Sense brand

## The mark

A hexagon with a pulse through it.

**The hexagon is a cluster** — a cell, a container, a unit that tiles with
others. **The pulse is the product.** K8Sense does not list a cluster, it reads
one: the line is a vital sign, and the rise is the thing worth looking at. It
is the same shape as the trend chart on the dashboard, which is not a
coincidence.

### Six sides, deliberately

The Kubernetes logo is a **seven**-sided helm with spokes, and it is a CNCF
trademark. Their trademark guidelines allow using the Kubernetes name to
describe compatibility — "a Kubernetes client" — but **not** using the logo, or
a confusingly similar derivative, as your own product mark.

An earlier version of the splash screen drew exactly that: a heptagon with
spokes and a hub. It has been replaced. Do not reintroduce a seven-sided
outline, spokes radiating from a centre, or the Kubernetes blue (#326CE5).

## Files

| File | Use |
| :--- | :--- |
| `k8sense-mark.svg` | The mark alone, in brand blue. Light backgrounds, documents, README headers. |
| `k8sense-mark-white.svg` | The mark in `currentColor`, defaulting to white. Dark or coloured backgrounds; inline it in HTML and it inherits the text colour. |
| `k8sense-tile.svg` | Mark on the blue tile. Avatars, app icon, anywhere it needs its own ground. |
| `k8sense-logo.svg` | Horizontal lockup for light backgrounds. |
| `k8sense-logo-dark.svg` | Horizontal lockup for dark backgrounds, with the blue lightened for contrast. |
| `k8sense-favicon.svg` | A 16-pixel drawing, not a scaled-down mark. See below. |
| `k8sense-social.svg` | 1280×640 share card: GitHub repository preview, Open Graph, LinkedIn. |
| `png/` | Rendered exports. Regenerate with `make brand`. |

### Why the favicon is a different drawing

At 16px the mark's 6.5-unit hexagon stroke lands on two-thirds of a pixel and
the whole thing turns to grey mush — this was measured, not assumed. The
favicon keeps the half that carries the meaning (the pulse) on the ground that
carries the brand (the tile), and drops the hexagon. Do not substitute a
scaled-down `k8sense-tile.svg`; it is unreadable at that size.

## Colour

The blue is **the SynapCTX blue**, so the two products read as siblings.

| Token | Value | Use |
| :--- | :--- | :--- |
| Brand blue | `#1976D2` → `#1557b0` | Tile and mark gradient, top-left to bottom-right |
| Brand blue, dark surfaces | `#8ab4f8` | The mark and "K8" on dark backgrounds, where `#1976D2` fails contrast |
| Ink | `#16181d` | "Sense" on light backgrounds |
| Paper | `#e6e1e9` | "Sense" on dark backgrounds |

Never place the `#1976D2` mark on a dark background — use the white or
`#8ab4f8` variant. Never place the white mark on white.

## The wordmark

"K8Sense" — one word, capital K, digit 8, capital S. Not "K8sense", "k8Sense"
or "K8 Sense".

The lockups set it in the system sans stack as **live text**, so it stays
editable and searchable. That means it depends on the reader's fonts. Before
sending a lockup anywhere that must render byte-identically — print, a
partner's press kit, an app store listing — convert it to outlines first
(Illustrator: Type ▸ Create Outlines, or Inkscape's export-text-to-path).

## Clear space and minimum size

Keep at least **half the mark's width** free on every side of a lockup.

Minimum sizes: mark 24px, lockup 120px wide, favicon variant below that.

## Where each variant is already wired

- `build/appicon.png` — the packaged application icon, rendered from the tile
  at 880px inside a 1024px canvas. The margin is deliberate: macOS icons are
  not edge-to-edge and Wails adds no padding of its own.
- `web/public/favicon.svg`, `favicon-32.png`, `apple-touch-icon.png` — served
  by the dev server and by k8sense.com. The webview itself never asks for a
  favicon; the browser does.
- `web/src/lib/components/Splash.svelte` — the mark redrawn in the splash's own
  gradient, because it sits on the splash's dark ground rather than on blue.

## Regenerating

```sh
make brand
```

Renders every PNG in `png/` from the SVG sources, rewrites `build/appicon.png`
and refreshes the frontend's favicons. Requires `rsvg-convert` and ImageMagick
(`brew install librsvg imagemagick`).
