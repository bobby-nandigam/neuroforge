package ir

import "sort"

// OperatorSpec is the static declaration of an operation type: its typed
// signature, default execution semantics, and the backends that can run it.
// The registry is the single source of truth the type checker and backend
// selector consult. Keeping semantics here (not in ad-hoc code) is what lets
// the optimizer reason safely about caching and reordering.
type OperatorSpec struct {
	Type    string
	Summary string
	Inputs  []Port
	Outputs []Port

	// Backends lists backend names that implement this operator. The first
	// entry is the default when a workload does not name one.
	Backends []string

	// Default semantics. An LLM reasoning op is non-deterministic and therefore
	// not cacheable unless the workload explicitly pins it deterministic.
	Deterministic bool
	SideEffects   bool
	Cacheable     bool
}

// registry maps operator type -> spec.
var registry = map[string]OperatorSpec{}

// Register adds an operator spec to the global registry. It panics on a
// duplicate so wiring mistakes surface at process start.
func Register(spec OperatorSpec) {
	if _, dup := registry[spec.Type]; dup {
		panic("ir: duplicate operator registration: " + spec.Type)
	}
	registry[spec.Type] = spec
}

// Lookup returns the operator spec for a type.
func Lookup(opType string) (OperatorSpec, bool) {
	s, ok := registry[opType]
	return s, ok
}

// Operators returns all registered operator specs sorted by type, for CLI and
// dashboard introspection.
func Operators() []OperatorSpec {
	out := make([]OperatorSpec, 0, len(registry))
	for _, s := range registry {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// SupportsBackend reports whether the operator type can run on the backend.
func (s OperatorSpec) SupportsBackend(backend string) bool {
	for _, b := range s.Backends {
		if b == backend {
			return true
		}
	}
	return false
}

// DefaultBackend returns the first (preferred) backend for the operator.
func (s OperatorSpec) DefaultBackend() string {
	if len(s.Backends) == 0 {
		return ""
	}
	return s.Backends[0]
}

// The standard operator catalog. These map directly onto the Meta AI
// integration surface described in the project spec plus a set of local
// operators the runtime executes itself.
//
// Backend naming convention:
//
//	meta.*  -> delegated to a Meta model API (real adapter, mock fallback)
//	local   -> executed by the NEUROFORGE X local operator backend
//	mock    -> deterministic stand-in for tests and credential-less demos
func init() {
	Register(OperatorSpec{
		Type:          "cognition.plan",
		Summary:       "Interpret a natural-language instruction into a structured execution plan (Muse Spark).",
		Inputs:        []Port{{Name: "instruction", Type: TypeText}},
		Outputs:       []Port{{Name: "plan", Type: TypeJSON}},
		Backends:      []string{"meta.muse_spark", "mock"},
		Deterministic: false,
		Cacheable:     false,
	})
	Register(OperatorSpec{
		Type:          "cognition.reasoning",
		Summary:       "Reason over observations and produce a structured interpretation (Muse Spark).",
		Inputs:        []Port{{Name: "observations", Type: TypeJSON}},
		Outputs:       []Port{{Name: "result", Type: TypeJSON}},
		Backends:      []string{"meta.muse_spark", "mock"},
		Deterministic: false,
		Cacheable:     false,
	})
	Register(OperatorSpec{
		Type:          "vision.segmentation",
		Summary:       "Segment an image into objects with masks and boxes (SAM 3.1).",
		Inputs:        []Port{{Name: "image", Type: TypeImage}},
		Outputs:       []Port{{Name: "objects", Type: TypeObjectList}, {Name: "mask", Type: TypeMask}},
		Backends:      []string{"meta.sam", "mock"},
		Deterministic: true,
		Cacheable:     true,
	})
	Register(OperatorSpec{
		Type:          "vision.frame_extract",
		Summary:       "Extract sampled frames from a video as images (local operator).",
		Inputs:        []Port{{Name: "video", Type: TypeVideo}},
		Outputs:       []Port{{Name: "frames", Type: TypeImage}},
		Backends:      []string{"local", "mock"},
		Deterministic: true,
		Cacheable:     true,
	})
	Register(OperatorSpec{
		Type:          "vision.features",
		Summary:       "Normalize detections into spatial feature vectors (local operator).",
		Inputs:        []Port{{Name: "objects", Type: TypeObjectList}},
		Outputs:       []Port{{Name: "features", Type: TypeJSON}},
		Backends:      []string{"local", "mock"},
		Deterministic: true,
		Cacheable:     true,
	})
	Register(OperatorSpec{
		Type:          "image.generate",
		Summary:       "Generate an illustrative image from a prompt (Muse Image).",
		Inputs:        []Port{{Name: "prompt", Type: TypeText}},
		Outputs:       []Port{{Name: "image", Type: TypeImage}},
		Backends:      []string{"meta.muse_image", "mock"},
		Deterministic: false,
		Cacheable:     false,
	})
	Register(OperatorSpec{
		Type:          "output.structured",
		Summary:       "Assemble upstream results into a final structured report (local operator).",
		Inputs:        []Port{{Name: "data", Type: TypeJSON}},
		Outputs:       []Port{{Name: "report", Type: TypeJSON}},
		Backends:      []string{"local", "mock"},
		Deterministic: true,
		Cacheable:     true,
	})
}
