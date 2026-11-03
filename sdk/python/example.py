"""Run the warehouse pipeline through the NEUROFORGE X Python SDK.

Start the control plane first:  make server   (listens on :8080)
Then:                           python sdk/python/example.py
"""

import asyncio
import os

from neuroforge import NeuroForge


def build(nf: NeuroForge):
    wl = nf.workload(name="warehouse-analysis")
    wl.input("video", "video", ref="warehouse.mp4")
    wl.input("instruction", "text", ref="Analyze this warehouse video and report objects.")
    wl.add("plan", "cognition.plan", backend="meta.muse_spark", inputs={"instruction": "$instruction"})
    wl.add("frames", "vision.frame_extract", backend="local", inputs={"video": "$video"})
    wl.add("segment", "vision.segmentation", backend="meta.sam", inputs={"image": "frames.frames"})
    wl.add("features", "vision.features", backend="local", inputs={"objects": "segment.objects"})
    wl.add("reason", "cognition.reasoning", backend="meta.muse_spark", inputs={"observations": "features.features"})
    wl.add("report", "output.structured", backend="local", inputs={"data": "reason.result"})
    wl.output("plan", "report")
    return wl


async def main():
    nf = NeuroForge(base_url=os.environ.get("NEUROFORGE_URL", "http://localhost:8080"))
    print("health:", nf.health())

    wl = build(nf)

    compiled = nf.compile(wl, optimize=True)
    print(f"\ncompiled: {len(compiled.base['operations'])} -> {len(compiled.optimized['operations'])} ops")
    print("levels:", compiled.levels)

    result = await nf.execute(wl, optimize=True, cache=True, concurrency=4)
    print(f"\nsimulated={result.simulated}  total={result.total_ms:.1f} ms")
    print("states:", result.states)
    print("report output keys:", list(result.outputs.get("report", {}).keys()))


if __name__ == "__main__":
    asyncio.run(main())
