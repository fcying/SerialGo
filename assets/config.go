package assets

import _ "embed"

// DefaultConfig is generated from the repository's config.toml before builds.
//
//go:embed default-config.toml
var DefaultConfig string
