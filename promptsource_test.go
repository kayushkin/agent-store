package agentstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testHostDirectives = "# Directives\n\nBe plain.\n\n## Services\n\nUse the stores.\n"
const testHostOverview = "# Overview\n\nRepos live here.\n\n## Scheduler\n\n```bash\n# List jobs\ncurl :8092/api/jobs\n```\n"

type promptFixture struct {
	t     *testing.T
	home  string
	store *Store
}

func newPromptFixture(t *testing.T) *promptFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	f := &promptFixture{t: t, home: home}
	f.write("AGENTS.md", testHostDirectives)
	f.write("CLAUDE.md", testHostOverview)
	store, err := Open(filepath.Join(home, "agents.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	f.store = store
	if _, err := store.Scan(); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *promptFixture) write(relativePath, content string) {
	f.t.Helper()
	path := filepath.Join(f.home, relativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *promptFixture) read(relativePath string) string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.home, relativePath))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(data)
}

// importHost imports the two host files and returns the global collection id.
func (f *promptFixture) importHost() int64 {
	f.t.Helper()
	reports, err := f.store.ImportUntrackedPromptFiles([]string{"AGENTS.md", "CLAUDE.md"})
	if err != nil {
		f.t.Fatal(err)
	}
	if len(reports) != 1 {
		f.t.Fatalf("imported %d collections, want 1", len(reports))
	}
	return reports[0].CollectionID
}

func (f *promptFixture) render(collectionID int64) *PromptRenderResult {
	f.t.Helper()
	result, err := f.store.RenderPromptCollection(collectionID)
	if err != nil {
		f.t.Fatal(err)
	}
	return result
}

func TestImportStoresEverySectionAndWritesNothing(t *testing.T) {
	f := newPromptFixture(t)
	id := f.importHost()

	view, err := f.store.GetPromptCollectionView(id)
	if err != nil {
		t.Fatal(err)
	}
	var headings []string
	for _, section := range view.Sections {
		headings = append(headings, section.Heading)
	}
	if got, want := strings.Join(headings, "|"), "# Directives|## Services|# Overview|## Scheduler"; got != want {
		t.Fatalf("headings = %s, want %s", got, want)
	}
	if !strings.Contains(view.Sections[3].Body, "# List jobs") {
		t.Fatal("the shell comment inside the code fence was cut out of its section")
	}
	if f.read("AGENTS.md") != testHostDirectives || f.read("CLAUDE.md") != testHostOverview {
		t.Fatal("import changed a file on disk")
	}
	for _, output := range view.Outputs {
		if output.Drifted {
			t.Fatalf("%s reads as drifted straight after import", output.RelativePath)
		}
	}
	revisions, err := f.store.ListPromptSectionRevisions(id, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 4 || revisions[0].Source != PromptRevisionSourceImport {
		t.Fatalf("revisions = %d (source %q), want 4 import revisions", len(revisions), revisions[0].Source)
	}

	// A second import finds nothing left to claim.
	again, err := f.store.ImportUntrackedPromptFiles(nil)
	if err != nil || len(again) != 0 {
		t.Fatalf("second import = %v, %v; want nothing", again, err)
	}
}

func TestRenderGivesEveryOutputTheSameFullPrompt(t *testing.T) {
	f := newPromptFixture(t)
	id := f.importHost()
	result := f.render(id)
	if result.RefusedReason != "" {
		t.Fatal(result.RefusedReason)
	}
	if len(result.WrittenFiles) != 2 {
		t.Fatalf("wrote %d files, want 2", len(result.WrittenFiles))
	}
	if f.read("AGENTS.md") != f.read("CLAUDE.md") {
		t.Fatal("the two outputs differ after a render")
	}
	for _, want := range []string{"Be plain.", "Repos live here.", "# List jobs"} {
		if !strings.Contains(f.read("CLAUDE.md"), want) {
			t.Fatalf("render lost %q", want)
		}
	}
	if again := f.render(id); len(again.WrittenFiles) != 0 {
		t.Fatalf("an unchanged collection rewrote %d files", len(again.WrittenFiles))
	}
}

func TestBodyEditInAFileIsCarriedBackAndReachesTheOtherFile(t *testing.T) {
	f := newPromptFixture(t)
	id := f.importHost()
	f.render(id)

	f.write("CLAUDE.md", strings.Replace(f.read("CLAUDE.md"), "Use the stores.", "Use the stores, always.", 1))
	reconciliation, err := f.store.ReconcilePromptDrifts()
	if err != nil {
		t.Fatal(err)
	}
	if len(reconciliation.Detected) != 1 || reconciliation.Detected[0].Status != PromptDriftStatusApplied {
		t.Fatalf("reconcile = %+v, want one applied drift", reconciliation.Detected)
	}
	if !strings.Contains(f.read("AGENTS.md"), "Use the stores, always.") {
		t.Fatal("the edit did not reach the other output")
	}
	revisions, _ := f.store.ListPromptSectionRevisions(id, 0, 1)
	if revisions[0].Source != PromptRevisionSourceDrift || revisions[0].DriftID != reconciliation.Detected[0].ID {
		t.Fatalf("newest revision = %+v, want one from the drift", revisions[0])
	}
}

func TestEditBeforeTheFirstRenderTouchesOnlyItsOwnSections(t *testing.T) {
	f := newPromptFixture(t)
	id := f.importHost()
	// CLAUDE.md holds only half the collection here. Diffing it against all
	// the sections would read the other half as deleted.
	f.write("CLAUDE.md", strings.Replace(testHostOverview, "Repos live here.", "Repos live in ~/repos.", 1))
	reconciliation, err := f.store.ReconcilePromptDrifts()
	if err != nil {
		t.Fatal(err)
	}
	if len(reconciliation.Detected) != 1 || reconciliation.Detected[0].Status != PromptDriftStatusApplied {
		t.Fatalf("reconcile = %+v", reconciliation.Detected)
	}
	view, _ := f.store.GetPromptCollectionView(id)
	if len(view.Sections) != 4 {
		t.Fatalf("sections = %d, want all 4 kept", len(view.Sections))
	}
	if !strings.Contains(f.read("AGENTS.md"), "Repos live in ~/repos.") || !strings.Contains(f.read("AGENTS.md"), "Be plain.") {
		t.Fatal("render after the drift is missing text")
	}
}

func TestAddedSectionIsHeldBlocksRenderAndAppliesWithItsLabels(t *testing.T) {
	f := newPromptFixture(t)
	id := f.importHost()
	f.render(id)

	f.write("AGENTS.md", strings.Replace(f.read("AGENTS.md"), "# Overview", "## Reminders\n\nOne coordinator.\n\n# Overview", 1))
	reconciliation, err := f.store.ReconcilePromptDrifts()
	if err != nil {
		t.Fatal(err)
	}
	if len(reconciliation.Detected) != 1 {
		t.Fatalf("detected %d drifts, want 1", len(reconciliation.Detected))
	}
	drift := reconciliation.Detected[0]
	if drift.Status != PromptDriftStatusHeld || !drift.NeedsAnnotation() {
		t.Fatalf("drift = %+v, want held and waiting for labels", drift)
	}
	if strings.Contains(f.read("CLAUDE.md"), "Reminders") {
		t.Fatal("a held drift was rendered")
	}
	if result := f.render(id); result.RefusedReason == "" || len(result.WrittenFiles) != 0 {
		t.Fatal("render wrote over a file with unsettled drift")
	}
	if !strings.Contains(f.read("AGENTS.md"), "One coordinator.") {
		t.Fatal("the drifted file was overwritten")
	}

	if _, err := f.store.SetPromptDriftAnnotation(drift.ID, PromptDriftAnnotation{InsertedSections: []PromptDriftInsertedSectionLabel{{OperationIndex: 5}}}); err == nil {
		t.Fatal("a label for an operation that does not exist was accepted")
	}
	annotation := PromptDriftAnnotation{Note: "added reminders", AnnotatedBy: "test", InsertedSections: []PromptDriftInsertedSectionLabel{{OperationIndex: 0, Tags: []string{"Reminders", "scheduler"}}}}
	if err := f.store.ApplyPromptDrift(drift.ID, &annotation); err != nil {
		t.Fatal(err)
	}
	f.render(id)
	view, _ := f.store.GetPromptCollectionView(id)
	var headings []string
	for _, section := range view.Sections {
		headings = append(headings, section.Heading)
		if section.Heading == "## Reminders" && strings.Join(section.Tags, ",") != "reminders,scheduler" {
			t.Fatalf("tags = %v", section.Tags)
		}
	}
	if got, want := strings.Join(headings, "|"), "# Directives|## Services|## Reminders|# Overview|## Scheduler"; got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
	if f.read("CLAUDE.md") != f.read("AGENTS.md") || !strings.Contains(f.read("CLAUDE.md"), "One coordinator.") {
		t.Fatal("outputs disagree after the drift was applied")
	}
}

func TestDismissedDriftIsOverwrittenButKept(t *testing.T) {
	f := newPromptFixture(t)
	id := f.importHost()
	f.render(id)
	f.write("CLAUDE.md", f.read("CLAUDE.md")+"\n## Scratch\n\nnot wanted\n")
	reconciliation, _ := f.store.ReconcilePromptDrifts()
	drift := reconciliation.Detected[0]
	if _, err := f.store.DismissPromptDrift(drift.ID); err != nil {
		t.Fatal(err)
	}
	if result := f.render(id); result.RefusedReason != "" {
		t.Fatal(result.RefusedReason)
	}
	if strings.Contains(f.read("CLAUDE.md"), "not wanted") {
		t.Fatal("dismissed edit survived the render")
	}
	kept, err := f.store.GetPromptDriftDiskContent(drift.ID)
	if err != nil || !strings.Contains(string(kept), "not wanted") {
		t.Fatal("the dismissed content was not kept on the drift")
	}
}

func TestSectionBodyThatWouldReadBackAsTwoSectionsIsRefused(t *testing.T) {
	f := newPromptFixture(t)
	id := f.importHost()
	_, err := f.store.CreatePromptSection(id, &PromptSection{Heading: "## A", Body: "x\n\n## B\n\ny", Enabled: true}, "")
	if err == nil {
		t.Fatal("accepted a body holding a section-level heading")
	}
	if _, err := f.store.CreatePromptSection(id, &PromptSection{Heading: "## A", Body: "```\n## B\n```", Enabled: true}, ""); err != nil {
		t.Fatalf("refused a heading-like line inside a code fence: %v", err)
	}
}

func TestResolveContextDeliversOncePerHarness(t *testing.T) {
	f := newPromptFixture(t)
	f.write("repos/logstack/AGENTS.md", "# Logstack\n\nGo service.\n")
	if _, err := f.store.Scan(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ImportUntrackedPromptFiles([]string{"AGENTS.md", "CLAUDE.md"}); err != nil {
		t.Fatal(err)
	}
	workDir := filepath.Join(f.home, "repos", "logstack", "cmd")

	if _, err := f.store.ResolveContext("claude_code", workDir); !errors.Is(err, ErrPromptHarnessDeliveryUnknown) {
		t.Fatalf("unknown harness: err = %v, want ErrPromptHarnessDeliveryUnknown", err)
	}
	if _, err := f.store.SetPromptHarnessDelivery(PromptHarnessDelivery{Harness: "codex", Delivery: PromptDeliveryInject}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SetPromptHarnessDelivery(PromptHarnessDelivery{Harness: "claude_code", Delivery: PromptDeliveryNativeFile, NativeRelativePath: "CLAUDE.md"}); err != nil {
		t.Fatal(err)
	}

	injected, err := f.store.ResolveContext("codex", workDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Be plain.", "Repos live here.", "Go service."} {
		if !strings.Contains(injected.Content, want) {
			t.Fatalf("inject harness is missing %q", want)
		}
	}
	if strings.Index(injected.Content, "Be plain.") > strings.Index(injected.Content, "Go service.") {
		t.Fatal("project sections came before host sections")
	}

	native, err := f.store.ResolveContext("claude_code", workDir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(native.Content, "Be plain.") {
		t.Fatal("the host prompt was injected into a harness that reads it from its own file")
	}
	if !strings.Contains(native.Content, "Go service.") {
		t.Fatal("logstack renders to no CLAUDE.md, so its sections had to be injected and were not")
	}

	elsewhere, _ := f.store.ResolveContext("codex", filepath.Join(f.home, "repos", "other"))
	if strings.Contains(elsewhere.Content, "Go service.") {
		t.Fatal("a project's sections reached a session outside its root")
	}
}

func TestWorktreeCopiesAreIgnoredNotDeleted(t *testing.T) {
	f := newPromptFixture(t)
	f.write("repos/logstack/AGENTS.md", "# Logstack\n")
	f.write("repos/logstack/.git/HEAD", "ref: refs/heads/main\n")
	f.write("repos/logstack-copy/AGENTS.md", "# Logstack\n")
	f.write("repos/logstack-copy/.git", "gitdir: ../logstack/.git/worktrees/copy\n")
	if _, err := f.store.CreateTrackedFileIgnoreRule(TrackedFileIgnoreKindGitWorktree, "", "a worktree's files are copies"); err != nil {
		t.Fatal(err)
	}
	result, err := f.store.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if result.Ignored != 1 {
		t.Fatalf("ignored = %d, want 1", result.Ignored)
	}
	visible, _ := f.store.ListTrackedFiles("project", "")
	all, _ := f.store.ListTrackedFilesIncludingIgnored("project", "")
	if len(visible) != 1 || len(all) != 2 {
		t.Fatalf("visible = %d, all = %d; want 1 and 2", len(visible), len(all))
	}
	reports, err := f.store.ImportUntrackedPromptFiles(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range reports {
		for _, file := range report.Files {
			if strings.Contains(file.Path, "logstack-copy") {
				t.Fatal("a worktree copy became a prompt collection")
			}
		}
	}
}

func TestPerFileSectionsTableIsSetAsideNotDropped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "agents.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Put the database back into the shape the first prompt-collections commit left.
	for _, statement := range []string{
		`DROP TABLE prompt_sections`,
		`CREATE TABLE prompt_sections (id INTEGER PRIMARY KEY, collection_id INTEGER NOT NULL, title TEXT NOT NULL, heading TEXT, body TEXT NOT NULL, applies_to TEXT NOT NULL, priority INTEGER NOT NULL DEFAULT 0, enabled INTEGER NOT NULL DEFAULT 1, source_path TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
		`CREATE INDEX idx_prompt_sections_collection ON prompt_sections(collection_id, priority, id)`,
		`INSERT INTO prompt_sections (collection_id, title, body, applies_to, created_at, updated_at) VALUES (1, 'old', 'old body', 'claude', 1, 1)`,
	} {
		if _, err := store.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	store.Close()

	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var kept, fresh int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM prompt_sections_legacy_per_file`).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM prompt_sections`).Scan(&fresh); err != nil {
		t.Fatal(err)
	}
	if kept != 1 || fresh != 0 {
		t.Fatalf("legacy rows = %d, new rows = %d; want 1 and 0", kept, fresh)
	}
}
