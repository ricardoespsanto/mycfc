package static

import "embed"

// Files contains the generated, fingerprinted frontend assets.
//
//go:embed dist/*
var Files embed.FS
