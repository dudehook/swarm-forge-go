package swarmforge

import (
	"io/fs"
	"testing"
)

// TestEmbeddedTemplates guards the go:embed directive: the binary must carry the
// canonical templates so `swarmforge template install` has something to install.
func TestEmbeddedTemplates(t *testing.T) {
	src := TemplatesFS()
	for _, name := range []string{"coding-pair", "four-pack", "six-pack"} {
		if _, err := fs.Stat(src, name+"/manifest.json"); err != nil {
			t.Errorf("embedded template %q missing manifest.json: %v", name, err)
		}
		if _, err := fs.Stat(src, name+"/swarmforge/swarmforge.conf"); err != nil {
			t.Errorf("embedded template %q missing swarmforge.conf: %v", name, err)
		}
	}
}
