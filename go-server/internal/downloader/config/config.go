// Package config loads the YAML configuration. Every string value may use
// ${ENV_VAR} references; they are expanded before parsing.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   Server            `yaml:"server"`
	WorkDir  string            `yaml:"work_dir"`  // per-job scratch space (downloads, extraction, merge output)
	StateDir string            `yaml:"state_dir"` // job state (JSON), survives restarts
	Workers  int               `yaml:"workers"`   // jobs processed in parallel
	Hosts    []Host            `yaml:"hosts"`     // first match wins
	Callback Callback          `yaml:"callback"`  // default callback, systems may override
	Systems  map[string]System `yaml:"systems"`
	// DefaultSystem is used when a request names a system that is not configured.
	// Empty = such requests are rejected.
	DefaultSystem string `yaml:"default_system"`
	// KeepFailedWorkDirs leaves the work dir of failed jobs for inspection/resume.
	KeepFailedWorkDirs bool `yaml:"keep_failed_work_dirs"`
}

type Server struct {
	Listen   string `yaml:"listen"`    // e.g. ":8090"
	APIToken string `yaml:"api_token"` // if set, requests need "Authorization: Bearer <token>"
}

// Host selects how links of a given host are downloaded.
type Host struct {
	Name  string   `yaml:"name"`
	Match []string `yaml:"match"` // host patterns: "1fichier.com", "*.archive.org", "*"
	// Type: onefichier | archive_org | http
	Type string `yaml:"type"`

	// onefichier
	Token     string        `yaml:"token"`      // API key
	TokenFile string        `yaml:"token_file"` // alternative: file holding the API key
	APIURL    string        `yaml:"api_url"`    // default https://api.1fichier.com/v1/download/get_token.cgi
	APIPause  time.Duration `yaml:"api_pause"`  // pause after each API call (1fichier allows 3 req/s)

	// archive_org (S3 "LOW <access>:<secret>" authorization)
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`

	// all types
	Headers   map[string]string `yaml:"headers"` // extra request headers
	UserAgent string            `yaml:"user_agent"`
	Retries   int               `yaml:"retries"` // attempts per file
	Timeout   time.Duration     `yaml:"timeout"` // per-file timeout (0 = none)
	// RetryDelay is multiplied by the attempt number.
	RetryDelay time.Duration `yaml:"retry_delay"`
}

type Callback struct {
	URL     string            `yaml:"url"`
	Method  string            `yaml:"method"` // default POST
	Headers map[string]string `yaml:"headers"`
	Timeout time.Duration     `yaml:"timeout"`
	Retries int               `yaml:"retries"`
	// OnFailure also calls back (status "failed") when a job fails.
	OnFailure *bool `yaml:"on_failure"`
	// AllowRequestOverride lets a request pass its own callback_url.
	AllowRequestOverride *bool `yaml:"allow_request_override"`
}

// System describes what happens to the downloaded files of one platform.
type System struct {
	Aliases []string `yaml:"aliases"` // other names accepted in requests (e.g. RomM slugs)
	// TargetDir is a Go template; fields: .System .JobID .Metadata (map) .Name
	TargetDir string `yaml:"target_dir"`
	// FileName optionally renames each final file (Go template, same fields
	// plus .File (base name), .Stem, .Ext). Empty = keep name.
	FileName string `yaml:"file_name"`
	// OnConflict: rename (default) | overwrite | skip | fail
	OnConflict string `yaml:"on_conflict"`
	// Mode of the final step: move (default) | copy
	Mode     string    `yaml:"mode"`
	DirMode  string    `yaml:"dir_mode"` // octal, default 0755
	Steps    []Step    `yaml:"steps"`    // processing pipeline, in order
	Callback *Callback `yaml:"callback"` // overrides the global callback fields that are set
}

// Step is one pipeline step. Type picks the implementation; the remaining
// fields are interpreted by it (unknown ones are ignored).
type Step struct {
	Type string `yaml:"type"` // extract | filter | switch_decompress | switch_merge | exec
	Name string `yaml:"name"` // optional label for logs

	// extract
	Binary       string `yaml:"binary"`        // 7z (extract), switch-tool (switch_*), program (exec)
	KeepArchives bool   `yaml:"keep_archives"` // extract: keep archive volumes in the result
	Recursive    *bool  `yaml:"recursive"`     // extract: also unpack archives found inside archives (default true)
	Password     string `yaml:"password"`      // extract: archive password

	// filter
	Include []string `yaml:"include"` // regexps on file name; keep matching files
	Exclude []string `yaml:"exclude"` // regexps on file name; drop matching files
	// RequireMatch: fail the job if the filter leaves nothing.
	RequireMatch bool `yaml:"require_match"`

	// switch_*
	Keys string `yaml:"keys"` // prod.keys
	// DeleteInputs removes the source files as soon as the step succeeded
	// (saves disk space; the work dir is removed at the end anyway).
	DeleteInputs *bool `yaml:"delete_inputs"`
	// AllowLoose (switch_merge): pass through updates/DLCs whose base game is
	// not in the job instead of failing.
	AllowLoose *bool `yaml:"allow_loose"`

	// exec: Args are Go templates. Fields: .Input (one file, per_file mode),
	// .Inputs (all files), .OutDir, .System, .JobID, .Metadata, .Keys
	Args    []string `yaml:"args"`
	PerFile bool     `yaml:"per_file"` // run once per input file instead of once for all
	// Outputs: what the step yields: outdir (files written to .OutDir, default),
	// inputs (inputs unchanged, e.g. a validating command), both
	Outputs string            `yaml:"outputs"`
	Env     map[string]string `yaml:"env"`

	Timeout time.Duration `yaml:"timeout"` // external command timeout (0 = none)
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	expanded := os.Expand(string(raw), func(k string) string {
		if k == "$" {
			return "$" // "$$" escapes a literal dollar
		}
		return os.Getenv(k)
	})
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(expanded))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	c.defaults()
	return &c, c.validate()
}

// Overrides are runtime path overrides the RomM server applies after loading,
// so the Switch steps use its own bundled switch-tool and prod.keys regardless
// of the paths written in a config.yaml (which was authored for the container's
// filesystem). An empty field leaves the config's value untouched.
type Overrides struct {
	SwitchTool string // absolute path to the switch-tool binary
	SwitchKeys string // absolute path to prod.keys
	WorkDir    string // per-job scratch dir (if the config's is container-only)
	StateDir   string // job state dir
	// DisableCallback clears the HTTP callback URL (global + per-system). The
	// in-process downloader applies finished jobs to the DB via its poller, so
	// the config's callback to the container (http://romm:8080/...) is both
	// unreachable on the host and redundant — clearing it stops failed-callback
	// retry noise.
	DisableCallback bool
}

// ApplyOverrides rewrites the Switch steps (and optional work/state dirs) to the
// given paths. Called before building the pipelines.
func (c *Config) ApplyOverrides(ov Overrides) {
	if ov.WorkDir != "" {
		c.WorkDir = ov.WorkDir
	}
	if ov.StateDir != "" {
		c.StateDir = ov.StateDir
	}
	for name, sys := range c.Systems {
		for i := range sys.Steps {
			switch sys.Steps[i].Type {
			case "switch_decompress", "switch_merge":
				if ov.SwitchTool != "" {
					sys.Steps[i].Binary = ov.SwitchTool
				}
				if ov.SwitchKeys != "" {
					sys.Steps[i].Keys = ov.SwitchKeys
				}
			}
		}
		if ov.DisableCallback {
			sys.Callback = nil
		}
		c.Systems[name] = sys
	}
	if ov.DisableCallback {
		c.Callback.URL = ""
	}
}

func (c *Config) defaults() {
	if c.Server.Listen == "" {
		c.Server.Listen = ":8090"
	}
	if c.WorkDir == "" {
		c.WorkDir = "work"
	}
	if c.StateDir == "" {
		c.StateDir = "state"
	}
	if c.Workers <= 0 {
		c.Workers = 1
	}
	for i := range c.Hosts {
		h := &c.Hosts[i]
		if h.Retries <= 0 {
			h.Retries = 3
		}
		if h.RetryDelay <= 0 {
			h.RetryDelay = 2 * time.Second
		}
		if h.Type == "onefichier" {
			if h.APIURL == "" {
				h.APIURL = "https://api.1fichier.com/v1/download/get_token.cgi"
			}
			if h.APIPause <= 0 {
				h.APIPause = 400 * time.Millisecond
			}
		}
	}
	if c.Callback.Method == "" {
		c.Callback.Method = "POST"
	}
	if c.Callback.Timeout <= 0 {
		c.Callback.Timeout = 30 * time.Second
	}
	if c.Callback.Retries <= 0 {
		c.Callback.Retries = 5
	}
	t, f := true, false
	if c.Callback.OnFailure == nil {
		c.Callback.OnFailure = &t
	}
	if c.Callback.AllowRequestOverride == nil {
		c.Callback.AllowRequestOverride = &f
	}
}

func (c *Config) validate() error {
	for i, h := range c.Hosts {
		if len(h.Match) == 0 {
			return fmt.Errorf("hosts[%d] (%s): match is empty", i, h.Name)
		}
		switch h.Type {
		case "onefichier":
			if h.Token == "" && h.TokenFile == "" {
				return fmt.Errorf("hosts[%d] (%s): onefichier needs token or token_file", i, h.Name)
			}
		case "archive_org":
			if h.AccessKey == "" || h.SecretKey == "" {
				return fmt.Errorf("hosts[%d] (%s): archive_org needs access_key and secret_key (IA_S3_ACCESS_KEY / IA_S3_SECRET_KEY)", i, h.Name)
			}
		case "http":
		default:
			return fmt.Errorf("hosts[%d] (%s): unknown type %q", i, h.Name, h.Type)
		}
	}
	if len(c.Systems) == 0 {
		return fmt.Errorf("no systems configured")
	}
	for name, s := range c.Systems {
		if s.TargetDir == "" {
			return fmt.Errorf("systems.%s: target_dir is required", name)
		}
		switch s.OnConflict {
		case "", "rename", "overwrite", "skip", "fail":
		default:
			return fmt.Errorf("systems.%s: bad on_conflict %q", name, s.OnConflict)
		}
		switch s.Mode {
		case "", "move", "copy":
		default:
			return fmt.Errorf("systems.%s: bad mode %q", name, s.Mode)
		}
	}
	if c.DefaultSystem != "" {
		if _, ok := c.Systems[c.DefaultSystem]; !ok {
			return fmt.Errorf("default_system %q is not configured", c.DefaultSystem)
		}
	}
	return nil
}

// ResolveSystem maps a requested system name (or alias, case-insensitive) to
// its configured key.
func (c *Config) ResolveSystem(name string) (string, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	for key, s := range c.Systems {
		if strings.ToLower(key) == n {
			return key, true
		}
		for _, a := range s.Aliases {
			if strings.ToLower(a) == n {
				return key, true
			}
		}
	}
	if c.DefaultSystem != "" {
		return c.DefaultSystem, true
	}
	return "", false
}

// CallbackFor merges a system's callback over the global one.
func (c *Config) CallbackFor(system string) Callback {
	cb := c.Callback
	s, ok := c.Systems[system]
	if !ok || s.Callback == nil {
		return cb
	}
	o := s.Callback
	if o.URL != "" {
		cb.URL = o.URL
	}
	if o.Method != "" {
		cb.Method = o.Method
	}
	if len(o.Headers) > 0 {
		h := map[string]string{}
		for k, v := range cb.Headers {
			h[k] = v
		}
		for k, v := range o.Headers {
			h[k] = v
		}
		cb.Headers = h
	}
	if o.Timeout > 0 {
		cb.Timeout = o.Timeout
	}
	if o.Retries > 0 {
		cb.Retries = o.Retries
	}
	if o.OnFailure != nil {
		cb.OnFailure = o.OnFailure
	}
	if o.AllowRequestOverride != nil {
		cb.AllowRequestOverride = o.AllowRequestOverride
	}
	return cb
}

// Bool returns *b or def when unset.
func Bool(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}
