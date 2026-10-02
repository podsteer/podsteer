import { describe, expect, it } from "vitest";

import {
  CRITICAL_ERROR_RATIO,
  MAX_WIDTH,
  MIN_WIDTH,
  TONE_COLOUR,
  UNKNOWN_NODE_ID,
  WARN_ERROR_RATIO,
  buildOverlay,
  edgeTooltip,
  edgeWidth,
  hotSet,
  isSystemNamespace,
  metricFor,
  provenanceNote,
  toneFor,
  trafficState,
} from "./trafficLayer";
import type {
  TrafficEdge,
  TrafficEndpoint,
  TrafficLayer,
  TrafficSources,
} from "./topology/contract";

function ep(p: Partial<TrafficEndpoint> = {}): TrafficEndpoint {
  return {
    namespace: "",
    workload: "",
    service: "",
    external: "",
    unknown: false,
    nodeId: "",
    ...p,
  };
}
function edge(
  p: Partial<Omit<TrafficEdge, "source" | "dest">> & {
    source?: Partial<TrafficEndpoint>;
    dest?: Partial<TrafficEndpoint>;
  } = {},
): TrafficEdge {
  const { source, dest, ...rest } = p;
  return {
    source: ep(source),
    dest: ep(dest),
    protocol: "http",
    requestsPerSec: 0,
    errorsPerSec: 0,
    bytesPerSec: 0,
    connections: 0,
    p50: -1,
    p95: -1,
    p99: -1,
    ...rest,
  };
}
function layer(
  edges: TrafficEdge[],
  p: Partial<TrafficLayer> = {},
): TrafficLayer {
  return {
    source: "istio",
    window: "5m",
    edges,
    unmapped: [],
    status: "answered",
    message: "",
    provenance: null,
    expressions: [],
    ...p,
  };
}
const drawn = new Map([
  ["a", { x: 0, y: 0 }],
  ["b", { x: 200, y: 100 }],
  ["c", { x: 400, y: 0 }],
]);
const mapped = (id: string): Partial<TrafficEndpoint> => ({
  nodeId: id,
  workload: id,
  namespace: "shop",
});

describe("width scaling", () => {
  it("is log scaled and clamped", () => {
    expect(edgeWidth(1000, 1000)).toBe(MAX_WIDTH);
    expect(edgeWidth(100, 1000)).toBeCloseTo(
      MIN_WIDTH + (2 / 3) * (MAX_WIDTH - MIN_WIDTH),
    );
    expect(edgeWidth(1, 1000)).toBe(MIN_WIDTH);
    expect(edgeWidth(0.0001, 1000)).toBe(MIN_WIDTH);
    expect(edgeWidth(5000, 1000)).toBe(MAX_WIDTH);
  });
  it("gives zero, negative and NaN the minimum", () => {
    for (const v of [0, -3, NaN]) expect(edgeWidth(v, 10)).toBe(MIN_WIDTH);
    expect(edgeWidth(5, 0)).toBe(MIN_WIDTH);
  });
  it("picks the metric: requests, else bytes, else connections", () => {
    expect(
      metricFor(layer([edge({ requestsPerSec: 1, bytesPerSec: 9 })])),
    ).toBe("requests");
    expect(metricFor(layer([edge({ bytesPerSec: 9 })]))).toBe("bytes");
    expect(
      metricFor(layer([edge({ connections: 2 })], { source: "caretta" })),
    ).toBe("connections");
    expect(metricFor(layer([], { source: "caretta" }))).toBe("connections");
  });
});

describe("colour thresholds", () => {
  it("maps error ratio to a theme token", () => {
    expect(toneFor(null)).toBe("neutral");
    expect(toneFor(0)).toBe("normal");
    expect(toneFor(WARN_ERROR_RATIO - 0.001)).toBe("normal");
    expect(toneFor(WARN_ERROR_RATIO)).toBe("warn");
    expect(toneFor(CRITICAL_ERROR_RATIO)).toBe("critical");
    for (const c of Object.values(TONE_COLOUR)) expect(c).toMatch(/^var\(--/);
  });
  it("colours overlay edges from errors/requests; no request rate is neutral", () => {
    const o = buildOverlay(
      layer([
        edge({
          source: mapped("a"),
          dest: mapped("b"),
          requestsPerSec: 100,
          errorsPerSec: 10,
        }),
        edge({
          source: mapped("b"),
          dest: mapped("c"),
          requestsPerSec: 100,
          errorsPerSec: 0,
        }),
        edge({ source: mapped("a"), dest: mapped("c"), bytesPerSec: 100 }),
      ]),
      drawn,
    );
    const by = Object.fromEntries(o.edges.map((e) => [e.id, e]));
    expect(by["a|b|http"].tone).toBe("critical");
    expect(by["b|c|http"].tone).toBe("normal");
    expect(by["a|c|http"].tone).toBe("neutral");
    expect(by["a|c|http"].errorRatio).toBeNull();
  });
});

describe("external and unknown grouping", () => {
  it("makes one overlay node per host and one Unknown, never touching drawn ids", () => {
    const o = buildOverlay(
      layer([
        edge({
          source: mapped("a"),
          dest: { external: "api.stripe.com" },
          requestsPerSec: 3,
        }),
        edge({
          source: mapped("b"),
          dest: { external: "api.stripe.com" },
          requestsPerSec: 2,
        }),
        edge({
          source: mapped("b"),
          dest: { external: "github.com" },
          requestsPerSec: 1,
        }),
        edge({
          source: { unknown: true },
          dest: mapped("a"),
          requestsPerSec: 4,
        }),
        edge({ source: {}, dest: mapped("c"), requestsPerSec: 4 }),
      ]),
      drawn,
    );
    expect(o.nodes.map((n) => n.id)).toEqual([
      "traffic:ext:api.stripe.com",
      "traffic:ext:github.com",
      UNKNOWN_NODE_ID,
    ]);
    expect(o.nodes.every((n) => !drawn.has(n.id))).toBe(true);
    // placed right of every drawn box
    expect(o.nodes.every((n) => n.x > 400)).toBe(true);
  });
  it("merges edges that land on the same pair and protocol, keeping busiest latency", () => {
    const o = buildOverlay(
      layer([
        edge({
          source: mapped("a"),
          dest: { external: "x.io" },
          requestsPerSec: 1,
          p95: 10,
        }),
        edge({
          source: mapped("a"),
          dest: { external: "x.io" },
          requestsPerSec: 5,
          p95: 99,
        }),
      ]),
      drawn,
    );
    expect(o.edges).toHaveLength(1);
    expect(o.edges[0].value).toBe(6);
    expect(o.edges[0].tooltip).toContain("p95 99 ms");
  });
  it("draws unmapped in-cluster workloads as overlay nodes, drops off-map ones", () => {
    const o = buildOverlay(
      layer([
        edge({
          source: { namespace: "other", workload: "w" },
          dest: mapped("a"),
          requestsPerSec: 1,
        }),
        edge({
          source: mapped("a"),
          dest: { nodeId: "gone" },
          requestsPerSec: 1,
        }),
      ]),
      drawn,
    );
    expect(o.nodes.map((n) => [n.kind, n.label])).toEqual([
      ["unmapped", "other/w"],
    ]);
    expect(o.skipped.offMap).toBe(1);
    expect(o.edges).toHaveLength(1);
  });
});

describe("hot path", () => {
  it("is the top 10% by value, at least one, ignoring zeros", () => {
    const values = Array.from({ length: 20 }, (_, i) => i); // 0..19, 19 live
    expect([...hotSet(values)].sort((a, b) => a - b)).toEqual([18, 19]);
    expect([...hotSet([0, 0, 5])]).toEqual([2]);
    expect(hotSet([0, 0]).size).toBe(0);
    expect(hotSet([]).size).toBe(0);
  });
  it("breaks ties by order", () => {
    expect([...hotSet([3, 3, 3])]).toEqual([0]);
  });
  it("flags overlay edges", () => {
    const edges = Array.from({ length: 10 }, (_, i) =>
      edge({
        source: mapped("a"),
        dest: mapped("b"),
        protocol: `p${i}`,
        requestsPerSec: i + 1,
      }),
    );
    const o = buildOverlay(layer(edges), drawn);
    expect(o.edges.filter((e) => e.hot).map((e) => e.protocol)).toEqual(["p9"]);
  });
});

describe("filters", () => {
  it("knows system namespaces", () => {
    for (const n of [
      "kube-system",
      "istio-system",
      "linkerd",
      "linkerd-viz",
      "cilium",
      "cilium-secrets",
    ]) {
      expect(isSystemNamespace(n)).toBe(true);
    }
    for (const n of ["shop", "istio-demo", "my-linkerd", ""])
      expect(isSystemNamespace(n)).toBe(false);
  });
  it("hideSystem and hideExternal drop edges and count them", () => {
    const l = layer([
      edge({ source: mapped("a"), dest: mapped("b"), requestsPerSec: 1 }),
      edge({
        source: { ...mapped("a"), namespace: "kube-system" },
        dest: mapped("b"),
        requestsPerSec: 1,
        protocol: "sys",
      }),
      edge({
        source: mapped("a"),
        dest: { external: "x.io" },
        requestsPerSec: 1,
      }),
      edge({
        source: { unknown: true },
        dest: mapped("b"),
        requestsPerSec: 1,
        protocol: "unk",
      }),
    ]);
    expect(buildOverlay(l, drawn).edges).toHaveLength(4);
    const sys = buildOverlay(l, drawn, { hideSystem: true });
    expect(sys.edges).toHaveLength(3);
    expect(sys.skipped.filtered).toBe(1);
    const ext = buildOverlay(l, drawn, { hideExternal: true });
    expect(ext.edges).toHaveLength(2);
    expect(ext.nodes).toEqual([]);
  });
});

describe("ordering and tooltip", () => {
  it("is stable regardless of input order", () => {
    const es = [
      edge({ source: mapped("c"), dest: mapped("a"), requestsPerSec: 1 }),
      edge({ source: mapped("a"), dest: mapped("b"), requestsPerSec: 2 }),
      edge({
        source: mapped("b"),
        dest: { external: "z.io" },
        requestsPerSec: 3,
      }),
      edge({ source: mapped("a"), dest: mapped("c"), requestsPerSec: 4 }),
    ];
    const x = buildOverlay(layer(es), drawn);
    const y = buildOverlay(layer([...es].reverse()), drawn);
    expect(x.edges.map((e) => e.id)).toEqual(y.edges.map((e) => e.id));
    expect(x.edges.map((e) => e.id)).toEqual(
      [...x.edges.map((e) => e.id)].sort(),
    );
    expect(x.edges.map((e) => e.path)).toEqual(y.edges.map((e) => e.path));
  });
  it("bows reverse pairs apart", () => {
    const o = buildOverlay(
      layer([
        edge({ source: mapped("a"), dest: mapped("b"), requestsPerSec: 1 }),
        edge({ source: mapped("b"), dest: mapped("a"), requestsPerSec: 1 }),
      ]),
      drawn,
    );
    expect(o.edges[0].path).not.toBe(o.edges[1].path);
  });
  it("shows latency only when exposed", () => {
    const e = edge({
      requestsPerSec: 12.34,
      errorsPerSec: 0.1234,
      p50: 5,
      p95: 0,
      p99: -1,
    });
    const t = edgeTooltip(e, "A", "B");
    expect(t).toContain("A → B (http)");
    expect(t).toContain("12.3 req/s");
    expect(t).toContain("1.0% errors");
    expect(t).toContain("p50 5 ms");
    expect(t).toContain("p95 0 ms");
    expect(t).not.toContain("p99");
    expect(edgeTooltip(edge({ connections: 4 }), "A", "B")).toContain(
      "4 connections",
    );
  });
});

describe("empty states", () => {
  const src = (p: Partial<TrafficSources> = {}): TrafficSources => ({
    backend: "Prometheus in monitoring",
    sources: [{ source: "istio", available: true, detail: "" }],
    status: "answered",
    message: "",
    ...p,
  });
  it("walks from idle to ready", () => {
    expect(trafficState(null, null).kind).toBe("idle");
    expect(
      trafficState(src({ status: "not-enabled", message: "m" }), null).kind,
    ).toBe("not-enabled");
    expect(trafficState(src({ status: "nothing-discovered" }), null).kind).toBe(
      "no-prometheus",
    );
    expect(
      trafficState(src({ status: "forbidden", message: "no" }), null),
    ).toMatchObject({
      kind: "backend-problem",
      status: "forbidden",
    });
    const none = trafficState(
      src({ sources: [{ source: "istio", available: false, detail: "" }] }),
      null,
    );
    expect(none.kind).toBe("no-source");
    if (none.kind === "no-source")
      expect(none.needs.map((n) => n.source)).toHaveLength(5);
    expect(trafficState(src(), null).kind).toBe("idle");
    expect(trafficState(src(), layer([], { status: "too-large" })).kind).toBe(
      "too-large",
    );
    expect(trafficState(src(), layer([])).kind).toBe("no-traffic");
    expect(trafficState(src(), layer([edge()], { unmapped: [ep()] }))).toEqual({
      kind: "ready",
      edges: 1,
      unmapped: 1,
    });
  });
  it("words provenance", () => {
    expect(provenanceNote(null)).toBeNull();
    expect(
      provenanceNote({
        source: "Prometheus in monitoring",
        verification: "verified",
      })?.text,
    ).toContain("this cluster");
    expect(
      provenanceNote({ source: "P", verification: "fleet", filtered: true })
        ?.text,
    ).toContain("narrowed");
  });
});
