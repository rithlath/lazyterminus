package terminus

import (
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "real", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseSites(t *testing.T) {
	sites, err := ParseSites(fixture(t, "site_list.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 4 {
		t.Fatalf("got %d sites, want 4", len(sites))
	}
	names := []string{"acme-intranet", "acme-portal", "acme-web", "sandbox-12"}
	for i, s := range sites {
		if s.Name != names[i] {
			t.Errorf("site %d = %q, want %q (sorted)", i, s.Name, names[i])
		}
		if s.Framework != "drupal8" || s.ID == "" || s.Created == 0 {
			t.Errorf("site %q incomplete: %+v", s.Name, s)
		}
	}
	if sites[2].PlanName != "Elite" {
		t.Errorf("acme-web plan = %q", sites[2].PlanName)
	}
}

func TestParseSite(t *testing.T) {
	s, err := ParseSite(fixture(t, "site_info_acme-web.json"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "acme-web" || s.UpstreamLabel != "Drupal (Composer Managed)" {
		t.Errorf("unexpected site: %+v", s)
	}
}

func TestParseEnvs(t *testing.T) {
	envs, err := ParseEnvs(fixture(t, "env_list_acme-web.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) < 4 {
		t.Fatalf("got %d envs", len(envs))
	}
	for i, want := range []string{"dev", "test", "live"} {
		if envs[i].ID != want {
			t.Errorf("env %d = %q, want %q", i, envs[i].ID, want)
		}
		if envs[i].IsMultidev() {
			t.Errorf("%s reported as multidev", want)
		}
	}
	for _, e := range envs[3:] {
		if !e.IsMultidev() {
			t.Errorf("%s should be a multidev", e.ID)
		}
	}
	for i := 4; i < len(envs); i++ {
		if envs[i-1].ID > envs[i].ID {
			t.Errorf("multidevs not sorted: %s before %s", envs[i-1].ID, envs[i].ID)
		}
	}
	dev := envs[0]
	if dev.ConnectionMode != "git" || dev.PHPVersion != "8.4" || !dev.Initialized || dev.Domain != "dev-acme-web.pantheonsite.io" {
		t.Errorf("dev = %+v", dev)
	}
}

func TestParseEnvInfo(t *testing.T) {
	e, err := ParseEnv(fixture(t, "env_info_acme-web_dev.json"))
	if err != nil {
		t.Fatal(err)
	}
	if e.ID != "dev" || e.DrushVersion != 10 || e.PHPVersion != "8.4" {
		t.Errorf("env info = %+v", e)
	}
}

func TestParseMultidevs(t *testing.T) {
	mds, err := ParseMultidevs(fixture(t, "multidev_list_acme-web.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(mds) == 0 || mds[0].ID == "" || mds[0].Domain == "" {
		t.Fatalf("multidevs = %+v", mds)
	}
}

func TestParseCodeLog(t *testing.T) {
	log, err := ParseCodeLog(fixture(t, "code_log_acme-web_test.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 15 {
		t.Fatalf("got %d commits", len(log))
	}
	first := log[0]
	if first.Labels != "test, live" || len(first.Hash) != 40 {
		t.Errorf("first commit = %+v", first)
	}
	l := first.LabelSet()
	if !l["test"] || !l["live"] || l["dev"] {
		t.Errorf("labels = %v", l)
	}
	if first.Time().IsZero() {
		t.Error("datetime did not parse")
	}
}

func TestParseDiffstatEmpty(t *testing.T) {
	ds, err := ParseDiffstat(fixture(t, "diffstat_acme-web_dev.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 0 {
		t.Errorf("want empty, got %+v", ds)
	}
}

func TestParseDiffstatRows(t *testing.T) {
	ds, err := ParseDiffstat([]byte(`[{"file":"a.php","status":"M","deletions":"2","additions":5}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 1 || ds[0].Deletions != 2 || ds[0].Additions != 5 {
		t.Errorf("diffstat = %+v", ds)
	}
}

func TestParseWorkflows(t *testing.T) {
	wfs, err := ParseWorkflows(fixture(t, "workflow_list_sandbox-12.json"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(wfs); i++ {
		if wfs[i-1].StartedAt < wfs[i].StartedAt {
			t.Fatalf("not newest first at %d", i)
		}
	}
	// The captured failed live deploy was followed by a successful one.
	if f, ok := LastFailed(wfs, "live"); ok {
		t.Errorf("superseded failure reported: %+v", f)
	}
	var failed Workflow
	for _, w := range wfs {
		if w.Status == StatusFailed {
			failed = w
		}
	}
	if failed.Workflow != `Deploy code to "live"` || failed.FinishedAt == nil || failed.Running() {
		t.Errorf("failed live deploy = %+v", failed)
	}
	if _, ok := LastDeploy(wfs, "live"); !ok {
		t.Error("no live deploy found")
	}
}

func TestParseWorkflowsNullEnvAndIntEpoch(t *testing.T) {
	wfs, err := ParseWorkflows(fixture(t, "workflow_list_acme-intranet.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sawNull bool
	for _, w := range wfs {
		if w.Env == nil {
			sawNull = true
			if w.EnvName() != "" {
				t.Error("EnvName of null env should be empty")
			}
		}
		if w.StartedAt.Time().IsZero() {
			t.Errorf("workflow %s has no start time", w.ID)
		}
	}
	if !sawNull {
		t.Error("fixture should contain a site-level (null env) workflow")
	}
}

func TestParseWorkflowStatus(t *testing.T) {
	w, err := ParseWorkflow(fixture(t, "workflow_status_acme-web.json"))
	if err != nil {
		t.Fatal(err)
	}
	if w.Status != StatusSucceeded || w.EnvName() != "dev" {
		t.Errorf("status = %+v", w)
	}
}

func TestParseWorkflowOps(t *testing.T) {
	ops, err := ParseWorkflowOps(fixture(t, "workflow_ops_failed_deploy.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := FailureReason(ops); got != "Bring application container to desired configuration state" {
		t.Errorf("reason = %q", got)
	}
	done, total := Progress(ops)
	if total != len(ops) || done >= total || done == 0 {
		t.Errorf("progress = %d/%d", done, total)
	}
	ops2, err := ParseWorkflowOps(fixture(t, "workflow_ops_failed_converge.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := FailureReason(ops2); got != "platform operation failed" {
		t.Errorf("reason with empty description = %q", got)
	}
}

func TestParseBackups(t *testing.T) {
	bs, err := ParseBackups(fixture(t, "backup_list_acme-web_live.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 9 || bs[0].Initiator != "automated" || bs[0].Size == "" {
		t.Fatalf("backups = %+v", bs)
	}
	for i := 1; i < len(bs); i++ {
		if bs[i-1].Date < bs[i].Date {
			t.Fatal("backups not newest first")
		}
	}
}

func TestParseUpstreamUpdatesEmpty(t *testing.T) {
	us, err := ParseUpstreamUpdates(fixture(t, "upstream_updates_list_acme-web_dev.json"))
	if err != nil || len(us) != 0 {
		t.Errorf("updates = %+v err=%v", us, err)
	}
}

func TestDecodeRowsSkipsNoise(t *testing.T) {
	rows, err := DecodeRows[Env]([]byte(" [notice] hi\n{\"dev\":{\"id\":\"dev\"}}"))
	if err != nil || len(rows) != 1 || rows[0].Key != "dev" {
		t.Errorf("rows=%+v err=%v", rows, err)
	}
	if _, err := DecodeRows[Env](nil); err == nil {
		t.Error("empty output should error")
	}
}

func TestErrorMessage(t *testing.T) {
	stderr := fixture(t, "upstream_updates_status_acme-web_dev.txt")
	if got := ErrorMessage(stderr); got != "The operation failed to complete." {
		t.Errorf("ErrorMessage = %q", got)
	}
}

func TestDecodeRowsTrailingNoise(t *testing.T) {
	rows, err := DecodeRows[Env]([]byte("{\"dev\":{\"id\":\"dev\"}}\n[notice] done\n"))
	if err != nil || len(rows) != 1 {
		t.Errorf("rows=%+v err=%v", rows, err)
	}
}

func TestDecodeRowsNull(t *testing.T) {
	rows, err := DecodeRows[Commit]([]byte("null\n"))
	if err != nil || len(rows) != 0 {
		t.Errorf("rows=%v err=%v", rows, err)
	}
}

func TestParseDomainsAndURLs(t *testing.T) {
	ds, err := ParseDomains([]byte(`{
		"live-acme.pantheonsite.io": {"id": "live-acme.pantheonsite.io", "type": "platform", "status": "OK", "primary": false, "deletable": false},
		"acme.example.com": {"id": "acme.example.com", "type": "custom", "status": "OK", "primary": "false", "deletable": true},
		"www.acme.example.com": {"id": "www.acme.example.com", "type": "custom", "status": "OK", "primary": "true", "deletable": true}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 3 {
		t.Fatalf("got %d domains, want 3", len(ds))
	}
	custom := CustomDomains(ds)
	if len(custom) != 2 || custom[0].ID != "www.acme.example.com" || custom[1].ID != "acme.example.com" {
		t.Errorf("custom = %+v, want primary first", custom)
	}
	e := Env{ID: "live", Domain: "live-acme.pantheonsite.io"}
	if got := PrimaryURL(e, ds); got != "https://www.acme.example.com" {
		t.Errorf("PrimaryURL = %q", got)
	}
	if got := PrimaryURL(e, nil); got != "https://live-acme.pantheonsite.io" {
		t.Errorf("PrimaryURL without domains = %q", got)
	}
	if empty, err := ParseDomains([]byte("[]")); err != nil || len(empty) != 0 {
		t.Errorf("empty list: %v, %v", empty, err)
	}
}

func TestCommitSubject(t *testing.T) {
	for _, c := range []struct{ msg, want string }{
		{"PROJ-77: Add hero banner block", "PROJ-77: Add hero banner block"},
		{"Fix typo\n\nLonger body", "Fix typo"},
		// Full messages (from the local repo).
		{"Merge pull request #54 from kalamuna/icare\n\nICare feedback form", "ICare feedback form (#54)"},
		{"Merge pull request #54 from kalamuna/icare  ICare feedback form  Adds the webform.", "ICare feedback form (#54)"},
		{"Merge pull request #48 from kalamuna/sync-config", "PR #48 sync-config"}, // no title
		{"Merge branch 'dev' into feature", "Merge branch 'dev' into feature"},
		// Cut at 50 characters by Pantheon.
		{"Merge pull request #54 from kalamuna/icare  ICare", "ICare (#54)"}, // 49: not cut
		{"Merge pull request #54 from kalamuna/icaree  ICare", "PR #54 icaree: ICare…"},
		{"Merge pull request #53 from kalamuna/vendor-upd  V", "PR #53 vendor-upd"},
		{"Merge pull request #47 from kalamuna/SA-CONTRIB-20", "PR #47 SA-CONTRIB-20…"},
		{"Update the news listing view so it sorts by date d", "Update the news listing view so it sorts by date d…"},
	} {
		if got := (Commit{Message: c.msg}).Subject(); got != c.want {
			t.Errorf("Subject(%q) [%d] = %q, want %q", c.msg, len(c.msg), got, c.want)
		}
	}
}
