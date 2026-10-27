package terminus

import "sync/atomic"

// builtinAliases maps Terminus 4 aliases of state-changing and sensitive
// commands to their canonical names, so safety rules apply to an alias even
// before the catalog (which carries the full alias list) has loaded.
var builtinAliases = map[string]string{
	"deploy":               "env:deploy",
	"env:cc":               "env:clear-cache",
	"env:create":           "multidev:create",
	"env:delete":           "multidev:delete",
	"env:merge-from-dev":   "multidev:merge-from-dev",
	"env:merge-to-dev":     "multidev:merge-to-dev",
	"drush":                "remote:drush",
	"wp":                   "remote:wp",
	"import:db":            "import:database",
	"site:import":          "import:site",
	"site:import:complete": "import:complete",
	"site-create":          "site:create",
	"code-rebuild":         "env:code-rebuild",
	"mt:delete":            "machine-token:delete",
	"mt:delete-all":        "machine-token:delete-all",
}

type resolver func(string) (string, bool)

// aliasResolver, when set by the catalog, resolves any alias. It is set from
// the UI goroutine and read from runner goroutines.
var aliasResolver atomic.Pointer[resolver]

// SetAliasResolver installs a resolver backed by the loaded command catalog.
func SetAliasResolver(f func(string) (string, bool)) {
	r := resolver(f)
	aliasResolver.Store(&r)
}

// Canonical returns the canonical command name for name (which may be an
// alias).
func Canonical(name string) string {
	if r := aliasResolver.Load(); r != nil {
		if c, ok := (*r)(name); ok {
			return c
		}
	}
	if c, ok := builtinAliases[name]; ok {
		return c
	}
	return name
}
