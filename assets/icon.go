package assets

import _ "embed"

// IconPNG is the generated window icon, embedded so the binary is standalone.
//
//go:embed icon.png
var IconPNG []byte
