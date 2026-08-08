package agentstore

import (
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// scanFixture builds a throwaway $HOME containing exactly the files we want
// Scan() to classify, and points the process at it for the duration of the
// test. Scan() resolves its root through os.UserHomeDir(), which reads $HOME
// on Unix, so this is the real walk over a real tree -- not a substitute for
// one. Returns the fake home.
func scanFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for rel, body := range files {
		abs := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func writeFixtureFile(t *testing.T, home, rel, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, rel), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A rescan that touches no file must report every row as Unchanged and none as
// Updated. This is the whole point of the counter: AGENTS.md tells agents to
// POST /files/scan to confirm an out-of-band edit landed, and a count that is
// identical whether or not anything changed cannot confirm anything.
func TestScanReportsUnchangedRowsSeparatelyFromChangedOnes(t *testing.T) {
	home := scanFixture(t, map[string]string{
		"CLAUDE.md":             "first",
		"AGENTS.md":             "second",
		".claude/CLAUDE.md":     "third",
		".claude/commands/x.md": "fourth",
	})
	s := testStore(t)

	first, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if first.Scanned != 4 || first.Added != 4 {
		t.Fatalf("first scan: want 4 scanned / 4 added, got %+v", first)
	}
	if first.Unchanged != 0 || first.Updated != 0 {
		t.Errorf("first scan: a brand new row is Added, not Updated or Unchanged: %+v", first)
	}

	// Nothing on disk moves between the two scans.
	second, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if second.Added != 0 {
		t.Errorf("second scan: want 0 added, got %d", second.Added)
	}
	if second.Updated != 0 {
		t.Errorf("second scan: no file changed, so Updated must be 0, got %d (%+v)", second.Updated, second)
	}
	if second.Unchanged != 4 {
		t.Errorf("second scan: want 4 unchanged, got %d (%+v)", second.Unchanged, second)
	}
	_ = home
}

// Updated must count exactly the files whose content changed -- not "rows that
// already existed". One edit among four files is one update and three
// unchanged.
func TestScanCountsOnlyContentChangesAsUpdated(t *testing.T) {
	home := scanFixture(t, map[string]string{
		"CLAUDE.md":             "first",
		"AGENTS.md":             "second",
		".claude/CLAUDE.md":     "third",
		".claude/commands/x.md": "fourth",
	})
	s := testStore(t)
	if _, err := s.Scan(); err != nil {
		t.Fatal(err)
	}

	writeFixtureFile(t, home, "CLAUDE.md", "first, edited out of band")

	res, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 1 {
		t.Errorf("one file changed, want Updated 1, got %d (%+v)", res.Updated, res)
	}
	if res.Unchanged != 3 {
		t.Errorf("three files untouched, want Unchanged 3, got %d (%+v)", res.Unchanged, res)
	}
	if res.Added != 0 || res.Missing != 0 {
		t.Errorf("want no adds or missing, got %+v", res)
	}
	if res.Scanned != 4 {
		t.Errorf("want Scanned 4, got %d", res.Scanned)
	}
}

// The four disjoint counters must account for every row the walk visited.
// Without this, a future branch that forgets to increment one of them leaves a
// silent hole in the report rather than a visible mismatch.
func TestScanCountersSumToScanned(t *testing.T) {
	home := scanFixture(t, map[string]string{
		"CLAUDE.md":         "first",
		"AGENTS.md":         "second",
		".claude/CLAUDE.md": "third",
	})
	s := testStore(t)
	if _, err := s.Scan(); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, home, "AGENTS.md", "second, changed")
	writeFixtureFile(t, home, ".claude/notes.md", "a new one")

	res, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Added + res.Updated + res.Unchanged; got != res.Scanned {
		t.Errorf("added+updated+unchanged = %d, want Scanned = %d (%+v)", got, res.Scanned, res)
	}
	if res.Added != 1 || res.Updated != 1 || res.Unchanged != 2 {
		t.Errorf("want 1 added / 1 updated / 2 unchanged, got %+v", res)
	}
}

// A file that disappears is Missing, and Missing is counted over rows that the
// walk did NOT visit -- so it is deliberately outside the Scanned identity
// asserted above. Pinned here so the distinction is not "fixed" into the sum by
// a later reader.
func TestScanMissingIsCountedOutsideScanned(t *testing.T) {
	home := scanFixture(t, map[string]string{
		"CLAUDE.md": "first",
		"AGENTS.md": "second",
	})
	s := testStore(t)
	if _, err := s.Scan(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(home, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}

	res, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if res.Missing != 1 {
		t.Errorf("want Missing 1, got %d (%+v)", res.Missing, res)
	}
	if res.Scanned != 1 {
		t.Errorf("the walk saw one file, want Scanned 1, got %d", res.Scanned)
	}
	if res.Unchanged != 1 {
		t.Errorf("the surviving file is unchanged, got %+v", res)
	}
}

// Version history is already hash-guarded, and that is what makes this a
// reporting defect rather than corruption. Pinned so a change to the counting
// cannot quietly start appending a version per scan.
//
// ⚠️ This case does NOT cover Scan's own FindVersionByHash guard, and an
// earlier draft of this comment claimed it did. Deleting that guard leaves
// this test green: AppendVersion de-dupes against the most recent version by
// itself (seed.go), so a repeated no-op scan is refused one layer down. The
// case that discriminates the two guards is the revert below -- do not cite
// this test as coverage for the guard in Scan.
func TestScanDoesNotAppendVersionsWhenNothingChanged(t *testing.T) {
	scanFixture(t, map[string]string{"CLAUDE.md": "first"})
	s := testStore(t)
	if _, err := s.Scan(); err != nil {
		t.Fatal(err)
	}
	files, err := s.ListTrackedFiles("", "")
	if err != nil || len(files) != 1 {
		t.Fatalf("want 1 tracked file, got %d (%v)", len(files), err)
	}
	id := files[0].ID

	countVersions := func() int {
		t.Helper()
		var n int
		if err := s.db.QueryRow("SELECT count(*) FROM tracked_file_versions WHERE tracked_file_id = ?", id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := countVersions()
	for i := 0; i < 3; i++ {
		if _, err := s.Scan(); err != nil {
			t.Fatal(err)
		}
	}
	if after := countVersions(); after != before {
		t.Errorf("three no-op scans appended %d versions (was %d)", after-before, before)
	}
}

// The guard in Scan (FindVersionByHash) searches the file's WHOLE history;
// AppendVersion's own de-dupe only compares against the most recent version.
// A revert -- A, then B, then back to A -- is the only case that tells them
// apart, so it is the only case that can pin the outer guard. Without it the
// revert appends a third version recording content already in the log.
//
// The revert must still be reported as Updated: the hash on the row moved from
// B to A, and an agent that reverts a file out of band needs the scan to say
// so. Version de-duplication and change reporting are separate questions, and
// this asserts both at once because the tempting fix to either one breaks the
// other.
func TestScanRevertToEarlierContentIsUpdatedButAppendsNoVersion(t *testing.T) {
	home := scanFixture(t, map[string]string{"CLAUDE.md": "A"})
	s := testStore(t)
	if _, err := s.Scan(); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, home, "CLAUDE.md", "B")
	if _, err := s.Scan(); err != nil {
		t.Fatal(err)
	}

	files, err := s.ListTrackedFiles("", "")
	if err != nil || len(files) != 1 {
		t.Fatalf("want 1 tracked file, got %d (%v)", len(files), err)
	}
	id := files[0].ID
	var beforeRevert int
	if err := s.db.QueryRow("SELECT count(*) FROM tracked_file_versions WHERE tracked_file_id = ?", id).Scan(&beforeRevert); err != nil {
		t.Fatal(err)
	}
	if beforeRevert != 2 {
		t.Fatalf("setup: want 2 versions (A and B) before the revert, got %d", beforeRevert)
	}

	writeFixtureFile(t, home, "CLAUDE.md", "A")
	res, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}

	if res.Updated != 1 {
		t.Errorf("a revert changes the row's hash, want Updated 1, got %d (%+v)", res.Updated, res)
	}
	if res.Unchanged != 0 {
		t.Errorf("want Unchanged 0 after a revert, got %d (%+v)", res.Unchanged, res)
	}
	var afterRevert int
	if err := s.db.QueryRow("SELECT count(*) FROM tracked_file_versions WHERE tracked_file_id = ?", id).Scan(&afterRevert); err != nil {
		t.Fatal(err)
	}
	if afterRevert != beforeRevert {
		t.Errorf("content A is already in history, want %d versions, got %d", beforeRevert, afterRevert)
	}
}
