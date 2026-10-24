// Package pipeline turns the downloaded files of a job into the final files,
// through a per-system list of configurable steps.
//
// Each step receives the current file set and returns the new one. Steps
// write into their own directory below the job work dir, which is removed
// when the job is done - so files a step replaces (archives, merged parts)
// disappear with it, or right away when the step deletes its inputs.
package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/zszwede/romm-go/internal/downloader/config"
)

// Job is what steps know about the job they run for.
type Job struct {
	ID       string
	System   string
	Name     string // optional display name from the request
	Metadata map[string]any
	WorkDir  string
	Log      *slog.Logger
}

// TemplateData is exposed to every template (target_dir, file_name, exec args).
func (j *Job) TemplateData() map[string]any {
	return map[string]any{
		"JobID":    j.ID,
		"System":   j.System,
		"Name":     j.Name,
		"Metadata": j.Metadata,
	}
}

type Step interface {
	Run(ctx context.Context, job *Job, dir string, files []string) ([]string, error)
}

type namedStep struct {
	label string
	step  Step
}

// Pipeline is the compiled step list of one system.
type Pipeline struct {
	steps []namedStep
}

// factories lets new step types be registered (e.g. per-platform tools).
var factories = map[string]func(config.Step) (Step, error){
	"extract":           newExtract,
	"filter":            newFilter,
	"switch_decompress": newSwitchDecompress,
	"switch_merge":      newSwitchMerge,
	"exec":              newExec,
}

// Register adds a step type.
func Register(name string, f func(config.Step) (Step, error)) { factories[name] = f }

func Build(steps []config.Step) (*Pipeline, error) {
	p := &Pipeline{}
	for i, sc := range steps {
		f, ok := factories[sc.Type]
		if !ok {
			return nil, fmt.Errorf("step %d: unknown type %q", i+1, sc.Type)
		}
		s, err := f(sc)
		if err != nil {
			return nil, fmt.Errorf("step %d (%s): %w", i+1, sc.Type, err)
		}
		label := sc.Name
		if label == "" {
			label = sc.Type
		}
		label = strings.Map(func(r rune) rune {
			if r == '/' || r == '\\' || r == ' ' {
				return '_'
			}
			return r
		}, label)
		p.steps = append(p.steps, namedStep{label: fmt.Sprintf("%02d-%s", i+1, label), step: s})
	}
	return p, nil
}

// Run executes the steps in order. onStep reports the step being run.
func (p *Pipeline) Run(ctx context.Context, job *Job, files []string, onStep func(string)) ([]string, error) {
	for _, s := range p.steps {
		if onStep != nil {
			onStep(s.label)
		}
		dir := filepath.Join(job.WorkDir, "steps", s.label)
		// a re-run (job resumed) starts the step from scratch
		os.RemoveAll(dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		start := time.Now()
		job.Log.Info("step start", "step", s.label, "files", len(files))
		out, err := s.step.Run(ctx, job, dir, files)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.label, err)
		}
		job.Log.Info("step done", "step", s.label, "files", len(out), "took", time.Since(start).Round(time.Second))
		files = out
	}
	return files, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// runCmd runs a command, logging its output; returns combined stdout and
// stderr separately for steps that parse them.
func runCmd(ctx context.Context, log *slog.Logger, timeout time.Duration, env map[string]string, name string, args ...string) (string, string, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = nil
	if len(env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	log.Debug("exec", "cmd", name, "args", args)
	err := cmd.Run()
	so, se := stdout.String(), stderr.String()
	if so != "" {
		log.Debug("exec stdout", "cmd", filepath.Base(name), "out", tail(so, 4000))
	}
	if se != "" {
		log.Debug("exec stderr", "cmd", filepath.Base(name), "out", tail(se, 4000))
	}
	if err != nil {
		return so, se, fmt.Errorf("%s: %w: %s", filepath.Base(name), err, tail(strings.TrimSpace(se+"\n"+so), 600))
	}
	return so, se, nil
}

func tail(s string, n int) string {
	if len(s) > n {
		return "..." + s[len(s)-n:]
	}
	return s
}

// Render executes a Go template string.
func Render(tpl string, data any) (string, error) {
	t, err := template.New("t").Option("missingkey=zero").Funcs(funcs).Parse(tpl)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

var funcs = template.FuncMap{
	"lower": strings.ToLower,
	"base":  filepath.Base,
	"ext":   filepath.Ext,
	// stem "/a/b/Game.iso" -> "Game"
	"stem":  func(p string) string { b := filepath.Base(p); return strings.TrimSuffix(b, filepath.Ext(b)) },
	"upper": strings.ToUpper,
	"trim":  strings.TrimSpace,
	// default "x" .Field -> .Field or "x" when empty/nil
	"default": func(def any, v any) any {
		if v == nil {
			return def
		}
		if s, ok := v.(string); ok && s == "" {
			return def
		}
		return v
	},
	// safe makes a value usable as a single path component
	"safe": func(v any) string {
		s := fmt.Sprint(v)
		r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "\x00", "")
		return strings.TrimSpace(r.Replace(s))
	},
}

func boolOr(b *bool, def bool) bool { return config.Bool(b, def) }
