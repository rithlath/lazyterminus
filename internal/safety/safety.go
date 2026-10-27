// Package safety decides what confirmation a Terminus command needs and
// enforces it: Guard refuses to pass any mutating command to the runner
// unless it carries an Approval produced by Approve for exactly that argv.
//
// The destructive-command list lives here and only here.
package safety

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/rithlath/lazyterminus/internal/terminus"
)

// Level is the confirmation a command requires.
type Level int

// Confirmation levels, in increasing strictness.
const (
	None    Level = iota // read-only
	YesNo                // standard y/n showing the exact command
	Typed                // type the full site.env (or site) name
	Blocked              // never allowed
)

func (l Level) String() string {
	return [...]string{"none", "yes/no", "typed", "blocked"}[l]
}

// Context carries facts about current state that change the required level.
type Context struct {
	// UncommittedChanges is true when the target environment has SFTP
	// changes that a connection:set to git would discard.
	UncommittedChanges bool
}

// rule marks a command (or glob pattern) as destructive, optionally only
// when a condition holds.
type rule struct {
	pattern string
	reason  string
	when    func(a terminus.Args, c Context) bool
}

// destructive is THE list of destructive commands: each requires typed
// confirmation on any environment. Extend it from config with
// extra_destructive_commands.
var destructive = []rule{
	{pattern: "env:wipe", reason: "wipes the database and files"},
	{pattern: "multidev:delete", reason: "deletes the environment and its database and files"},
	{pattern: "site:delete", reason: "deletes the entire site"},
	{pattern: "import:*", reason: "overwrites site content with an import"},
	{pattern: "backup:restore", reason: "overwrites the environment with a backup"},
	{pattern: "env:clone-content", reason: "overwrites the target's database/files",
		when: func(a terminus.Args, _ Context) bool {
			p := a.Positionals()
			return len(p) > 1 && (p[1] == "test" || p[1] == "live")
		}},
	{pattern: "connection:set", reason: "uncommitted SFTP changes will be lost",
		when: func(a terminus.Args, c Context) bool {
			p := a.Positionals()
			return c.UncommittedChanges && len(p) > 1 && p[1] == "git"
		}},
	{pattern: "env:deploy", reason: "deploys to production",
		when: func(a terminus.Args, _ Context) bool { return targetEnv(a) == "live" }},
}

// blocked commands are never run from lazyterminus.
var blocked = []rule{
	{pattern: "env:clone-content", reason: "cloning content into live is not allowed",
		when: func(a terminus.Args, _ Context) bool {
			p := a.Positionals()
			return len(p) > 1 && p[1] == "live"
		}},
	{pattern: "env:deploy", reason: "--sync-content is never allowed when deploying to live",
		when: func(a terminus.Args, _ Context) bool { return targetEnv(a) == "live" && a.HasFlag("sync-content") }},
}

// Policy holds the configured safety settings.
type Policy struct {
	ProtectedEnvs    []string
	ExtraDestructive []string
	ReadOnly         bool
}

// Decision is the outcome of evaluating one command.
type Decision struct {
	Args    []string
	Level   Level
	Phrase  string   // text the user must type for Typed
	Target  string   // site.env (or site) the command affects
	Reasons []string // why the level is what it is
}

// targetEnv returns the environment a command affects: the clone target
// for env:clone-content, the new environment for multidev:create, and the
// env part of the first site.env argument otherwise.
func targetEnv(a terminus.Args) string {
	_, env := target(a)
	return env
}

func target(a terminus.Args) (site, env string) {
	p := a.Positionals()
	if len(p) == 0 {
		return "", ""
	}
	site, env, ok := terminus.SplitSiteEnv(p[0])
	if !ok {
		return p[0], ""
	}
	switch terminus.Canonical(a.Command()) {
	case "env:clone-content", "multidev:create":
		if len(p) > 1 {
			env = p[1]
		}
	}
	return site, env
}

func matches(pattern, cmd string) bool {
	ok, err := path.Match(pattern, cmd)
	return err == nil && ok
}

// Protected reports whether env is protected. live is always protected.
func (p Policy) Protected(env string) bool {
	return env == "live" || slices.Contains(p.ProtectedEnvs, env)
}

// Evaluate returns the confirmation required to run args.
func (p Policy) Evaluate(args []string, c Context) Decision {
	a := terminus.Args(args)
	d := Decision{Args: slices.Clone(args)}
	if !terminus.IsMutating(args) {
		return d
	}
	cmd := terminus.Canonical(a.Command())
	site, env := target(a)
	switch {
	case env != "":
		d.Target = terminus.SiteEnv(site, env)
	case site != "":
		d.Target = site
	default:
		d.Target = cmd
	}
	if p.ReadOnly {
		d.Level = Blocked
		d.Reasons = append(d.Reasons, "read_only is enabled in the config")
		return d
	}
	for _, r := range blocked {
		if matches(r.pattern, cmd) && (r.when == nil || r.when(a, c)) {
			d.Level = Blocked
			d.Reasons = append(d.Reasons, r.reason)
			return d
		}
	}
	d.Level = YesNo
	if !terminus.IsReadOnly(cmd) && cmd != a.Command() {
		d.Reasons = append(d.Reasons, fmt.Sprintf("%s is an alias of %s", a.Command(), cmd))
	}
	if env != "" && p.Protected(env) {
		d.Level = Typed
		d.Reasons = append(d.Reasons, fmt.Sprintf("%s is a protected environment", env))
	}
	for _, r := range destructive {
		if matches(r.pattern, cmd) && (r.when == nil || r.when(a, c)) {
			d.Level = Typed
			d.Reasons = append(d.Reasons, r.reason)
		}
	}
	for _, pat := range p.ExtraDestructive {
		if matches(pat, cmd) || matches(pat, a.Command()) {
			d.Level = Typed
			d.Reasons = append(d.Reasons, fmt.Sprintf("listed in extra_destructive_commands (%s)", pat))
		}
	}
	if d.Level == Typed {
		d.Phrase = d.Target
	}
	return d
}

// EvaluatePlan returns the strictest decision across several commands that
// are confirmed together (e.g. env:wake then remote:drush).
func (p Policy) EvaluatePlan(steps [][]string, c Context) Decision {
	var worst Decision
	for i, s := range steps {
		d := p.Evaluate(s, c)
		if i == 0 || d.Level > worst.Level {
			reasons := worst.Reasons
			worst = d
			worst.Reasons = append(reasons, d.Reasons...)
		} else {
			worst.Reasons = append(worst.Reasons, d.Reasons...)
		}
	}
	worst.Args = nil
	return worst
}

// Approval authorizes specific argvs to run once each. Only Approve creates
// one; its fields are unexported so callers can't forge it.
type Approval struct {
	mu    sync.Mutex
	steps [][]string
	used  []bool
	ctx   Context
	level Level
}

// Errors returned by Approve and Guard.
var (
	ErrBlocked       = errors.New("command is blocked by the safety policy")
	ErrWrongPhrase   = errors.New("confirmation text does not match")
	ErrNotConfirmed  = errors.New("not confirmed")
	ErrNoApproval    = errors.New("refusing to run a mutating command without confirmation")
	ErrApprovalLevel = errors.New("approval is weaker than the command requires")
)

// Approve checks the user's response against the plan's requirement and
// returns an Approval for exactly those steps. typed is what the user typed
// (Typed level); confirmed is the y/n answer (YesNo level).
func (p Policy) Approve(steps [][]string, c Context, typed string, confirmed bool) (*Approval, error) {
	d := p.EvaluatePlan(steps, c)
	switch d.Level {
	case Blocked:
		return nil, fmt.Errorf("%w: %s", ErrBlocked, strings.Join(d.Reasons, "; "))
	case Typed:
		if strings.TrimSpace(typed) != d.Phrase {
			return nil, ErrWrongPhrase
		}
	case YesNo:
		if !confirmed {
			return nil, ErrNotConfirmed
		}
	}
	a := &Approval{ctx: c, level: d.Level, used: make([]bool, len(steps))}
	for _, s := range steps {
		a.steps = append(a.steps, slices.Clone(s))
	}
	return a, nil
}

// consume marks the first unused step equal to args as used.
func (a *Approval) consume(args []string) (Context, Level, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, s := range a.steps {
		if !a.used[i] && slices.Equal(s, args) {
			a.used[i] = true
			return a.ctx, a.level, true
		}
	}
	return Context{}, None, false
}

type approvalKey struct{}

// WithApproval attaches an approval to ctx for Guard to check.
func WithApproval(ctx context.Context, a *Approval) context.Context {
	return context.WithValue(ctx, approvalKey{}, a)
}

// Guard is the outermost runner: read-only commands pass through; mutating
// commands need an unused Approval for exactly their argv, at a level at
// least as strict as the policy requires right now.
type Guard struct {
	Inner  terminus.Runner
	Policy Policy
}

func (g *Guard) check(ctx context.Context, args []string) error {
	if !terminus.IsMutating(args) {
		return nil
	}
	a, _ := ctx.Value(approvalKey{}).(*Approval)
	if a == nil {
		return fmt.Errorf("%w: %s", ErrNoApproval, terminus.Args(args).String())
	}
	c, lvl, ok := a.consume(args)
	if !ok {
		return fmt.Errorf("%w: %s was not the approved command", ErrNoApproval, terminus.Args(args).String())
	}
	d := g.Policy.Evaluate(args, c)
	if d.Level == Blocked {
		return fmt.Errorf("%w: %s", ErrBlocked, strings.Join(d.Reasons, "; "))
	}
	if lvl < d.Level {
		return ErrApprovalLevel
	}
	return nil
}

// Run implements terminus.Runner.
func (g *Guard) Run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	if err := g.check(ctx, args); err != nil {
		return nil, nil, err
	}
	return g.Inner.Run(ctx, args...)
}

// Stream implements terminus.Runner.
func (g *Guard) Stream(ctx context.Context, args ...string) (<-chan string, <-chan error) {
	if err := g.check(ctx, args); err != nil {
		lines, errc := make(chan string), make(chan error, 1)
		close(lines)
		errc <- err
		close(errc)
		return lines, errc
	}
	return g.Inner.Stream(ctx, args...)
}

// Command implements terminus.Execer.
func (g *Guard) Command(ctx context.Context, args ...string) (*exec.Cmd, error) {
	if err := g.check(ctx, args); err != nil {
		return nil, err
	}
	return terminus.CommandFor(g.Inner, ctx, args...)
}
