// Package web embeds the citizen and dispatcher single-page apps so the
// binary (and the Docker image) is fully self-contained.
package web

import "embed"

//go:embed citizen.html dispatcher.html static
var FS embed.FS
