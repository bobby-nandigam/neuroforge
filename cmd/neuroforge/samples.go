package main

const sampleWorkload = `# NEUROFORGE X — Cognitive Video Intelligence Pipeline
# Analyze a warehouse video: plan the work, segment objects, extract spatial
# features, reason over the observations, and assemble a structured report.
name: warehouse-intelligence

inputs:
  - name: video
    type: video
    ref: examples/warehouse-intelligence/warehouse.mp4
  - name: instruction
    type: text
    ref: "Analyze this warehouse video, identify objects, understand the scene, and generate a structured inspection report."

operations:
  # Compiler frontend: Muse Spark turns the instruction into a structured plan.
  - id: plan
    type: cognition.plan
    backend: meta.muse_spark
    inputs: { instruction: $instruction }

  # Local operator: frame sampling metadata.
  - id: frames
    type: vision.frame_extract
    backend: local
    params: { fps: 1 }
    inputs: { video: $video }

  # Visual perception: SAM 3.1 segments objects.
  - id: segment
    type: vision.segmentation
    backend: meta.sam
    inputs: { image: frames.frames }

  # Local operator: measured spatial features (observations, not inferences).
  - id: features
    type: vision.features
    backend: local
    inputs: { objects: segment.objects }

  # Cognition: Muse Spark interprets the measured observations.
  - id: reason
    type: cognition.reasoning
    backend: meta.muse_spark
    inputs: { observations: features.features }

  # Local operator: assemble the final structured report.
  - id: report
    type: output.structured
    backend: local
    params: { title: "Warehouse inspection report" }
    inputs: { data: reason.result }

  # Optional visual summary via Muse Image.
  - id: summarize
    type: image.generate
    backend: meta.muse_image
    inputs: { prompt: $instruction }

outputs: [plan, report, summarize]
`

const envExample = `# NEUROFORGE X — Meta Model API configuration
# The runtime works without these using a clearly-labeled deterministic mock.
# Set them to execute against the real Meta Model API (OpenAI-compatible).

# Required for any real Meta call:
META_API_KEY=

# OpenAI-compatible base URL (default https://api.meta.ai/v1):
META_API_BASE_URL=https://api.meta.ai/v1

# Model identifiers — confirm exact IDs against the Meta developer portal:
MUSE_SPARK_MODEL=muse-spark
MUSE_IMAGE_MODEL=muse-image

# Non-chat endpoint paths — required to enable the real SAM / Muse Image
# backends (otherwise those operators use the mock). Confirm against the docs:
SAM_API_PATH=
MUSE_IMAGE_API_PATH=

# Optional per-request timeout in milliseconds (default 60000):
META_API_TIMEOUT_MS=60000
`
