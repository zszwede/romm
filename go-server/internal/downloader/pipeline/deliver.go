package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zszwede/romm-go/internal/downloader/config"
	"github.com/zszwede/romm-go/internal/downloader/fsutil"
)

// Delivered is one final file.
type Delivered struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Skipped bool   `json:"skipped,omitempty"` // on_conflict=skip and the target existed
}

// Deliver moves (or copies) the final files into the system's target dir.
func Deliver(sys config.System, job *Job, files []string) ([]Delivered, error) {
	data := job.TemplateData()
	target, err := Render(sys.TargetDir, data)
	if err != nil {
		return nil, fmt.Errorf("target_dir: %w", err)
	}
	target = filepath.Clean(strings.TrimSpace(target))
	if target == "" || target == "." {
		return nil, fmt.Errorf("target_dir rendered empty")
	}
	mode := os.FileMode(0o755)
	if sys.DirMode != "" {
		m, err := strconv.ParseUint(sys.DirMode, 8, 32)
		if err != nil {
			return nil, fmt.Errorf("dir_mode: %w", err)
		}
		mode = os.FileMode(m)
	}
	if err := os.MkdirAll(target, mode); err != nil {
		return nil, err
	}

	var out []Delivered
	for _, f := range files {
		name := filepath.Base(f)
		if sys.FileName != "" {
			d := job.TemplateData()
			ext := filepath.Ext(name)
			d["File"], d["Stem"], d["Ext"] = name, strings.TrimSuffix(name, ext), ext
			n, err := Render(sys.FileName, d)
			if err != nil {
				return out, fmt.Errorf("file_name: %w", err)
			}
			if n = fsutil.SafeName(n); n != "" {
				name = n
			}
		}
		dst := filepath.Join(target, name)
		if _, err := os.Lstat(dst); err == nil {
			switch sys.OnConflict {
			case "skip":
				st, _ := os.Stat(dst)
				job.Log.Warn("target exists, skipped", "path", dst)
				out = append(out, Delivered{Path: dst, Name: name, Size: st.Size(), Skipped: true})
				continue
			case "fail":
				return out, fmt.Errorf("target exists: %s", dst)
			case "overwrite":
				if err := os.Remove(dst); err != nil {
					return out, err
				}
			default: // rename
				dst = fsutil.UniquePath(dst)
			}
		}
		if sys.Mode == "copy" {
			err = fsutil.Copy(f, dst)
		} else {
			err = fsutil.Move(f, dst)
		}
		if err != nil {
			return out, fmt.Errorf("deliver %s: %w", name, err)
		}
		st, err := os.Stat(dst)
		if err != nil {
			return out, err
		}
		job.Log.Info("delivered", "path", dst)
		out = append(out, Delivered{Path: dst, Name: filepath.Base(dst), Size: st.Size()})
	}
	return out, nil
}
