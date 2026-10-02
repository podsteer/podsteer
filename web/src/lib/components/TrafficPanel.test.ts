import { render, fireEvent, cleanup, waitFor } from "@testing-library/svelte";
import { afterEach, describe, expect, it, vi } from "vitest";

import TrafficPanel from "./TrafficPanel.svelte";
import { setTrafficBindings } from "$lib/topology/trafficApi";
import type { TrafficLayer, TrafficSources } from "$lib/topology/contract";

afterEach(() => {
  cleanup();
  setTrafficBindings(null);
});

const sources = (p: Partial<TrafficSources> = {}): TrafficSources => ({
  backend: "Prometheus in monitoring",
  sources: [
    { source: "istio", available: true, detail: "istio_requests_total" },
    { source: "linkerd", available: false, detail: "" },
  ],
  status: "answered",
  message: "",
  ...p,
});

const layer: TrafficLayer = {
  source: "istio",
  window: "5m",
  edges: [],
  unmapped: [],
  status: "answered",
  message: "",
  provenance: {
    origin: "backend",
    source: "Prometheus in monitoring",
    verification: "verified",
  },
  expressions: ["sum by (a) (rate(x[5m]))"],
};

function mount(onlayer = vi.fn()) {
  return {
    onlayer,
    ...render(TrafficPanel, {
      props: { clusterId: "c1", namespaces: ["shop"], all: false, onlayer },
    }),
  };
}

describe("TrafficPanel", () => {
  it("asks nothing until switched on", () => {
    const Sources = vi.fn();
    const Traffic = vi.fn();
    setTrafficBindings({ Sources, Traffic });
    const { getByText } = mount();
    expect(getByText(/Nothing is asked until you do/)).toBeTruthy();
    expect(Sources).not.toHaveBeenCalled();
    expect(Traffic).not.toHaveBeenCalled();
  });

  it("asks sources once, then traffic, and hands the layer up", async () => {
    const Sources = vi.fn().mockResolvedValue(sources());
    const Traffic = vi.fn().mockResolvedValue(layer);
    setTrafficBindings({ Sources, Traffic });
    const { getByRole, onlayer, findByText } = mount();
    await fireEvent.click(getByRole("checkbox"));
    await findByText(/No traffic edges|answered with no traffic/i);
    expect(Sources).toHaveBeenCalledTimes(1);
    expect(Traffic).toHaveBeenCalledWith("c1", ["shop"], false, "istio", "5m");
    expect(onlayer).toHaveBeenLastCalledWith(layer);
  });

  it("says what each source needs when none is found, and that nothing is installed", async () => {
    setTrafficBindings({
      Sources: vi.fn().mockResolvedValue(sources({ sources: [] })),
      Traffic: vi.fn(),
    });
    const { getByRole, findByText, getAllByText } = mount();
    await fireEvent.click(getByRole("checkbox"));
    await findByText(/No traffic source was found/);
    expect(getAllByText(/PodSteer installs nothing/).length).toBeGreaterThan(0);
    expect(await findByText(/labelsContext/)).toBeTruthy();
  });

  it("links to the setting when the metrics query is not enabled", async () => {
    setTrafficBindings({
      Sources: vi
        .fn()
        .mockResolvedValue(sources({ status: "not-enabled", message: "Off." })),
      Traffic: vi.fn(),
    });
    const { getByRole, findByText } = mount();
    await fireEvent.click(getByRole("checkbox"));
    await findByText(/Metrics query is not enabled/);
    await waitFor(() =>
      expect(
        getByRole("button", { name: /metrics query setting/ }),
      ).toBeTruthy(),
    );
  });

  it("says so when the traffic backend is not in this build", async () => {
    const { getByRole, findByRole } = mount();
    await fireEvent.click(getByRole("checkbox"));
    expect((await findByRole("alert")).textContent).toMatch(/not available/);
  });
});
