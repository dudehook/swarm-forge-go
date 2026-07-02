// Package swarmforge (the module root) embeds the basic starter template so
// `swarmforge template install` can seed the user templates directory straight
// from the binary — no repo checkout required.
//
// Only the basic `coding-pair` template is embedded: it's the stable starter.
// The richer templates (four-pack/six-pack) are expected to evolve, so they live
// in repo templates/ but are not baked into the binary. This is the ONLY place
// templates are embedded; `init` and `templates` still read the on-disk user
// directory (see internal/scaffold), so installed templates remain user-editable.
package swarmforge

import (
	"embed"
	"io/fs"
)

//go:embed all:templates/coding-pair
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
