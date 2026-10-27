package terminus

// readOnly lists Terminus commands known not to change remote state. It is
// deliberately explicit rather than pattern-based (no "*:list") so that a
// plugin command with an innocent-looking name is never treated as safe.
// Anything not listed here is treated as mutating.
var readOnly = map[string]bool{
	"--version":                true,
	"_complete":                true,
	"aliases":                  true, // writes local drush alias files only
	"art":                      true,
	"art:list":                 true,
	"auth:whoami":              true,
	"backup:automatic:info":    true,
	"backup:get":               true,
	"backup:info":              true,
	"backup:list":              true,
	"branch:list":              true,
	"completion":               true,
	"connection:info":          true,
	"dashboard:view":           true,
	"domain:dns":               true,
	"domain:list":              true,
	"domain:lookup":            true,
	"env:code-log":             true,
	"env:diffstat":             true,
	"env:info":                 true,
	"env:list":                 true,
	"env:metrics":              true,
	"env:view":                 true,
	"help":                     true,
	"https:info":               true,
	"list":                     true,
	"lock:info":                true,
	"machine-token:list":       true,
	"multidev:list":            true,
	"new-relic:info":           true,
	"node:builds:list":         true,
	"node:logs:build:get":      true,
	"node:logs:runtime:get":    true,
	"org:info":                 true,
	"org:list":                 true,
	"org:people:list":          true,
	"org:site:list":            true,
	"org:upstream:list":        true,
	"payment-method:list":      true,
	"plan:info":                true,
	"plan:list":                true,
	"secret:org:list":          true,
	"secret:site:list":         true,
	"self:config:dump":         true,
	"self:info":                true,
	"self:plugin:list":         true,
	"self:plugin:search":       true,
	"site:info":                true,
	"site:list":                true,
	"site:lookup":              true,
	"site:org:list":            true,
	"site:team:list":           true,
	"ssh-key:list":             true,
	"tag:list":                 true,
	"upstream:info":            true,
	"upstream:list":            true,
	"upstream:updates:list":    true,
	"upstream:updates:status":  true,
	"vcs:connection:list":      true,
	"workflow:info:logs":       true,
	"workflow:info:operations": true,
	"workflow:info:status":     true,
	"workflow:list":            true,
	"workflow:wait":            true,
	"workflow:watch":           true,
}

// IsReadOnly reports whether a command name is on the known read-only list.
// Unknown commands, including all plugin commands, return false.
func IsReadOnly(command string) bool { return readOnly[Canonical(command)] }

// IsMutating reports whether args invoke a command not known to be read-only.
// A bare `--help` invocation of any command is read-only.
func IsMutating(args []string) bool {
	a := Args(args)
	if len(args) > 0 && args[0] == "--version" {
		return false
	}
	if a.HasFlag("help") {
		return false
	}
	return !IsReadOnly(a.Command())
}
