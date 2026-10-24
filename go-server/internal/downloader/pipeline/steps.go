package pipeline

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/zszwede/romm-go/internal/downloader/config"
	"github.com/zszwede/romm-go/internal/downloader/fsutil"
)

func lookPath(bin string) (string, error) {
	p, err := exec.LookPath(bin)
	if err != nil {
		return "", fmt.Errorf("binary %q not found: %w", bin, err)
	}
	return p, nil
}

func compileAll(pats []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(pats))
	for _, p := range pats {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("bad regexp %q: %w", p, err)
		}
		out = append(out, re)
	}
	return out, nil
}

func matchAny(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// filter: keep files whose name matches include (all when empty) and not
// exclude. Dropped files are deleted.
// ---------------------------------------------------------------------------

type filter struct {
	include, exclude []*regexp.Regexp
	require          bool
}

func newFilter(c config.Step) (Step, error) {
	inc, err := compileAll(c.Include)
	if err != nil {
		return nil, err
	}
	exc, err := compileAll(c.Exclude)
	if err != nil {
		return nil, err
	}
	return &filter{include: inc, exclude: exc, require: c.RequireMatch}, nil
}

func (f *filter) Run(_ context.Context, job *Job, _ string, files []string) ([]string, error) {
	var out []string
	for _, p := range files {
		n := filepath.Base(p)
		keep := (len(f.include) == 0 || matchAny(f.include, n)) && !matchAny(f.exclude, n)
		if keep {
			out = append(out, p)
		} else {
			job.Log.Info("filter: dropped", "file", n)
			os.Remove(p)
		}
	}
	if f.require && len(out) == 0 {
		return nil, fmt.Errorf("no files left after filter (had %s)", baseNames(files))
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// exec: run any program. The extension point for platforms with their own
// tools (chdman, dolphin-tool, ...).
// ---------------------------------------------------------------------------

type execStep struct {
	bin     string
	args    []string
	perFile bool
	outputs string
	env     map[string]string
	timeout time.Duration
	include []*regexp.Regexp
	delete  bool
}

func newExec(c config.Step) (Step, error) {
	if c.Binary == "" {
		return nil, fmt.Errorf("exec needs binary")
	}
	if _, err := lookPath(c.Binary); err != nil {
		return nil, err
	}
	switch c.Outputs {
	case "", "outdir", "inputs", "both":
	default:
		return nil, fmt.Errorf("bad outputs %q", c.Outputs)
	}
	inc, err := compileAll(c.Include)
	if err != nil {
		return nil, err
	}
	out := c.Outputs
	if out == "" {
		out = "outdir"
	}
	return &execStep{
		bin: c.Binary, args: c.Args, perFile: c.PerFile, outputs: out, env: c.Env,
		timeout: c.Timeout, include: inc,
		// replaced inputs go away by default; "inputs"/"both" keep them
		delete: boolOr(c.DeleteInputs, out == "outdir"),
	}, nil
}

func (e *execStep) Run(ctx context.Context, job *Job, dir string, files []string) ([]string, error) {
	// files not matching include (when set) skip the command and pass through
	var targets, pass []string
	for _, f := range files {
		if len(e.include) == 0 || matchAny(e.include, filepath.Base(f)) {
			targets = append(targets, f)
		} else {
			pass = append(pass, f)
		}
	}
	if len(targets) == 0 {
		return files, nil
	}
	outDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	run := func(input string, inputs []string) error {
		data := job.TemplateData()
		data["Input"] = input
		data["Inputs"] = inputs
		data["OutDir"] = outDir
		args := make([]string, 0, len(e.args))
		for _, a := range e.args {
			s, err := Render(a, data)
			if err != nil {
				return fmt.Errorf("arg %q: %w", a, err)
			}
			args = append(args, s)
		}
		_, _, err := runCmd(ctx, job.Log, e.timeout, e.env, e.bin, args...)
		return err
	}
	if e.perFile {
		for _, f := range targets {
			if err := run(f, []string{f}); err != nil {
				return nil, err
			}
		}
	} else if err := run("", targets); err != nil {
		return nil, err
	}

	out := append([]string(nil), pass...)
	if e.outputs == "inputs" || e.outputs == "both" {
		out = append(out, targets...)
	}
	if e.outputs == "outdir" || e.outputs == "both" {
		got, err := fsutil.ListFiles(outDir)
		if err != nil {
			return nil, err
		}
		if len(got) == 0 {
			return nil, fmt.Errorf("%s produced no files in %s", filepath.Base(e.bin), outDir)
		}
		out = append(out, got...)
		if e.delete {
			fsutil.RemoveAll(targets)
		}
	}
	return out, nil
}
