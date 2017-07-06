package dbkit

import (
	"os/exec"
	"testing"
)

func TestEmbedResources(t *testing.T) {
	// run go generate to create new embedded templates
	c := exec.Command("go", "generate")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Error(err, string(out))
		t.FailNow()
	}

	// run go install on dbkit to install
	c = exec.Command("go", "install")
	c.Dir = "dbkit"
	out, err = c.CombinedOutput()
	if err != nil {
		t.Error(err, string(out))
		t.FailNow()
	}
}
