/**
 * The observed-traffic overlay: pure functions from a loaded TrafficLayer and
 * the boxes the map drew to the lines drawn over them.
 *
 * TRAFFIC IS A LAYER, NOT AN EDGE. Nothing here touches the topology graph:
 * the graph's edges are relationships Kubernetes really holds, these are
 * what a monitoring backend measured over a window. Endpoints that are not
 * boxes on the map (the internet, an unlabelled peer, a workload outside the
 * scope) become SYNTHETIC OVERLAY NODES that exist only in this module's
 * output, one per host and one "Unknown".
 *
 * No colour is invented: tones map onto the gauge tokens the charts already
 * use, so the overlay follows the theme and its contrast rules.
 */

import type {
  TrafficEdge,
  TrafficEndpoint,
  TrafficLayer,
  TrafficSourceName,
  TrafficSources,
} from "./topology/contract";

/** What an edge's width encodes. One per layer, so widths are comparable. */
export type TrafficMetric = "requests" | "bytes" | "connections";

/** Colour tone, by error ratio. `neutral` means errors are not exposed. */
export type TrafficTone = "normal" | "warn" | "critical" | "neutral";

/** Theme tokens (app.css) per tone. */
export const TONE_COLOUR: Record<TrafficTone, string> = {
  normal: "var(--gauge-normal)",
  warn: "var(--gauge-warn)",
  critical: "var(--gauge-critical)",
  neutral: "var(--outline)",
};

export const MIN_WIDTH = 1;
export const MAX_WIDTH = 8;
/** Decades below the busiest edge that still get a distinct width. */
export const WIDTH_DECADES = 3;
/** Error ratio at or above which an edge is warned / critical. */
export const WARN_ERROR_RATIO = 0.01;
export const CRITICAL_ERROR_RATIO = 0.05;
/** Fraction of visible edges flagged as the hot path. */
export const HOT_FRACTION = 0.1;

export const UNKNOWN_NODE_ID = "traffic:unknown";

/** Namespaces that are the platform's own, hidden by `hideSystem`. */
const SYSTEM_NAMESPACES = new Set([
  "kube-system",
  "kube-public",
  "kube-node-lease",
  "istio-system",
  "istio-ingress",
  "linkerd",
  "cilium",
]);
const SYSTEM_PREFIXES = ["linkerd-", "cilium-"];

export function isSystemNamespace(ns: string): boolean {
  if (!ns) return false;
  return (
    SYSTEM_NAMESPACES.has(ns) || SYSTEM_PREFIXES.some((p) => ns.startsWith(p))
  );
}

export interface TrafficFilters {
  hideSystem?: boolean;
  /** Hides edges to or from the outside world and unlabelled peers. */
  hideExternal?: boolean;
}

export interface Point {
  x: number;
  y: number;
}

/** A box that exists only on the overlay. */
export interface OverlayNode {
  id: string;
  kind: "external" | "unknown" | "unmapped";
  label: string;
  x: number;
  y: number;
}

export interface OverlayEdge {
  id: string;
  from: string;
  to: string;
  protocol: string;
  /** The value width is scaled from, in the layer's metric. */
  value: number;
  width: number;
  tone: TrafficTone;
  colour: string;
  /** Null when the source exposes no errors. */
  errorRatio: number | null;
  hot: boolean;
  tooltip: string;
  /** SVG path between the two box centres; reverse pairs bow apart. */
  path: string;
}

export interface TrafficOverlay {
  metric: TrafficMetric;
  edges: OverlayEdge[];
  nodes: OverlayNode[];
  /** Edges not drawn, by reason, so the panel can say so. */
  skipped: { filtered: number; offMap: number };
}

// ---------------------------------------------------------------- metric

/** The metric a layer is scaled by: requests, else bytes, else connections. */
export function metricFor(
  layer: Pick<TrafficLayer, "edges" | "source">,
): TrafficMetric {
  if (layer.edges.some((e) => e.requestsPerSec > 0)) return "requests";
  if (layer.edges.some((e) => e.bytesPerSec > 0)) return "bytes";
  if (layer.edges.some((e) => e.connections > 0)) return "connections";
  return layer.source === "caretta" ? "connections" : "requests";
}

export function valueOf(e: TrafficEdge, metric: TrafficMetric): number {
  const v =
    metric === "requests"
      ? e.requestsPerSec
      : metric === "bytes"
        ? e.bytesPerSec
        : e.connections;
  return Number.isFinite(v) && v > 0 ? v : 0;
}

/** Log-scaled width, clamped to [MIN_WIDTH, MAX_WIDTH]. */
export function edgeWidth(value: number, max: number): number {
  if (!(value > 0) || !(max > 0)) return MIN_WIDTH;
  const t =
    (Math.log10(value) - Math.log10(max) + WIDTH_DECADES) / WIDTH_DECADES;
  const clamped = Math.min(1, Math.max(0, t));
  return MIN_WIDTH + clamped * (MAX_WIDTH - MIN_WIDTH);
}

/** errors / requests, or null when this edge has no request rate to divide by. */
export function errorRatio(e: TrafficEdge): number | null {
  if (!(e.requestsPerSec > 0)) return null;
  const r = Math.max(0, e.errorsPerSec) / e.requestsPerSec;
  return Math.min(1, r);
}

export function toneFor(ratio: number | null): TrafficTone {
  if (ratio === null) return "neutral";
  if (ratio >= CRITICAL_ERROR_RATIO) return "critical";
  if (ratio >= WARN_ERROR_RATIO) return "warn";
  return "normal";
}

// -------------------------------------------------------------- endpoints

/** The host an endpoint is grouped under, or '' when it is not external. */
export function externalHost(ep: TrafficEndpoint): string {
  return ep.external.trim();
}

/** Human label of an in-cluster endpoint. */
export function endpointLabel(ep: TrafficEndpoint): string {
  const host = externalHost(ep);
  if (host) return host;
  const name = ep.workload || ep.service;
  if (!name) return "Unknown";
  return ep.namespace ? `${ep.namespace}/${name}` : name;
}

type Resolved =
  | { kind: "node"; id: string }
  | {
      kind: "overlay";
      id: string;
      nodeKind: OverlayNode["kind"];
      label: string;
    }
  | { kind: "off" };

function resolve(
  ep: TrafficEndpoint,
  drawn: ReadonlyMap<string, Point>,
): Resolved {
  if (ep.nodeId)
    return drawn.has(ep.nodeId)
      ? { kind: "node", id: ep.nodeId }
      : { kind: "off" };
  const host = externalHost(ep);
  if (host)
    return {
      kind: "overlay",
      id: `traffic:ext:${host}`,
      nodeKind: "external",
      label: host,
    };
  if (ep.unknown || !(ep.workload || ep.service)) {
    return {
      kind: "overlay",
      id: UNKNOWN_NODE_ID,
      nodeKind: "unknown",
      label: "Unknown",
    };
  }
  const label = endpointLabel(ep);
  return {
    kind: "overlay",
    id: `traffic:ns:${label}`,
    nodeKind: "unmapped",
    label,
  };
}

function isOutside(ep: TrafficEndpoint): boolean {
  return (
    !ep.nodeId &&
    (externalHost(ep) !== "" || ep.unknown || !(ep.workload || ep.service))
  );
}

/** Whether a filter removes this edge. */
export function filteredOut(e: TrafficEdge, f: TrafficFilters): boolean {
  if (
    f.hideSystem &&
    (isSystemNamespace(e.source.namespace) ||
      isSystemNamespace(e.dest.namespace))
  ) {
    return true;
  }
  if (f.hideExternal && (isOutside(e.source) || isOutside(e.dest))) return true;
  return false;
}

// ---------------------------------------------------------------- tooltip

function fmt(n: number): string {
  if (n >= 1000) return n.toFixed(0);
  if (n >= 0.01) return String(Number(n.toPrecision(3)));
  return n > 0 ? "<0.01" : "0";
}

function fmtBytes(n: number): string {
  const units = ["B/s", "KiB/s", "MiB/s", "GiB/s"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${fmt(v)} ${units[i]}`;
}

function fmtMs(ms: number): string {
  return ms >= 1000 ? `${(ms / 1000).toFixed(2)} s` : `${fmt(ms)} ms`;
}

/** The per-edge tooltip: rate, error %, latency quantiles when exposed, protocol. */
export function edgeTooltip(
  e: TrafficEdge,
  fromLabel: string,
  toLabel: string,
): string {
  const head = e.protocol
    ? `${fromLabel} → ${toLabel} (${e.protocol})`
    : `${fromLabel} → ${toLabel}`;
  const parts: string[] = [];
  if (e.requestsPerSec > 0) parts.push(`${fmt(e.requestsPerSec)} req/s`);
  const ratio = errorRatio(e);
  if (ratio !== null)
    parts.push(
      `${(ratio * 100).toFixed(ratio > 0 && ratio < 0.001 ? 2 : 1)}% errors`,
    );
  if (e.bytesPerSec > 0) parts.push(fmtBytes(e.bytesPerSec));
  if (e.connections > 0) parts.push(`${fmt(e.connections)} connections`);
  const lat: string[] = [];
  if (e.p50 >= 0) lat.push(`p50 ${fmtMs(e.p50)}`);
  if (e.p95 >= 0) lat.push(`p95 ${fmtMs(e.p95)}`);
  if (e.p99 >= 0) lat.push(`p99 ${fmtMs(e.p99)}`);
  if (lat.length) parts.push(lat.join(" · "));
  return parts.length ? `${head}\n${parts.join(" · ")}` : head;
}

// --------------------------------------------------------------- geometry

function edgePath(a: Point, b: Point, bow: number): string {
  if (a.x === b.x && a.y === b.y) {
    const r = 24;
    return `M ${a.x} ${a.y} C ${a.x + r} ${a.y - 2 * r}, ${a.x - r} ${a.y - 2 * r}, ${a.x} ${a.y}`;
  }
  const mx = (a.x + b.x) / 2;
  const my = (a.y + b.y) / 2;
  const dx = b.x - a.x;
  const dy = b.y - a.y;
  const len = Math.hypot(dx, dy);
  const cx = mx + (-dy / len) * len * 0.15 * bow;
  const cy = my + (dx / len) * len * 0.15 * bow;
  return `M ${a.x} ${a.y} Q ${cx} ${cy} ${b.x} ${b.y}`;
}

/** Where the synthetic column sits, relative to the drawn boxes. */
export const OVERLAY_GAP_X = 260;
export const OVERLAY_GAP_Y = 90;

function placeOverlayNodes(
  specs: Map<string, { kind: OverlayNode["kind"]; label: string }>,
  drawn: ReadonlyMap<string, Point>,
): OverlayNode[] {
  let maxX = 0;
  let minY = 0;
  let first = true;
  for (const p of drawn.values()) {
    if (first) {
      maxX = p.x;
      minY = p.y;
      first = false;
    }
    maxX = Math.max(maxX, p.x);
    minY = Math.min(minY, p.y);
  }
  // Unknown last, then externals by host, so the column never reshuffles.
  const order = (k: OverlayNode["kind"]) =>
    k === "unmapped" ? 0 : k === "external" ? 1 : 2;
  const ids = [...specs.keys()].sort((a, b) => {
    const sa = specs.get(a)!;
    const sb = specs.get(b)!;
    return (
      order(sa.kind) - order(sb.kind) ||
      sa.label.localeCompare(sb.label) ||
      a.localeCompare(b)
    );
  });
  return ids.map((id, i) => ({
    id,
    kind: specs.get(id)!.kind,
    label: specs.get(id)!.label,
    x: maxX + OVERLAY_GAP_X,
    y: minY + i * OVERLAY_GAP_Y,
  }));
}

// ------------------------------------------------------------------ build

interface Merged {
  key: string;
  from: string;
  to: string;
  fromLabel: string;
  toLabel: string;
  edge: TrafficEdge;
  /** The constituent with the highest value; its latency quantiles are kept. */
  best: number;
}

/**
 * Turns a layer into overlay edges and synthetic nodes.
 *
 * `drawn` maps the topology node ids that are on screen to their centres.
 * An endpoint mapped to a node that is NOT drawn (kind toggled off, folded
 * away) drops its edge and is counted in `skipped.offMap`.
 *
 * Several measurements that land on the same pair of boxes and protocol —
 * two external IPs of one host, say — are summed. Quantiles cannot be added,
 * so the merged edge keeps those of its busiest constituent.
 */
export function buildOverlay(
  layer: TrafficLayer,
  drawn: ReadonlyMap<string, Point>,
  filters: TrafficFilters = {},
): TrafficOverlay {
  const metric = metricFor(layer);
  const skipped = { filtered: 0, offMap: 0 };
  const specs = new Map<string, { kind: OverlayNode["kind"]; label: string }>();
  const merged = new Map<string, Merged>();

  for (const e of layer.edges) {
    if (filteredOut(e, filters)) {
      skipped.filtered++;
      continue;
    }
    const a = resolve(e.source, drawn);
    const b = resolve(e.dest, drawn);
    if (a.kind === "off" || b.kind === "off") {
      skipped.offMap++;
      continue;
    }
    const from = a.id;
    const to = b.id;
    const fromLabel = a.kind === "overlay" ? a.label : endpointLabel(e.source);
    const toLabel = b.kind === "overlay" ? b.label : endpointLabel(e.dest);
    const key = `${from}|${to}|${e.protocol}`;
    const v = valueOf(e, metric);
    const m = merged.get(key);
    if (!m) {
      merged.set(key, {
        key,
        from,
        to,
        fromLabel,
        toLabel,
        edge: { ...e },
        best: v,
      });
    } else {
      const acc = m.edge;
      acc.requestsPerSec += e.requestsPerSec;
      acc.errorsPerSec += e.errorsPerSec;
      acc.bytesPerSec += e.bytesPerSec;
      acc.connections += e.connections;
      if (v > m.best) {
        m.best = v;
        acc.p50 = e.p50;
        acc.p95 = e.p95;
        acc.p99 = e.p99;
      }
    }
    if (a.kind === "overlay")
      specs.set(a.id, { kind: a.nodeKind, label: a.label });
    if (b.kind === "overlay")
      specs.set(b.id, { kind: b.nodeKind, label: b.label });
  }

  const nodes = placeOverlayNodes(specs, drawn);
  const pos = new Map<string, Point>(drawn);
  for (const n of nodes) pos.set(n.id, n);

  const list = [...merged.values()].sort((x, y) =>
    x.key < y.key ? -1 : x.key > y.key ? 1 : 0,
  );
  const values = list.map((m) => valueOf(m.edge, metric));
  const max = values.reduce((a, v) => Math.max(a, v), 0);
  const hot = hotSet(values);

  const present = new Set(list.map((m) => `${m.from}|${m.to}`));
  const edges: OverlayEdge[] = list.map((m, i) => {
    const ratio = errorRatio(m.edge);
    const tone = toneFor(ratio);
    const reverse = present.has(`${m.to}|${m.from}`) && m.from !== m.to;
    const bow = reverse ? (m.from < m.to ? 1 : -1) : 0;
    return {
      id: m.key,
      from: m.from,
      to: m.to,
      protocol: m.edge.protocol,
      value: values[i],
      width: edgeWidth(values[i], max),
      tone,
      colour: TONE_COLOUR[tone],
      errorRatio: ratio,
      hot: hot.has(i),
      tooltip: edgeTooltip(m.edge, m.fromLabel, m.toLabel),
      path: edgePath(pos.get(m.from)!, pos.get(m.to)!, bow),
    };
  });

  return { metric, edges, nodes, skipped };
}

/** Indices of the top 10% by value (at least one), ignoring zero-valued edges. */
export function hotSet(values: readonly number[]): Set<number> {
  const live = values.map((v, i) => ({ v, i })).filter((x) => x.v > 0);
  if (!live.length) return new Set();
  const n = Math.max(1, Math.ceil(live.length * HOT_FRACTION));
  live.sort((a, b) => b.v - a.v || a.i - b.i);
  return new Set(live.slice(0, n).map((x) => x.i));
}

// --------------------------------------------------- states and wording

export interface SourceNeed {
  source: TrafficSourceName;
  label: string;
  needs: string;
}

/** What each source needs. PodSteer reads metrics that already exist. */
export const SOURCE_NEEDS: readonly SourceNeed[] = [
  {
    source: "istio",
    label: "Istio",
    needs:
      "Istio sidecars or ambient mode, with Prometheus scraping the mesh (istio_requests_total).",
  },
  {
    source: "linkerd",
    label: "Linkerd",
    needs:
      "linkerd-viz installed, whose Prometheus holds the proxies’ metrics.",
  },
  {
    source: "beyla",
    label: "Beyla / OBI",
    needs:
      "Grafana Beyla or OpenTelemetry eBPF Instrumentation exporting network flow metrics.",
  },
  {
    source: "caretta",
    label: "Caretta",
    needs:
      "Caretta running, with Prometheus scraping its caretta_links_observed metric.",
  },
  {
    source: "hubble",
    label: "Hubble",
    needs:
      "Cilium Hubble metrics enabled with labelsContext including source and destination workload and namespace.",
  },
];

export const NOTHING_INSTALLED =
  "PodSteer installs nothing in your cluster; it only reads metrics that are already there.";

export function sourceLabel(s: TrafficSourceName): string {
  return SOURCE_NEEDS.find((n) => n.source === s)?.label ?? s;
}

export type TrafficState =
  | { kind: "idle" }
  | { kind: "not-enabled"; message: string }
  | { kind: "no-prometheus"; message: string }
  | { kind: "backend-problem"; status: string; message: string }
  | { kind: "no-source"; needs: readonly SourceNeed[] }
  | { kind: "too-large"; message: string }
  | { kind: "no-traffic"; message: string }
  | { kind: "ready"; edges: number; unmapped: number };

const BACKEND_OK = new Set(["", "answered", "answered-empty", "enabled", "ok"]);

/** Collapses what the backend said into the one state the panel shows. */
export function trafficState(
  sources: TrafficSources | null,
  layer: TrafficLayer | null,
): TrafficState {
  if (!sources) return { kind: "idle" };
  switch (sources.status) {
    case "not-enabled":
      return { kind: "not-enabled", message: sources.message };
    case "nothing-discovered":
      return { kind: "no-prometheus", message: sources.message };
    default:
      if (!BACKEND_OK.has(sources.status)) {
        return {
          kind: "backend-problem",
          status: sources.status,
          message: sources.message,
        };
      }
  }
  if (!sources.sources.some((s) => s.available))
    return { kind: "no-source", needs: SOURCE_NEEDS };
  if (!layer) return { kind: "idle" };
  if (layer.status === "too-large")
    return { kind: "too-large", message: layer.message };
  if (!BACKEND_OK.has(layer.status)) {
    return {
      kind: "backend-problem",
      status: layer.status,
      message: layer.message,
    };
  }
  if (layer.edges.length === 0)
    return { kind: "no-traffic", message: layer.message };
  return {
    kind: "ready",
    edges: layer.edges.length,
    unmapped: layer.unmapped.length,
  };
}

/** The one-line provenance note, from the backend's SeriesProvenance. */
export function provenanceNote(
  p: unknown,
): { source: string; text: string } | null {
  if (!p || typeof p !== "object") return null;
  const o = p as { source?: string; verification?: string; filtered?: boolean };
  if (!o.source) return null;
  let text = "";
  switch (o.verification) {
    case "verified":
      text = "It answers for this cluster and nothing else.";
      break;
    case "fleet":
      text = o.filtered
        ? "It also holds other clusters, so this query was narrowed to this cluster's own namespaces."
        : "It also holds other clusters.";
      break;
    case "mismatch":
      text = "It appears to hold a different cluster.";
      break;
    case "unverifiable":
      text = "PodSteer could not check which cluster it holds.";
      break;
  }
  return { source: o.source, text };
}
