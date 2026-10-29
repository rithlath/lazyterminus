// Package terminus wraps the Terminus CLI: a Runner abstraction (real, mock,
// and wrappers), typed JSON models matching Terminus 4.x output, and parsers.
package terminus

import (
	"bytes"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Site is one row of `site:list --format=json` (also `site:info`).
type Site struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Label       string `json:"label,omitempty"`
	PlanName    string `json:"plan_name"`
	Framework   string `json:"framework"`
	Region      string `json:"region"`
	Owner       string `json:"owner"`
	Created     Epoch  `json:"created"`
	Memberships string `json:"memberships,omitempty"`
	Frozen      bool   `json:"frozen"`
	Upstream    string `json:"upstream,omitempty"`
	// UpstreamLabel is only present in site:info.
	UpstreamLabel string `json:"upstream_label,omitempty"`
}

// Env is one row of `env:list --fields=* --format=json` or `env:info`.
type Env struct {
	ID                   string `json:"id"`
	Created              Epoch  `json:"created"`
	Domain               string `json:"domain"`
	ConnectionMode       string `json:"connection_mode"`
	Locked               bool   `json:"locked"`
	Initialized          bool   `json:"initialized"`
	PHPVersion           string `json:"php_version,omitempty"`
	PHPRuntimeGeneration int    `json:"php_runtime_generation,omitempty"`
	// DrushVersion is only present in env:info.
	DrushVersion FlexInt `json:"drush_version,omitempty"`
}

// IsMultidev reports whether the environment is a multidev (anything other
// than the three standard environments).
func (e Env) IsMultidev() bool { return IsMultidevName(e.ID) }

// IsMultidevName reports whether env is not dev, test, or live.
func IsMultidevName(env string) bool {
	return env != "dev" && env != "test" && env != "live"
}

// Multidev is one row of `multidev:list --fields=* --format=json`.
type Multidev struct {
	ID          string `json:"id"`
	Created     Epoch  `json:"created"`
	Domain      string `json:"domain"`
	OnServerDev any    `json:"onserverdev"`
	Locked      bool   `json:"locked"`
	Initialized bool   `json:"initialized"`
}

// Commit is one row of `env:code-log --format=json`. Labels is a
// comma-separated list of environments the commit is deployed to.
type Commit struct {
	Datetime string `json:"datetime"`
	Author   string `json:"author"`
	Labels   string `json:"labels"`
	Hash     string `json:"hash"`
	Message  string `json:"message"`
}

// LabelSet returns the environments this commit is on.
func (c Commit) LabelSet() map[string]bool {
	set := map[string]bool{}
	for l := range strings.SplitSeq(c.Labels, ",") {
		if l = strings.TrimSpace(l); l != "" {
			set[l] = true
		}
	}
	return set
}

// Time parses Datetime (UTC, no zone suffix in Terminus output).
func (c Commit) Time() time.Time {
	t, _ := time.Parse("2006-01-02T15:04:05", c.Datetime)
	return t
}

// CodeLogMessageMax is where Pantheon's code log cuts commit messages off.
const CodeLogMessageMax = 50

// mergePR matches GitHub's merge commit header; the PR title follows it.
var mergePR = regexp.MustCompile(`^Merge pull request #(\d+) from (\S+)(.*)$`)

// Subject is the line worth showing for a commit: the first line, or for a
// GitHub merge, the PR title with its number ("ICare feedback form (#54)").
// The body may come with its newlines or flattened into spaces, so a double
// space ends the title too. When Pantheon's cutoff leaves only a stub of
// the title, the branch name says more: "PR #53 vendor-updates".
func (c Commit) Subject() string {
	msg := strings.TrimSpace(c.Message)
	cut := utf8.RuneCountInString(c.Message) == CodeLogMessageMax || len(c.Message) == CodeLogMessageMax
	flat := strings.ReplaceAll(msg, "\n", "  ")
	if m := mergePR.FindStringSubmatch(flat); m != nil {
		num, branch, rest := m[1], m[2], strings.TrimSpace(m[3])
		if _, b, ok := strings.Cut(branch, "/"); ok {
			branch = b
		}
		title, _, _ := strings.Cut(rest, "  ")
		title = strings.TrimSpace(title)
		complete := !cut || strings.Contains(rest, "  ")
		switch {
		case title != "" && complete:
			return title + " (#" + num + ")"
		case title == "" && cut:
			return "PR #" + num + " " + branch + "…"
		case utf8.RuneCountInString(title) >= 4:
			return "PR #" + num + " " + branch + ": " + title + "…"
		}
		return "PR #" + num + " " + branch
	}
	first, _, multi := strings.Cut(msg, "\n")
	if cut && !multi {
		first += "…"
	}
	return strings.TrimSpace(first)
}

// DiffstatEntry is one row of `env:diffstat --format=json`.
type DiffstatEntry struct {
	File      string  `json:"file"`
	Status    string  `json:"status"`
	Deletions FlexInt `json:"deletions"`
	Additions FlexInt `json:"additions"`
}

// Workflow is one row of `workflow:list --format=json`. Env is null for
// site-level workflows such as "Create a Multidev environment".
type Workflow struct {
	ID         string  `json:"id"`
	Env        *string `json:"env"`
	Workflow   string  `json:"workflow"`
	User       string  `json:"user"`
	Status     string  `json:"status"`
	StartedAt  Epoch   `json:"started_at"`
	FinishedAt *Epoch  `json:"finished_at"`
	Time       string  `json:"time"`
}

// Workflow status values reported by Terminus.
const (
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
)

// EnvName returns the workflow's environment or "" for site-level workflows.
func (w Workflow) EnvName() string {
	if w.Env == nil {
		return ""
	}
	return *w.Env
}

// Running reports whether the workflow has not finished.
func (w Workflow) Running() bool {
	return w.Status == StatusRunning || (w.FinishedAt == nil && w.Status != StatusFailed && w.Status != StatusSucceeded)
}

// WorkflowOp is one row of `workflow:info:operations --format=json`.
type WorkflowOp struct {
	ID          string  `json:"-"`
	Type        string  `json:"type"`
	Result      *string `json:"result"`
	Duration    *string `json:"duration"`
	Description string  `json:"description"`
}

// Backup is one row of `backup:list --fields=* --format=json`.
type Backup struct {
	File      string `json:"file"`
	Size      string `json:"size"`
	Date      Epoch  `json:"date"`
	Expiry    Epoch  `json:"expiry"`
	Initiator string `json:"initiator"`
}

// UpstreamUpdate is one row of `upstream:updates:list --format=json`.
type UpstreamUpdate struct {
	Hash     string `json:"hash"`
	Datetime string `json:"datetime"`
	Message  string `json:"message"`
	Author   string `json:"author"`
}

// Epoch is a Unix timestamp that Terminus emits as either an integer or a
// float with fractional seconds.
type Epoch float64

// UnmarshalJSON accepts numbers, numeric strings, and null.
func (e *Epoch) UnmarshalJSON(b []byte) error {
	b = bytes.Trim(b, `"`)
	if string(b) == "null" || len(b) == 0 {
		*e = 0
		return nil
	}
	f, err := strconv.ParseFloat(string(b), 64)
	if err != nil {
		return fmt.Errorf("epoch %q: %w", b, err)
	}
	*e = Epoch(f)
	return nil
}

// Time converts to time.Time; zero epochs map to the zero time.
func (e Epoch) Time() time.Time {
	if e == 0 {
		return time.Time{}
	}
	sec, frac := math.Modf(float64(e))
	return time.Unix(int64(sec), int64(frac*1e9))
}

// EpochOf converts a time to an Epoch.
func EpochOf(t time.Time) Epoch { return Epoch(float64(t.UnixNano()) / 1e9) }

// FlexInt accepts a JSON number or numeric string.
type FlexInt int

// UnmarshalJSON accepts numbers, numeric strings, empty strings, and null.
func (f *FlexInt) UnmarshalJSON(b []byte) error {
	b = bytes.Trim(b, `"`)
	if string(b) == "null" || len(b) == 0 {
		*f = 0
		return nil
	}
	n, err := strconv.ParseFloat(string(b), 64)
	if err != nil {
		return fmt.Errorf("int %q: %w", b, err)
	}
	*f = FlexInt(n)
	return nil
}

// Domain is one row of `domain:list --fields=* --format=json`. Type is
// "platform" for the *.pantheonsite.io domain and "custom" for added ones.
// The shape is from Terminus's Domain::serialize(); no real capture yet.
type Domain struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`
	Status    string   `json:"status,omitempty"`
	Primary   FlexBool `json:"primary"`
	Deletable FlexBool `json:"deletable"`
}

// FlexBool accepts a JSON boolean, a "true"/"false" or "1"/"0" string, or
// null.
type FlexBool bool

// UnmarshalJSON implements json.Unmarshaler.
func (f *FlexBool) UnmarshalJSON(b []byte) error {
	switch strings.ToLower(string(bytes.Trim(b, `"`))) {
	case "true", "1", "yes":
		*f = true
	default:
		*f = false
	}
	return nil
}

// URL is the https address of a domain or hostname.
func URL(host string) string {
	if host == "" {
		return ""
	}
	return "https://" + host
}

// CustomDomains returns domains that aren't Pantheon's platform domain,
// primary first, then in Terminus's order.
func CustomDomains(ds []Domain) []Domain {
	var out []Domain
	for _, d := range ds {
		if d.Type != "platform" && !strings.HasSuffix(d.ID, ".pantheonsite.io") {
			out = append(out, d)
		}
	}
	for i, d := range out {
		if d.Primary && i > 0 {
			copy(out[1:i+1], out[:i])
			out[0] = d
			break
		}
	}
	return out
}

// PrimaryURL is the address to open for an environment: its primary custom
// domain when one is set, else the platform domain.
func PrimaryURL(e Env, ds []Domain) string {
	for _, d := range ds {
		if d.Primary && d.ID != "" {
			return URL(d.ID)
		}
	}
	return URL(e.Domain)
}
