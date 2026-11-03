// Typed client for the NEUROFORGE X control plane.

export type Port = { name: string; type: string };

export type Operation = {
  id: string;
  type: string;
  backend: string;
  depends_on?: string[];
  bindings?: { port: string; source: string }[];
  inputs?: Port[];
  outputs?: Port[];
  deterministic: boolean;
  side_effects: boolean;
  cacheable: boolean;
};

export type PassRecord = { name: string; changed: boolean; notes?: string[] };

export type Module = {
  schema_version: string;
  name: string;
  inputs?: { name: string; type: string; ref?: string }[];
  operations: Operation[];
  outputs?: string[];
  passes?: PassRecord[];
};

export type Diagnostic = { severity: string; op?: string; message: string };

export type Compiled = {
  base: Module;
  optimized: Module;
  diagnostics: Diagnostic[];
};

export type NodeProfile = {
  op: string;
  type: string;
  backend: string;
  state: string;
  attempts: number;
  cache_hit: boolean;
  queue_wait_ms: number;
  duration_ms: number;
};

export type Metrics = {
  profile: {
    total_ms: number;
    nodes: NodeProfile[];
    node_count: number;
    completed: number;
    failed: number;
    cache_hits: number;
    retries: number;
    sum_node_ms: number;
    critical_path_ms: number;
  };
  cache?: { hits: number; misses: number; hit_rate: number; evictions: number; size: number; capacity: number };
  concurrency: number;
  states: Record<string, string>;
};

export type RunResponse = {
  compiled: Compiled;
  simulated: boolean;
  outputs: Record<string, Record<string, { type: string; data?: unknown; ref?: string; meta?: Record<string, unknown> }>>;
  metrics: Metrics;
  error: string;
};

export type Backend = { name: string; configured: boolean; capabilities: string[] };

async function post<T>(path: string, body: unknown): Promise<T> {
  const r = await fetch(path, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
  return (await r.json()) as T;
}
async function get<T>(path: string): Promise<T> {
  const r = await fetch(path);
  return (await r.json()) as T;
}

export const api = {
  health: () => get<{ status: string; meta_configured: boolean; base_url: string }>("/api/health"),
  backends: () => get<Backend[]>("/api/backends"),
  operators: () => get<any[]>("/api/operators"),
  compile: (workload: string, optimize = true) =>
    post<{ compiled: Compiled; levels: string[][]; error?: string }>("/api/compile", { workload, optimize }),
  run: (workload: string, optimize: boolean, cache: boolean, concurrency: number) =>
    post<RunResponse & { levels?: string[][] }>("/api/run", { workload, optimize, cache, concurrency }),
};
