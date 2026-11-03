// Convert an MIR module into React Flow nodes/edges with a layered layout
// derived from the compiler's execution levels.

import type { Edge, Node } from "reactflow";
import type { Module } from "./api";

const BACKEND_COLOR: Record<string, string> = {
  local: "#22d3ee",
  mock: "#64748b",
  "meta.sam": "#a78bfa",
  "meta.muse_spark": "#f472b6",
  "meta.muse_image": "#fbbf24",
};

const STATE_COLOR: Record<string, string> = {
  COMPLETED: "#34d399",
  RUNNING: "#22d3ee",
  FAILED: "#f87171",
  BLOCKED: "#fbbf24",
  CANCELLED: "#9ca3af",
  READY: "#60a5fa",
  PENDING: "#475569",
};

export function toFlow(
  mod: Module,
  levels: string[][],
  states: Record<string, string>
): { nodes: Node[]; edges: Edge[] } {
  const levelOf: Record<string, number> = {};
  levels.forEach((lvl, i) => lvl.forEach((id) => (levelOf[id] = i)));

  const perLevelIndex: Record<number, number> = {};
  const nodes: Node[] = mod.operations.map((op) => {
    const lv = levelOf[op.id] ?? 0;
    const idx = (perLevelIndex[lv] = (perLevelIndex[lv] ?? 0) + 1) - 1;
    const state = states[op.id];
    const border = state ? STATE_COLOR[state] ?? "#475569" : BACKEND_COLOR[op.backend] ?? "#475569";
    const isOutput = (mod.outputs ?? []).includes(op.id);
    return {
      id: op.id,
      position: { x: lv * 240, y: idx * 110 },
      data: {
        label: (
          <div style={{ minWidth: 150 }}>
            <div style={{ fontWeight: 700, color: BACKEND_COLOR[op.backend] ?? "#e2e8f0" }}>{op.id}</div>
            <div style={{ fontSize: 10, color: "#94a3b8" }}>{op.type}</div>
            <div style={{ fontSize: 10, color: "#64748b" }}>@{op.backend}</div>
            {state && (
              <div style={{ fontSize: 10, marginTop: 4, color: STATE_COLOR[state] ?? "#94a3b8" }}>● {state}</div>
            )}
            {isOutput && <div style={{ fontSize: 9, color: "#34d399" }}>output</div>}
          </div>
        ),
      },
      style: {
        background: "#12151a",
        border: `1.5px solid ${border}`,
        borderRadius: 8,
        padding: 8,
        color: "#e2e8f0",
      },
    } as Node;
  });

  const edges: Edge[] = [];
  const seen = new Set<string>();
  for (const op of mod.operations) {
    const srcs = new Set<string>();
    (op.depends_on ?? []).forEach((d) => srcs.add(d));
    (op.bindings ?? []).forEach((b) => {
      if (b.source && b.source[0] !== "$") srcs.add(b.source.split(".")[0]);
    });
    srcs.forEach((s) => {
      const key = `${s}->${op.id}`;
      if (seen.has(key)) return;
      seen.add(key);
      edges.push({
        id: key,
        source: s,
        target: op.id,
        animated: states[op.id] === "RUNNING",
        style: { stroke: "#334155" },
      });
    });
  }
  return { nodes, edges };
}
