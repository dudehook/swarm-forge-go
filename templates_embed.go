// Package swarmforge (the module root) embeds the canonical swarm templates so
// `swarmforge template install` can seed the user templates directory straight
// from the binary — no repo checkout required.
//
// This is the ONLY place templates are embedded. `init` and `templates` still
// read the on-disk user directory (see internal/scaffold); the embed is just the
// install source, so installed templates remain user-editable.
package swarmforge

import (
	"embed"
	"io/fs"
)

//go:embed all:templates
var templatesFS embed.FS

// TemplatesFS returns the embedded templates tree rooted so each top-level entry
// is a template directory (e.g. "coding-pair/manifest.json").
func TemplatesFS() fs.FS {
	// The embed path is a compile-time constant, so Sub cannot fail here.
	sub, err := fs.Sub(templatesFS, "templates")
	if err != nil {
		panic(err)
	}
	return sub
}
