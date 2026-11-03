"""NEUROFORGE X Python SDK.

A thin, dependency-free client for the NEUROFORGE X control plane. It lets you
build a multimodal workload programmatically, compile it to MIR, and execute it
on the Go runtime, then inspect real outputs, metrics, and the execution graph.

Example
-------
    import asyncio
    from neuroforge import NeuroForge

    nf = NeuroForge(base_url="http://localhost:8080")

    wl = nf.workload(name="warehouse-analysis")
    wl.input("video", "video", ref="warehouse.mp4")
    wl.add("frames",  "vision.frame_extract", backend="local",    inputs={"video": "$video"})
    wl.add("segment", "vision.segmentation",  backend="meta.sam",  inputs={"image": "frames.frames"})
    wl.add("features","vision.features",       backend="local",     inputs={"objects": "segment.objects"})
    wl.add("report",  "output.structured",     backend="local",     inputs={"data": "features.features"})
    wl.output("report")

    compiled = nf.compile(wl)
    result = asyncio.run(nf.execute(wl, optimize=True, cache=True, concurrency=4))
    print(result.outputs)
    print(result.metrics)
    print(result.execution_graph)
"""

from .client import NeuroForge, Workload, Result, CompileResult, NeuroForgeError

__all__ = ["NeuroForge", "Workload", "Result", "CompileResult", "NeuroForgeError"]
__version__ = "0.1.0"
