// Package setup wires the default NEUROFORGE X backend registry: local and mock
// backends plus Meta adapters (Muse Spark, SAM, Muse Image) fronted by a
// Fallback so workloads run without credentials, using the mock with clearly
// labeled simulated output, and automatically use real Meta APIs when
// configured via environment variables.
package setup

import (
	"neuroforge/backends"
	"neuroforge/backends/local"
	"neuroforge/backends/meta/client"
	"neuroforge/backends/meta/muse_image"
	"neuroforge/backends/meta/muse_spark"
	"neuroforge/backends/meta/sam"
	"neuroforge/backends/mock"
)

// Env bundles the assembled runtime environment.
type Env struct {
	Registry *backends.Registry
	Gateway  *client.Gateway
}

// Default builds the standard registry and Meta gateway from the environment.
func Default() *Env {
	gw := client.New(client.FromEnv())
	mk := mock.New()

	reg := backends.NewRegistry()
	reg.Register(mk)
	reg.Register(local.New())
	// Meta adapters, each with a mock fallback under the same name.
	reg.Register(backends.NewFallback("meta.muse_spark", muse_spark.New(gw), mk))
	reg.Register(backends.NewFallback("meta.sam", sam.New(gw), mk))
	reg.Register(backends.NewFallback("meta.muse_image", muse_image.New(gw), mk))

	return &Env{Registry: reg, Gateway: gw}
}
