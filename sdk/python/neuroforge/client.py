"""NEUROFORGE X control-plane client (standard library only)."""

from __future__ import annotations

import asyncio
import json
import urllib.error
import urllib.request
from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional


class NeuroForgeError(Exception):
    """Raised when the control plane reports an error."""


@dataclass
class Workload:
    """A programmatically-built multimodal workload."""

    name: str
    inputs: List[Dict[str, Any]] = field(default_factory=list)
    operations: List[Dict[str, Any]] = field(default_factory=list)
    outputs: List[str] = field(default_factory=list)

    def input(self, name: str, type: str, ref: str = "") -> "Workload":
        spec: Dict[str, Any] = {"name": name, "type": type}
        if ref:
            spec["ref"] = ref
        self.inputs.append(spec)
        return self

    def add(
        self,
        id: str,
        operation: str,
        backend: str = "",
        inputs: Optional[Dict[str, str]] = None,
        params: Optional[Dict[str, Any]] = None,
        depends_on: Optional[List[str]] = None,
    ) -> "Workload":
        op: Dict[str, Any] = {"id": id, "type": operation}
        if backend:
            op["backend"] = backend
        if inputs:
            op["inputs"] = inputs
        if params:
            op["params"] = params
        if depends_on:
            op["depends_on"] = depends_on
        self.operations.append(op)
        return self

    def output(self, *ids: str) -> "Workload":
        self.outputs.extend(ids)
        return self

    def to_dict(self) -> Dict[str, Any]:
        return {
            "name": self.name,
            "inputs": self.inputs,
            "operations": self.operations,
            "outputs": self.outputs,
        }

    def to_json(self) -> str:
        # The control plane accepts JSON workloads (YAML is a superset of JSON).
        return json.dumps(self.to_dict())


@dataclass
class CompileResult:
    base: Dict[str, Any]
    optimized: Dict[str, Any]
    diagnostics: List[Dict[str, Any]]
    levels: List[List[str]]

    @property
    def passes(self) -> List[Dict[str, Any]]:
        return self.optimized.get("passes", []) if self.optimized else []


@dataclass
class Result:
    outputs: Dict[str, Any]
    metrics: Dict[str, Any]
    simulated: bool
    compiled: Dict[str, Any]
    error: str = ""

    @property
    def execution_graph(self) -> Dict[str, Any]:
        return self.compiled.get("optimized", {})

    @property
    def states(self) -> Dict[str, str]:
        return self.metrics.get("states", {})

    @property
    def total_ms(self) -> float:
        return self.metrics.get("profile", {}).get("total_ms", 0.0)


class NeuroForge:
    """Client for the NEUROFORGE X control plane."""

    def __init__(self, base_url: str = "http://localhost:8080", api_key: str = "", timeout: float = 130.0):
        # api_key configures the *server's* Meta credentials via its environment;
        # it is accepted here for parity with the documented interface.
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.timeout = timeout

    # -- builders --
    def workload(self, name: str) -> Workload:
        return Workload(name=name)

    # -- HTTP --
    def _post(self, path: str, body: Dict[str, Any]) -> Dict[str, Any]:
        data = json.dumps(body).encode("utf-8")
        req = urllib.request.Request(
            self.base_url + path, data=data, headers={"Content-Type": "application/json"}, method="POST"
        )
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                return json.loads(resp.read().decode("utf-8"))
        except urllib.error.URLError as e:  # pragma: no cover - network errors
            raise NeuroForgeError(f"control plane unreachable at {self.base_url}: {e}") from e

    def _get(self, path: str) -> Any:
        try:
            with urllib.request.urlopen(self.base_url + path, timeout=self.timeout) as resp:
                return json.loads(resp.read().decode("utf-8"))
        except urllib.error.URLError as e:  # pragma: no cover
            raise NeuroForgeError(f"control plane unreachable at {self.base_url}: {e}") from e

    # -- API --
    def operators(self) -> List[Dict[str, Any]]:
        return self._get("/api/operators")

    def backends(self) -> List[Dict[str, Any]]:
        return self._get("/api/backends")

    def health(self) -> Dict[str, Any]:
        return self._get("/api/health")

    def compile(self, workload: Workload, optimize: bool = True) -> CompileResult:
        resp = self._post("/api/compile", {"workload": workload.to_json(), "optimize": optimize})
        compiled = resp.get("compiled") or {}
        if resp.get("error"):
            raise NeuroForgeError(resp["error"])
        return CompileResult(
            base=compiled.get("base", {}),
            optimized=compiled.get("optimized", {}),
            diagnostics=compiled.get("diagnostics", []) or [],
            levels=resp.get("levels", []) or [],
        )

    def run(
        self, workload: Workload, optimize: bool = True, cache: bool = False, concurrency: int = 0
    ) -> Result:
        resp = self._post(
            "/api/run",
            {"workload": workload.to_json(), "optimize": optimize, "cache": cache, "concurrency": concurrency},
        )
        if resp.get("error"):
            raise NeuroForgeError(resp["error"])
        return Result(
            outputs=resp.get("outputs", {}) or {},
            metrics=resp.get("metrics", {}) or {},
            simulated=bool(resp.get("simulated")),
            compiled=resp.get("compiled", {}) or {},
            error=resp.get("error", ""),
        )

    async def execute(
        self, workload: Workload, optimize: bool = True, cache: bool = False, concurrency: int = 0, profile: bool = True
    ) -> Result:
        """Async wrapper around run() (profiling is always recorded)."""
        return await asyncio.to_thread(self.run, workload, optimize, cache, concurrency)
