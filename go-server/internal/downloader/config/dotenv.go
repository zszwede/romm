package config

import (
	"bufio"
	"os"
	"strings"
)

// LoadDotEnv reads KEY=VALUE pairs from the file at path into the process
// environment so config.yaml's ${VAR} references resolve from a .env file.
//
// Semantics match the common dotenv convention:
//   - a missing file is NOT an error (.env is optional);
//   - a variable already present in the real environment is left untouched,
//     so a container's env or a shell `export` always wins over the file;
//   - blank lines and `# comment` lines are ignored;
//   - an optional leading `export ` is allowed;
//   - values may be bare, or wrapped in single or double quotes (double
//     quotes honour \n and \" escapes); a ` #` inline comment after a bare
//     value is stripped.
//
// It is intentionally dependency-free and is not a full shell parser — enough
// for tokens, keys and URLs.
func LoadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimRight(sc.Text(), "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		if key == "" {
			continue
		}
		if _, ok := os.LookupEnv(key); ok {
			continue // real environment wins
		}
		if err := os.Setenv(key, dotenvValue(line[eq+1:])); err != nil {
			return err
		}
	}
	return sc.Err()
}

func dotenvValue(raw string) string {
	v := strings.TrimSpace(raw)
	if len(v) >= 2 {
		if v[0] == '"' && v[len(v)-1] == '"' {
			inner := v[1 : len(v)-1]
			inner = strings.ReplaceAll(inner, `\n`, "\n")
			inner = strings.ReplaceAll(inner, `\"`, `"`)
			return inner
		}
		if v[0] == '\'' && v[len(v)-1] == '\'' {
			return v[1 : len(v)-1]
		}
	}
	// Strip an inline comment that follows whitespace on a bare value.
	if i := strings.Index(v, " #"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return v
}
