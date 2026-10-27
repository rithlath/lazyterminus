package safety

import (
	"github.com/rithlath/lazyterminus/internal/config"
	"github.com/rithlath/lazyterminus/internal/terminus"
)

// PolicyFrom builds the policy from config.
func PolicyFrom(cfg config.Config) Policy {
	return Policy{ProtectedEnvs: cfg.ProtectedEnvs, ExtraDestructive: cfg.ExtraDestructiveCommands, ReadOnly: cfg.ReadOnly}
}

// Chain builds the runner stack every command goes through, outermost
// first:
//
//	Guard    refuses mutating commands without a matching confirmation
//	DryRun   (dryRun) prints mutating commands instead of running them
//	History  appends executed mutating commands to the audit log
//	Limited  caps concurrent terminus processes
//	Recorder keeps every command that actually runs for the History view
//
// The returned DryRun is nil unless dryRun is set.
func Chain(base terminus.Runner, cfg config.Config, historyPath string, dryRun bool) (terminus.Runner, *terminus.DryRun, *terminus.Recorder) {
	rec := &terminus.Recorder{Inner: base}
	var r terminus.Runner = terminus.NewLimited(rec, cfg.MaxConcurrent)
	r = &terminus.History{Inner: r, Path: historyPath}
	var dry *terminus.DryRun
	if dryRun {
		dry = &terminus.DryRun{Inner: r}
		r = dry
	}
	return &Guard{Inner: r, Policy: PolicyFrom(cfg)}, dry, rec
}
