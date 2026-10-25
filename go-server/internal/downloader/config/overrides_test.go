package config

import "testing"

func TestApplyOverrides(t *testing.T) {
	c := &Config{
		WorkDir:  "/container/work",
		StateDir: "/container/state",
		Systems: map[string]System{
			"switch": {Steps: []Step{
				{Type: "extract"},
				{Type: "switch_decompress", Binary: "/mnt/retroEMU/switch-tool", Keys: "/mnt/retroEMU/prod.keys"},
				{Type: "switch_merge", Binary: "/mnt/retroEMU/switch-tool", Keys: "/mnt/retroEMU/prod.keys"},
			}},
			"psx": {Steps: []Step{{Type: "extract"}}},
		},
	}
	c.ApplyOverrides(Overrides{
		SwitchTool: "/opt/romm/external-bins/switch-tool",
		SwitchKeys: "/opt/romm/resources/switch/prod.keys",
		WorkDir:    "/host/work",
	})

	if c.WorkDir != "/host/work" {
		t.Errorf("WorkDir = %q", c.WorkDir)
	}
	if c.StateDir != "/container/state" { // unchanged (no override)
		t.Errorf("StateDir = %q", c.StateDir)
	}
	sw := c.Systems["switch"].Steps
	if sw[0].Binary != "" {
		t.Errorf("extract step should be untouched, got binary %q", sw[0].Binary)
	}
	for _, i := range []int{1, 2} {
		if sw[i].Binary != "/opt/romm/external-bins/switch-tool" {
			t.Errorf("step %d binary = %q", i, sw[i].Binary)
		}
		if sw[i].Keys != "/opt/romm/resources/switch/prod.keys" {
			t.Errorf("step %d keys = %q", i, sw[i].Keys)
		}
	}
}
