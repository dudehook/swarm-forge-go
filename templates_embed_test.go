package swarmforge

import (
	"io/fs"
	"testing"
)

// TestEmbeddedTemplates guards the go:embed directive: the binary must carry the
// basic coding-pair starter so `swarmforge template install` has something to
// install. The richer templates are intentionally NOT embedded.
func TestEmbeddedTemplates(t *testing.T) {
	src := TemplatesFS()
	if _, err := fs.Stat(src, "coding-pair/manifest.json"); err != nil {
		t.Errorf("embedded coding-pair missing manifest.json: %v", err)
	}
	if _, err := fs.Stat(src, "coding-pair/swarmforge/swarmforge.conf"); err != nil {
		t.Errorf("embedded coding-pair missing swarmforge.conf: %v", err)
	}
	for _, name := range []string{"four-pack", "six-pack"} {
		if _, err := fs.Stat(src, name+"/manifest.json"); err == nil {
			t.Errorf("template %q should NOT be embedded", name)
		}
	}
}
