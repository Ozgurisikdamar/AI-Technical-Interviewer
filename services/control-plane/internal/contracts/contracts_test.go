package contracts

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// repoContracts locates the repository-level contracts/ directory from this source file.
func repoContracts(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	dir := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "contracts")
	if _, err := os.Stat(dir); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("contracts/ not found at %s in CI: %v", dir, err)
		}
		t.Skipf("contracts/ not found (%v); drift check needs a full repository checkout", err)
	}
	return dir
}

func TestEmbeddedContractsMatchRepository(t *testing.T) {
	root := repoContracts(t)
	embedded, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(embedded) == 0 {
		t.Fatal("no contracts embedded; run tools/dev/sync-go-contracts.sh")
	}
	for _, rel := range embedded {
		want, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Errorf("%s: embedded but missing from contracts/: %v", rel, err)
			continue
		}
		if got := MustRead(rel); !bytes.Equal(got, want) {
			t.Errorf("%s: embedded copy differs from contracts/; run tools/dev/sync-go-contracts.sh", rel)
		}
	}
	// Every shipped plan and notice must be embedded, so new content cannot be forgotten.
	for _, dir := range []string{PlansDir, NoticesDir} {
		repoFiles, _ := filepath.Glob(filepath.Join(root, dir, "*.json"))
		got, err := ListDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(repoFiles) != len(got) {
			t.Errorf("%s: %d files in contracts/, %d embedded", dir, len(repoFiles), len(got))
		}
	}
}
