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

function mount(onlayer = vi.fn(), onhelp = vi.fn()) {
  const view = render(TrafficPanel, {
    props: { clusterId: "c1", namespaces: ["shop"], all: false, onlayer, onhelp, on: false },
  });
  /** The page's toolbar toggle, as the panel sees it: its `on` prop. */
  const turn = (on: boolean) => view.rerender({ on });
  /** What the panel last handed to Help, as one string. */
  const helped = () =>
    (onhelp.mock.calls.at(-1)?.[0]?.sections ?? [])
      .flatMap((s: { body: string[] }) => s.body)
      .join(" ");
  return { onlayer, onhelp, turn, helped, ...view };
}

describe("TrafficPanel", () => {
  it("asks nothing until switched on", () => {
    const Sources = vi.fn();
    const Traffic = vi.fn();
    setTrafficBindings({ Sources, Traffic });
    mount();
    expect(Sources).not.toHaveBeenCalled();
    expect(Traffic).not.toHaveBeenCalled();
  });

  it("asks sources once, then traffic, and hands the layer up", async () => {
    const Sources = vi.fn().mockResolvedValue(sources());
    const Traffic = vi.fn().mockResolvedValue(layer);
    setTrafficBindings({ Sources, Traffic });
    const { turn, onlayer, findByText } = mount();
    await turn(true);
    await findByText(/No traffic in the last 5m/);
    expect(Sources).toHaveBeenCalledTimes(1);
    expect(Traffic).toHaveBeenCalledWith("c1", ["shop"], false, "istio", "5m");
    expect(onlayer).toHaveBeenLastCalledWith(layer);
  });

  it("puts what each source needs in Help, and that nothing is installed", async () => {
    setTrafficBindings({
      Sources: vi.fn().mockResolvedValue(sources({ sources: [] })),
      Traffic: vi.fn(),
    });
    const { turn, findByText, helped, onhelp } = mount();
    await turn(true);
    await findByText(/No traffic source was found/);
    await waitFor(() => expect(helped()).toContain("labelsContext"));
    expect(helped()).toContain("PodSteer installs nothing");
    expect(onhelp.mock.calls.at(-1)?.[0]?.problem).toBe(true);
  });

  it("links to the setting when the metrics query is not enabled", async () => {
    setTrafficBindings({
      Sources: vi
        .fn()
        .mockResolvedValue(sources({ status: "not-enabled", message: "Off." })),
      Traffic: vi.fn(),
    });
    const { turn, findByText, getByRole } = mount();
    await turn(true);
    await findByText(/Reading a monitoring backend is off for this cluster/);
    await waitFor(() =>
      expect(getByRole("button", { name: /Turn it on in Settings/ })).toBeTruthy(),
    );
  });

  it("says so when the traffic backend is not in this build", async () => {
    const { turn, findByText, helped } = mount();
    await turn(true);
    await findByText(/Could not ask the monitoring backend/);
    await waitFor(() => expect(helped()).toMatch(/not available/));
  });

  it("asks again on every switch-on, so an answer of 'not enabled' is not kept", async () => {
    const Sources = vi
      .fn()
      .mockResolvedValueOnce(sources({ status: "not-enabled", message: "", sources: [] }))
      .mockResolvedValue(sources());
    const Traffic = vi.fn().mockResolvedValue(layer);
    setTrafficBindings({ Sources, Traffic });
    const { turn, findByText } = mount();
    await turn(true);
    await findByText(/is off for this cluster/);
    // Enabled elsewhere; off and on again.
    await turn(false);
    await turn(true);
    await waitFor(() => expect(Sources).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(Traffic).toHaveBeenCalledTimes(1));
  });

  it("names a backend status in words, never by its code", async () => {
    setTrafficBindings({
      Sources: vi.fn().mockResolvedValue(sources({ status: "needs-credential", message: "401" })),
      Traffic: vi.fn(),
    });
    const { turn, findByText, container, helped } = mount();
    await turn(true);
    await findByText(/asks for a login of its own/);
    expect(container.textContent).not.toContain("needs-credential");
    expect(helped()).not.toContain("needs-credential");
  });
});
