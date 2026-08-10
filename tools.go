//go:build tools

// Package tools pins build-time tooling in the module graph so `go generate`
// is reproducible. Nothing here is compiled into the binary — the build tag
// keeps it out of every normal build — but the imports stop `go mod tidy` from
// dropping the generator's dependencies.
package tools

import (
	_ "github.com/Khan/genqlient"
)
