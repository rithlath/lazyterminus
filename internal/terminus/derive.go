package terminus

import (
	"strings"
)

// SourceEnv returns the environment code is deployed from when deploying to
// target: dev → test, test → live. Multidevs merge into dev. Returns "" for
// dev itself.
func SourceEnv(target string) string {
	switch target {
	case "test":
		return "dev"
	case "live":
		return "test"
	}
	return ""
}

// NextEnv returns where code in env goes next: dev → test, test → live,
// multidev → dev, live → "".
func NextEnv(env string) string {
	switch env {
	case "dev":
		return "test"
	case "test":
		return "live"
	case "live":
		return ""
	}
	return "dev"
}

// DeployableCommits returns the commits that a deploy to target would
// promote: commits in the source environment's code log that carry the
// source label but not the target label. This mirrors Terminus's own
// Environment::countDeployableCommits().
func DeployableCommits(sourceLog []Commit, source, target string) []Commit {
	var out []Commit
	for _, c := range sourceLog {
		l := c.LabelSet()
		if l[source] && !l[target] {
			out = append(out, c)
		}
	}
	return out
}

// CommitsNotIn returns commits from log whose hash does not appear in other.
// Multidev code logs only carry the multidev's own label, so comparing hashes
// against dev's log is the only way to see what a multidev has that dev
// doesn't. Both logs are capped by Terminus, so this is approximate for
// long-lived branches.
func CommitsNotIn(log, other []Commit) []Commit {
	seen := make(map[string]bool, len(other))
	for _, c := range other {
		seen[c.Hash] = true
	}
	var out []Commit
	for _, c := range log {
		if !seen[c.Hash] {
			out = append(out, c)
		}
	}
	return out
}

// FailureReason summarizes why a workflow failed from its operations: the
// description of the first failed operation.
func FailureReason(ops []WorkflowOp) string {
	for _, op := range ops {
		if op.Result != nil && *op.Result == StatusFailed {
			if op.Description != "" {
				return op.Description
			}
			return op.Type + " operation failed"
		}
	}
	return ""
}

// Progress returns how many operations have finished out of the total.
func Progress(ops []WorkflowOp) (done, total int) {
	for _, op := range ops {
		total++
		if op.Result != nil && *op.Result != "" {
			done++
		}
	}
	return done, total
}

// IsDeployWorkflow reports whether wf is a code deploy into env.
func IsDeployWorkflow(wf Workflow, env string) bool {
	return strings.HasPrefix(wf.Workflow, "Deploy code to") && wf.EnvName() == env
}

// LastDeploy returns the most recent deploy workflow into env, if any.
// workflows must be newest first.
func LastDeploy(workflows []Workflow, env string) (Workflow, bool) {
	for _, wf := range workflows {
		if IsDeployWorkflow(wf, env) {
			return wf, true
		}
	}
	return Workflow{}, false
}

// LastFailed returns the most recent failed workflow for env that has not
// been superseded by a later success of the same workflow (e.g. a failed
// deploy followed by a successful one). workflows must be newest first.
func LastFailed(workflows []Workflow, env string) (Workflow, bool) {
	succeeded := map[string]bool{}
	for _, wf := range workflows {
		if wf.EnvName() != env {
			continue
		}
		switch {
		case wf.Status == StatusSucceeded:
			succeeded[wf.Workflow] = true
		case wf.Status == StatusFailed && !succeeded[wf.Workflow]:
			return wf, true
		}
	}
	return Workflow{}, false
}

// SiteEnv joins a site and environment as Terminus expects.
func SiteEnv(site, env string) string { return site + "." + env }

// SplitSiteEnv splits "site.env". ok is false if there is no dot.
func SplitSiteEnv(s string) (site, env string, ok bool) {
	i := strings.LastIndexByte(s, '.')
	if i <= 0 || i == len(s)-1 {
		return s, "", false
	}
	return s[:i], s[i+1:], true
}
