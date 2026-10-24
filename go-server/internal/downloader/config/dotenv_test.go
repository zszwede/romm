package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	content := "# comment line\n" +
		"export A=1\n" +
		"B=\"hello world\"\n" +
		"C='raw#val'\n" +
		"D=bare # trailing comment\n" +
		"PRESET=fromfile\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	// A variable already in the real environment must not be overridden.
	t.Setenv("PRESET", "fromenv")
	for _, k := range []string{"A", "B", "C", "D"} {
		t.Cleanup(func() { os.Unsetenv(k) })
	}

	if err := LoadDotEnv(p); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"A":      "1",
		"B":      "hello world",
		"C":      "raw#val",
		"D":      "bare",
		"PRESET": "fromenv",
	}
	for k, w := range want {
		if got := os.Getenv(k); got != w {
			t.Errorf("%s = %q, want %q", k, got, w)
		}
	}

	// A missing file is not an error.
	if err := LoadDotEnv(filepath.Join(dir, "nope.env")); err != nil {
		t.Errorf("missing file should be nil, got %v", err)
	}
}
