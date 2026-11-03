// Package contracts exposes the machine-readable contracts embedded in the binary.
//
// The files under embedded/ are copies of the repository-level contracts/ directory
// (tools/dev/sync-go-contracts.sh). TestEmbeddedContractsMatchRepository fails when they drift.
package contracts

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

//go:embed embedded
var files embed.FS

// Paths of the embedded contracts, relative to the repository contracts/ directory.
const (
	StateMachinePath       = "state-machine/interview-session.v1.1.json"
	Tier1ContextSchemaPath = "schemas/tier1/v1/tier1-context.schema.json"
	SegmentSubmissionPath  = "schemas/transcript/v1/segment-submission.schema.json"
	TranscriptSegmentPath  = "schemas/transcript/v1/transcript-segment.schema.json"
	AnswerFinalizedPath    = "schemas/events/v1/answer-finalized.schema.json"
	InterviewCompletedPath = "schemas/events/v1/interview-completed.schema.json"
	InterviewPlanPath      = "schemas/plan/v1/interview-plan.schema.json"
	ConsentNoticePath      = "schemas/consent/v1/consent-notice.schema.json"
	PlansDir               = "content/plans"
	NoticesDir             = "content/notices"
)

// Read returns an embedded contract file by its repository-relative path.
func Read(rel string) ([]byte, error) {
	b, err := files.ReadFile(path.Join("embedded", rel))
	if err != nil {
		return nil, fmt.Errorf("contract %q not embedded: %w", rel, err)
	}
	return b, nil
}

// MustRead is Read for files that are guaranteed by the build (panics if missing).
func MustRead(rel string) []byte {
	b, err := Read(rel)
	if err != nil {
		panic(err)
	}
	return b
}

// List returns the repository-relative paths of every embedded file, sorted.
func List() ([]string, error) {
	var out []string
	err := fs.WalkDir(files, "embedded", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			out = append(out, strings.TrimPrefix(p, "embedded/"))
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// ListDir returns the repository-relative paths of the embedded files in one directory.
func ListDir(dir string) ([]string, error) {
	entries, err := files.ReadDir(path.Join("embedded", dir))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, path.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}
