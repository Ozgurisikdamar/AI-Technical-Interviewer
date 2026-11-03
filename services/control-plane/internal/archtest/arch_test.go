// Package archtest enforces package boundaries of the modular control plane (ADR-002, ADR-001).
package archtest

import (
	"os/exec"
	"strings"
	"testing"
)

const module = "github.com/ozgurisikdamar/ai-technical-interviewer/services/control-plane"

func deps(t *testing.T, pkg string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", module+"/"+pkg).CombinedOutput()
	if err != nil {
		t.Fatalf("go list %s: %v\n%s", pkg, err, out)
	}
	return strings.Fields(string(out))
}

func allPackages(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("go", "list", module+"/...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	return strings.Fields(string(out))
}

func directImports(t *testing.T, pkg string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-f", `{{join .Imports " "}}`, module+"/"+pkg).CombinedOutput()
	if err != nil {
		t.Fatalf("go list %s: %v\n%s", pkg, err, out)
	}
	return strings.Fields(string(out))
}

// The flow engine must stay pure: no network, database, process, OS or clock access of its own,
// no third-party code and no control-plane package other than the embedded contracts.
func TestFlowEngineIsPure(t *testing.T) {
	allowedInternal := map[string]bool{module + "/internal/flow": true, module + "/internal/contracts": true}
	for _, d := range deps(t, "internal/flow") {
		if strings.HasPrefix(d, module) && !allowedInternal[d] {
			t.Errorf("internal/flow depends on %s", d)
		}
		if strings.Contains(strings.SplitN(d, "/", 2)[0], ".") && !strings.HasPrefix(d, module) {
			t.Errorf("internal/flow depends on third-party package %s", d)
		}
	}
	forbidden := map[string]bool{"net": true, "net/http": true, "database/sql": true, "os": true, "os/exec": true, "syscall": true, "time": true, "math/rand": true, "math/rand/v2": true, "crypto/rand": true}
	for _, d := range directImports(t, "internal/flow") {
		if forbidden[d] {
			t.Errorf("internal/flow imports %s directly", d)
		}
	}
}

// No control-plane package may contain or import evaluation code: Tier-2 is a separate deployable
// (ADR-001) and its output must never reach the live flow (ADR-015).
func TestNoEvaluationCodeInControlPlane(t *testing.T) {
	for _, p := range allPackages(t) {
		if strings.Contains(strings.ToLower(p), "evaluat") || strings.Contains(strings.ToLower(p), "tier2") {
			t.Errorf("evaluation package inside the control plane: %s", p)
		}
	}
}

// Packages that build Tier-1-facing data must not depend on packages that could expose
// rubric, recording or administrative data (checked as soon as they exist).
func TestTier1FacingPackagesHaveNarrowDependencies(t *testing.T) {
	forbidden := []string{"/internal/recording", "/internal/admin", "/cmd/"}
	for _, pkg := range []string{"internal/tier1ctx"} {
		out, err := exec.Command("go", "list", module+"/"+pkg).CombinedOutput()
		if err != nil {
			t.Logf("%s not present yet: %s", pkg, strings.TrimSpace(string(out)))
			continue
		}
		for _, d := range deps(t, pkg) {
			for _, f := range forbidden {
				if strings.Contains(d, module+f) {
					t.Errorf("%s depends on %s", pkg, d)
				}
			}
		}
	}
}
