// Package web embeds the compiled React frontend.
//
// Before running `go build ./cmd/web`, populate frontend/dist by running:
//
//	go generate ./web/...
//
// or manually:
//
//	cd web/frontend && npm ci && npm run build
//
// The Dockerfile.web always rebuilds the frontend automatically via the Node stage.
package web

import "embed"

//go:generate sh -c "cd frontend && npm ci && npm run build"

//go:embed frontend/dist
var FS embed.FS
