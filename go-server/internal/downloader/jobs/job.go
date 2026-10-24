// Package jobs queues download requests and runs them: download every link,
// run the system's pipeline, deliver the files, call back.
package jobs

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zszwede/romm-go/internal/downloader/pipeline"
)

type Status string

const (
	Queued      Status = "queued"
	Downloading Status = "downloading"
	Processing  Status = "processing"
	Delivering  Status = "delivering"
	Completed   Status = "completed"
	Failed      Status = "failed"
	Canceled    Status = "canceled"
)

func (s Status) Finished() bool { return s == Completed || s == Failed || s == Canceled }

// Link is one requested file. It accepts either a plain URL string or an
// object {"url": ..., "name": ..., any other fields}; the extra fields are
// kept and echoed back in the callback.
type Link struct {
	URL  string         `json:"url"`
	Name string         `json:"name,omitempty"`
	Meta map[string]any `json:"meta,omitempty"`

	// state
	Host  string `json:"host,omitempty"`
	File  string `json:"file,omitempty"` // downloaded file (work dir)
	Bytes int64  `json:"bytes,omitempty"`
	Total int64  `json:"total,omitempty"`
	Done  bool   `json:"done,omitempty"`
}

func (l *Link) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		l.URL = strings.TrimSpace(s)
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("link must be a URL string or an object with \"url\"")
	}
	type plain Link
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*l = Link(p)
	l.URL = strings.TrimSpace(l.URL)
	// unknown keys go to Meta
	known := map[string]bool{"url": true, "name": true, "meta": true, "host": true, "file": true, "bytes": true, "total": true, "done": true}
	for k, v := range raw {
		if !known[k] {
			if l.Meta == nil {
				l.Meta = map[string]any{}
			}
			l.Meta[k] = v
		}
	}
	return nil
}

// CallbackState tracks the notification to the source application.
type CallbackState struct {
	URL    string `json:"url,omitempty"`
	Status string `json:"status,omitempty"` // pending | sent | failed | none
	Error  string `json:"error,omitempty"`
}

type Job struct {
	ID              string               `json:"id"`
	System          string               `json:"system"`
	RequestedSystem string               `json:"requested_system,omitempty"`
	Name            string               `json:"name,omitempty"`
	Links           []*Link              `json:"links"`
	Metadata        map[string]any       `json:"metadata,omitempty"`
	Status          Status               `json:"status"`
	Step            string               `json:"step,omitempty"`
	Error           string               `json:"error,omitempty"`
	Files           []pipeline.Delivered `json:"files,omitempty"`
	Callback        CallbackState        `json:"callback"`
	Attempts        int                  `json:"attempts"`
	CreatedAt       time.Time            `json:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
	StartedAt       *time.Time           `json:"started_at,omitempty"`
	FinishedAt      *time.Time           `json:"finished_at,omitempty"`

	cancelRequested bool
}

// Payload is what the callback receives.
type Payload struct {
	Event    string               `json:"event"` // job.completed | job.failed | job.canceled
	JobID    string               `json:"job_id"`
	Status   Status               `json:"status"`
	System   string               `json:"system"`
	Name     string               `json:"name,omitempty"`
	Metadata map[string]any       `json:"metadata"`
	Path     string               `json:"path,omitempty"` // first delivered file
	Paths    []string             `json:"paths,omitempty"`
	Files    []pipeline.Delivered `json:"files,omitempty"`
	Links    []PayloadLink        `json:"links"`
	Error    string               `json:"error,omitempty"`
	Finished time.Time            `json:"finished_at"`
}

type PayloadLink struct {
	URL  string         `json:"url"`
	Name string         `json:"name,omitempty"`
	Meta map[string]any `json:"meta,omitempty"`
}

func (j *Job) payload() Payload {
	p := Payload{
		Event:    "job." + string(j.Status),
		JobID:    j.ID,
		Status:   j.Status,
		System:   j.System,
		Name:     j.Name,
		Metadata: j.Metadata,
		Files:    j.Files,
		Error:    j.Error,
	}
	if p.Metadata == nil {
		p.Metadata = map[string]any{}
	}
	if j.FinishedAt != nil {
		p.Finished = *j.FinishedAt
	}
	for _, f := range j.Files {
		p.Paths = append(p.Paths, f.Path)
	}
	if len(p.Paths) > 0 {
		p.Path = p.Paths[0]
	}
	for _, l := range j.Links {
		p.Links = append(p.Links, PayloadLink{URL: l.URL, Name: l.Name, Meta: l.Meta})
	}
	return p
}
