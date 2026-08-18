package runbooks

import (
	"os"
	"path/filepath"
	"testing"
)

func writeRunbook(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture runbook: %v", err)
	}
}

func TestLoad_ReadsMarkdownFilesOnly(t *testing.T) {
	dir := t.TempDir()
	writeRunbook(t, dir, "cpu.md", "# CPU Saturation Runbook\nCheck cpu utilization.")
	writeRunbook(t, dir, "notes.txt", "not a runbook")

	lib, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lib.docs) != 1 {
		t.Fatalf("expected 1 loaded doc (ignoring .txt), got %d", len(lib.docs))
	}
	if lib.docs[0].source != "cpu.md" {
		t.Errorf("expected source cpu.md, got %q", lib.docs[0].source)
	}
}

func TestLoad_EmptyDirectoryIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	lib, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error for empty dir: %v", err)
	}
	if got := lib.Retrieve("anything", 5); len(got) != 0 {
		t.Errorf("expected no results from empty library, got %d", len(got))
	}
}

func TestLoad_NonexistentDirectoryIsAnError(t *testing.T) {
	_, err := Load("/definitely/does/not/exist/anywhere")
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
}

func TestRetrieve_RanksByKeywordOverlap(t *testing.T) {
	dir := t.TempDir()
	writeRunbook(t, dir, "cpu.md", "CPU saturation runbook. Check router cpu utilization and control-plane latency.")
	writeRunbook(t, dir, "memory.md", "Memory leak runbook. Check process memory growth.")
	writeRunbook(t, dir, "bgp.md", "BGP edge instability runbook. Check route dampening.")

	lib, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	results := lib.Retrieve("cpu saturation", 3)
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if results[0].Source != "cpu.md" {
		t.Errorf("expected cpu.md to rank first for a cpu-saturation query, got %q", results[0].Source)
	}
	for _, r := range results {
		if r.MatchMethod != "keyword-overlap" {
			t.Errorf("expected honest match_method 'keyword-overlap', got %q", r.MatchMethod)
		}
	}
}

func TestRetrieve_RespectsLimit(t *testing.T) {
	dir := t.TempDir()
	writeRunbook(t, dir, "a.md", "alpha")
	writeRunbook(t, dir, "b.md", "beta")
	writeRunbook(t, dir, "c.md", "gamma")

	lib, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	results := lib.Retrieve("anything", 2)
	if len(results) != 2 {
		t.Fatalf("expected exactly 2 results respecting limit, got %d", len(results))
	}
}

func TestRetrieve_TieBreaksDeterministically(t *testing.T) {
	dir := t.TempDir()
	writeRunbook(t, dir, "z.md", "no matching terms here at all")
	writeRunbook(t, dir, "a.md", "no matching terms here at all")

	lib, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r1 := lib.Retrieve("nonexistent query term xyz123", 2)
	r2 := lib.Retrieve("nonexistent query term xyz123", 2)
	if len(r1) != 2 || len(r2) != 2 {
		t.Fatalf("expected 2 zero-score results both times, got %d and %d", len(r1), len(r2))
	}
	if r1[0].Source != r2[0].Source || r1[0].Source != "a.md" {
		t.Errorf("expected deterministic alphabetical tie-break to a.md, got %q then %q", r1[0].Source, r2[0].Source)
	}
}

// TestLoad_AgainstRealRunbookLibrary is an integration sanity check against
// the actual runbooks/ directory shipped in this repository (reused as-is
// from the original Python prototype, per docs/migration-plan.md). It
// proves the real content loads and that a cpu-related query actually
// surfaces the CPU runbook, not just synthetic fixtures.
func TestLoad_AgainstRealRunbookLibrary(t *testing.T) {
	lib, err := Load("../../runbooks")
	if err != nil {
		t.Fatalf("unexpected error loading real runbooks directory: %v", err)
	}
	if len(lib.docs) < 5 {
		t.Fatalf("expected at least 5 real runbook files, found %d", len(lib.docs))
	}
	results := lib.Retrieve("cpu saturation", 1)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Source != "cpu.md" {
		t.Errorf("expected real cpu.md to rank first for 'cpu saturation', got %q", results[0].Source)
	}
}

func TestRetrieve_TitleCaseFromFilenameStem(t *testing.T) {
	dir := t.TempDir()
	writeRunbook(t, dir, "packet_loss.md", "packet loss content")

	lib, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	results := lib.Retrieve("packet loss", 1)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Title != "Packet Loss" {
		t.Errorf("expected title 'Packet Loss', got %q", results[0].Title)
	}
}
