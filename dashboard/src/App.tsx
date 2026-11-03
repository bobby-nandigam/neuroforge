import { useEffect, useMemo, useState } from "react";
import ReactFlow, { Background, Controls, type Edge, type Node } from "reactflow";
import "reactflow/dist/style.css";
import { BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer, Cell } from "recharts";
import { api, type Backend, type Compiled, type RunResponse } from "./api";
import { toFlow } from "./graph";

const SAMPLE = `name: warehouse-intelligence
inputs:
  - { name: video, type: video, ref: warehouse.mp4 }
  - { name: instruction, type: text, ref: "Analyze this warehouse video and report objects." }
operations:
  - { id: plan,     type: cognition.plan,       backend: meta.muse_spark, inputs: { instruction: $instruction } }
  - { id: frames,   type: vision.frame_extract, backend: local,           inputs: { video: $video } }
  - { id: segment,  type: vision.segmentation,  backend: meta.sam,        inputs: { image: frames.frames } }
  - { id: features, type: vision.features,      backend: local,           inputs: { objects: segment.objects } }
  - { id: reason,   type: cognition.reasoning,  backend: meta.muse_spark, inputs: { observations: features.features } }
  - { id: report,   type: output.structured,    backend: local,           inputs: { data: reason.result } }
  - { id: summarize,type: image.generate,       backend: meta.muse_image, inputs: { prompt: $instruction } }
outputs: [plan, report, summarize]
`;

type Tab = "graph" | "profiler" | "mir" | "passes" | "registry";

export default function App() {
  const [workload, setWorkload] = useState(SAMPLE);
  const [optimize, setOptimize] = useState(true);
  const [cache, setCache] = useState(true);
  const [concurrency, setConcurrency] = useState(4);
  const [compiled, setCompiled] = useState<Compiled | null>(null);
  const [levels, setLevels] = useState<string[][]>([]);
  const [run, setRun] = useState<RunResponse | null>(null);
  const [backends, setBackends] = useState<Backend[]>([]);
  const [health, setHealth] = useState<{ meta_configured: boolean; base_url: string } | null>(null);
  const [tab, setTab] = useState<Tab>("graph");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  useEffect(() => {
    api.health().then(setHealth).catch(() => setHealth(null));
    api.backends().then(setBackends).catch(() => {});
  }, []);

  async function doCompile() {
    setBusy(true); setErr(""); setRun(null);
    try {
      const r = await api.compile(workload, optimize);
      setCompiled(r.compiled);
      setLevels(r.levels ?? []);
      if (r.error) setErr(r.error);
    } catch (e) { setErr(String(e)); } finally { setBusy(false); }
  }

  async function doRun() {
    setBusy(true); setErr("");
    try {
      const r = await api.run(workload, optimize, cache, concurrency);
      setRun(r);
      setCompiled(r.compiled);
      setLevels((r as any).levels ?? levels);
      if (r.error) setErr(r.error);
    } catch (e) { setErr(String(e)); } finally { setBusy(false); }
  }

  const mod = compiled?.optimized ?? null;
  const states = run?.metrics.states ?? {};
  const flow = useMemo<{ nodes: Node[]; edges: Edge[] }>(
    () => (mod ? toFlow(mod, levels, states) : { nodes: [], edges: [] }),
    [mod, levels, run]
  );

  return (
    <div className="min-h-screen flex flex-col">
      {/* Header */}
      <header className="border-b border-graphite-600 px-5 py-3 flex items-center gap-4">
        <div className="text-cyan font-bold tracking-tight text-lg">NEUROFORGE&nbsp;X</div>
        <div className="text-slate-500 text-xs">Compile Intelligence. Optimize Execution.</div>
        <div className="ml-auto flex items-center gap-3 text-xs">
          <StatusDot ok={!!health} label={health ? "control plane" : "offline"} />
          <StatusDot ok={!!health?.meta_configured} label={health?.meta_configured ? "Meta API" : "mock backend"} />
        </div>
      </header>

      <div className="flex flex-1 min-h-0">
        {/* Left: editor + controls */}
        <aside className="w-[380px] border-r border-graphite-600 flex flex-col">
          <div className="px-4 py-2 text-xs uppercase tracking-wider text-slate-500">Compiler Studio</div>
          <textarea
            className="flex-1 bg-graphite-900 text-slate-300 text-xs p-3 outline-none resize-none border-y border-graphite-700 leading-relaxed"
            spellCheck={false}
            value={workload}
            onChange={(e) => setWorkload(e.target.value)}
          />
          <div className="p-3 space-y-3 text-xs">
            <div className="flex items-center gap-4">
              <Toggle label="optimize" v={optimize} set={setOptimize} />
              <Toggle label="cache" v={cache} set={setCache} />
              <label className="flex items-center gap-1">
                concurrency
                <input type="number" min={1} max={16} value={concurrency}
                  onChange={(e) => setConcurrency(+e.target.value)}
                  className="w-12 bg-graphite-700 rounded px-1 py-0.5" />
              </label>
            </div>
            <div className="flex gap-2">
              <button onClick={doCompile} disabled={busy}
                className="flex-1 bg-graphite-700 hover:bg-graphite-600 border border-violet/40 text-violet rounded py-1.5">
                Compile
              </button>
              <button onClick={doRun} disabled={busy}
                className="flex-1 bg-cyan/10 hover:bg-cyan/20 border border-cyan/50 text-cyan rounded py-1.5">
                {busy ? "…" : "Run"}
              </button>
            </div>
            {err && <div className="text-red-400 whitespace-pre-wrap text-[11px]">{err}</div>}
            {run?.simulated && (
              <div className="text-amber-400/80 text-[11px]">
                ⚠ simulated: Meta credentials absent — outputs produced by the mock backend.
              </div>
            )}
            <Summary compiled={compiled} run={run} />
          </div>
        </aside>

        {/* Center: tabs */}
        <main className="flex-1 flex flex-col min-w-0">
          <nav className="flex gap-1 px-3 pt-2 text-xs border-b border-graphite-600">
            {(["graph", "profiler", "mir", "passes", "registry"] as Tab[]).map((t) => (
              <button key={t} onClick={() => setTab(t)}
                className={`px-3 py-1.5 rounded-t ${tab === t ? "bg-graphite-700 text-cyan" : "text-slate-500 hover:text-slate-300"}`}>
                {t}
              </button>
            ))}
          </nav>
          <div className="flex-1 min-h-0 bg-graphite-900">
            {tab === "graph" && <GraphPanel flow={flow} hasMod={!!mod} />}
            {tab === "profiler" && <Profiler run={run} />}
            {tab === "mir" && <Json obj={mod ?? { hint: "Compile a workload" }} />}
            {tab === "passes" && <Passes compiled={compiled} />}
            {tab === "registry" && <Registry backends={backends} />}
          </div>
        </main>
      </div>
    </div>
  );
}

function GraphPanel({ flow, hasMod }: { flow: { nodes: Node[]; edges: Edge[] }; hasMod: boolean }) {
  if (!hasMod) return <Empty>Compile a workload to see the computation graph.</Empty>;
  return (
    <ReactFlow nodes={flow.nodes} edges={flow.edges} fitView minZoom={0.2}
      proOptions={{ hideAttribution: true }}>
      <Background color="#1a1f27" gap={20} />
      <Controls className="!bg-graphite-700 !border-graphite-600" />
    </ReactFlow>
  );
}

function Profiler({ run }: { run: RunResponse | null }) {
  if (!run) return <Empty>Run a workload to see execution metrics.</Empty>;
  const p = run.metrics.profile;
  const data = p.nodes.map((n) => ({ name: n.op, ms: +n.duration_ms.toFixed(1), backend: n.backend, hit: n.cache_hit }));
  return (
    <div className="p-4 space-y-4 overflow-auto h-full text-xs">
      <div className="grid grid-cols-4 gap-3">
        <Stat label="total wall" value={`${p.total_ms.toFixed(1)} ms`} accent />
        <Stat label="serial-equiv" value={`${p.sum_node_ms.toFixed(1)} ms`} />
        <Stat label="critical path" value={`${p.critical_path_ms.toFixed(1)} ms`} />
        <Stat label="speedup" value={`${(p.sum_node_ms / Math.max(p.total_ms, 0.01)).toFixed(2)}×`} accent />
        <Stat label="completed" value={`${p.completed}/${p.node_count}`} />
        <Stat label="cache hits" value={`${p.cache_hits}`} />
        <Stat label="retries" value={`${p.retries}`} />
        <Stat label="concurrency" value={`${run.metrics.concurrency}`} />
      </div>
      <div className="h-56 bg-graphite-800 rounded border border-graphite-600 p-2">
        <ResponsiveContainer width="100%" height="100%">
          <BarChart data={data}>
            <XAxis dataKey="name" tick={{ fill: "#94a3b8", fontSize: 10 }} />
            <YAxis tick={{ fill: "#94a3b8", fontSize: 10 }} unit="ms" />
            <Tooltip contentStyle={{ background: "#12151a", border: "1px solid #232a34", fontSize: 11 }} />
            <Bar dataKey="ms" radius={[3, 3, 0, 0]}>
              {data.map((d, i) => (
                <Cell key={i} fill={d.hit ? "#34d399" : "#22d3ee"} />
              ))}
            </Bar>
          </BarChart>
        </ResponsiveContainer>
      </div>
      <table className="w-full text-left">
        <thead className="text-slate-500">
          <tr><th>op</th><th>type</th><th>backend</th><th>queue</th><th>dur</th><th>tries</th><th>state</th></tr>
        </thead>
        <tbody>
          {p.nodes.map((n) => (
            <tr key={n.op} className="border-t border-graphite-700">
              <td className="text-cyan">{n.op}</td><td className="text-slate-400">{n.type}</td>
              <td className="text-violet">{n.backend}</td><td>{n.queue_wait_ms.toFixed(1)}ms</td>
              <td>{n.duration_ms.toFixed(1)}ms{n.cache_hit ? " ⚡" : ""}</td><td>{n.attempts}</td>
              <td className="text-emerald-400">{n.state}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Passes({ compiled }: { compiled: Compiled | null }) {
  if (!compiled) return <Empty>Compile a workload to see optimizer passes.</Empty>;
  const base = compiled.base.operations.length;
  const opt = compiled.optimized.operations.length;
  return (
    <div className="p-4 space-y-3 text-xs overflow-auto h-full">
      <div className="flex gap-6">
        <Stat label="base ops" value={`${base}`} />
        <Stat label="optimized ops" value={`${opt}`} accent />
        <Stat label="eliminated" value={`${base - opt}`} />
      </div>
      {(compiled.optimized.passes ?? []).map((p, i) => (
        <div key={i} className="bg-graphite-800 border border-graphite-600 rounded p-3">
          <div className={p.changed ? "text-cyan" : "text-slate-500"}>{p.changed ? "✓" : "·"} {p.name}</div>
          {(p.notes ?? []).map((n, j) => <div key={j} className="text-slate-400 mt-1">{n}</div>)}
        </div>
      ))}
      {compiled.diagnostics?.length > 0 && (
        <div className="bg-graphite-800 border border-graphite-600 rounded p-3">
          <div className="text-slate-500 mb-1">diagnostics</div>
          {compiled.diagnostics.map((d, i) => (
            <div key={i} className={d.severity === "error" ? "text-red-400" : "text-amber-400/80"}>
              [{d.severity}] {d.op ? `${d.op}: ` : ""}{d.message}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function Registry({ backends }: { backends: Backend[] }) {
  return (
    <div className="p-4 grid grid-cols-2 gap-3 text-xs overflow-auto h-full">
      {backends.map((b) => (
        <div key={b.name} className="bg-graphite-800 border border-graphite-600 rounded p-3">
          <div className="flex items-center justify-between">
            <span className="text-violet font-bold">{b.name}</span>
            <StatusDot ok={b.configured} label={b.configured ? "configured" : "mock-fallback"} />
          </div>
          <div className="mt-2 flex flex-wrap gap-1">
            {b.capabilities.map((c) => (
              <span key={c} className="bg-graphite-700 text-slate-400 rounded px-1.5 py-0.5">{c}</span>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

// -- small components --
function Empty({ children }: { children: React.ReactNode }) {
  return <div className="h-full flex items-center justify-center text-slate-600 text-sm">{children}</div>;
}
function Json({ obj }: { obj: unknown }) {
  return <pre className="p-4 text-[11px] text-slate-400 overflow-auto h-full">{JSON.stringify(obj, null, 2)}</pre>;
}
function Stat({ label, value, accent }: { label: string; value: string; accent?: boolean }) {
  return (
    <div className="bg-graphite-800 border border-graphite-600 rounded px-3 py-2">
      <div className="text-slate-500 text-[10px] uppercase tracking-wider">{label}</div>
      <div className={`text-base font-bold ${accent ? "text-cyan" : "text-slate-200"}`}>{value}</div>
    </div>
  );
}
function Toggle({ label, v, set }: { label: string; v: boolean; set: (b: boolean) => void }) {
  return (
    <label className="flex items-center gap-1 cursor-pointer">
      <input type="checkbox" checked={v} onChange={(e) => set(e.target.checked)} />
      {label}
    </label>
  );
}
function StatusDot({ ok, label }: { ok: boolean; label: string }) {
  return (
    <span className="flex items-center gap-1 text-slate-400">
      <span className={`w-2 h-2 rounded-full ${ok ? "bg-emerald-400" : "bg-slate-600"}`} />
      {label}
    </span>
  );
}
function Summary({ compiled, run }: { compiled: Compiled | null; run: RunResponse | null }) {
  if (!compiled) return null;
  return (
    <div className="text-slate-500 text-[11px] space-y-0.5">
      <div>ops: {compiled.base.operations.length} → {compiled.optimized.operations.length}</div>
      {run && <div>wall: {run.metrics.profile.total_ms.toFixed(1)} ms · {run.metrics.profile.completed}/{run.metrics.profile.node_count} done</div>}
    </div>
  );
}
