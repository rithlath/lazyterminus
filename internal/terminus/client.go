package terminus

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/rithlath/lazyterminus/internal/cache"
)

// Cache TTLs for read results.
const (
	TTLEnvData       = 30 * time.Second
	TTLWorkflowsBusy = 5 * time.Second
	TTLDefault       = 60 * time.Second
	TTLFinishedOps   = 10 * time.Minute
)

// Client issues typed read commands through a Runner and caches results.
// All methods are safe to call from tea.Cmd goroutines.
type Client struct {
	R     Runner
	Cache *cache.Cache
}

// NewClient returns a client over r with an empty cache.
func NewClient(r Runner) *Client { return &Client{R: r, Cache: cache.New()} }

func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	out, _, err := c.R.Run(ctx, args...)
	return out, err
}

func key(parts ...string) string { return strings.Join(parts, "|") + "|" }

// InvalidateSite drops every cached result for site.
func (c *Client) InvalidateSite(site string) {
	for _, kind := range []string{"envs", "codelog", "diffstat", "workflows", "upstream"} {
		c.Cache.DeletePrefix(key(kind, site))
	}
}

// InvalidateEnv drops cached per-environment results for site.env.
func (c *Client) InvalidateEnv(site, env string) {
	c.Cache.Delete(key("codelog", site, env))
	c.Cache.Delete(key("diffstat", site, env))
}

// Sites lists sites (site:list).
func (c *Client) Sites(ctx context.Context, force bool) ([]Site, error) {
	return cache.Typed(c.Cache, key("sites"), TTLDefault, force, func() ([]Site, error) {
		out, err := c.run(ctx, "site:list", "--format=json")
		if err != nil {
			return nil, err
		}
		return ParseSites(out)
	})
}

// Envs lists a site's environments including multidevs (env:list).
func (c *Client) Envs(ctx context.Context, site string, force bool) ([]Env, error) {
	return cache.Typed(c.Cache, key("envs", site), TTLEnvData, force, func() ([]Env, error) {
		out, err := c.run(ctx, "env:list", site, "--format=json", "--fields=*")
		if err != nil {
			return nil, err
		}
		return ParseEnvs(out)
	})
}

// CodeLog returns an environment's recent commits (env:code-log).
func (c *Client) CodeLog(ctx context.Context, site, env string, force bool) ([]Commit, error) {
	return cache.Typed(c.Cache, key("codelog", site, env), TTLEnvData, force, func() ([]Commit, error) {
		out, err := c.run(ctx, "env:code-log", SiteEnv(site, env), "--format=json")
		if err != nil {
			return nil, err
		}
		return ParseCodeLog(out)
	})
}

// Diffstat returns uncommitted SFTP changes (env:diffstat).
func (c *Client) Diffstat(ctx context.Context, site, env string, force bool) ([]DiffstatEntry, error) {
	return cache.Typed(c.Cache, key("diffstat", site, env), TTLEnvData, force, func() ([]DiffstatEntry, error) {
		out, err := c.run(ctx, "env:diffstat", SiteEnv(site, env), "--format=json")
		if err != nil {
			return nil, err
		}
		return ParseDiffstat(out)
	})
}

// Workflows lists a site's recent workflows (workflow:list). busy selects
// the short TTL used while a job or workflow is running.
func (c *Client) Workflows(ctx context.Context, site string, busy, force bool) ([]Workflow, error) {
	ttl := TTLDefault
	if busy {
		ttl = TTLWorkflowsBusy
	}
	return cache.Typed(c.Cache, key("workflows", site), ttl, force, func() ([]Workflow, error) {
		out, err := c.run(ctx, "workflow:list", site, "--format=json")
		if err != nil {
			return nil, err
		}
		return ParseWorkflows(out)
	})
}

// WorkflowOps returns a workflow's operations (workflow:info:operations).
// Results for finished workflows are cached long; running ones never are.
func (c *Client) WorkflowOps(ctx context.Context, site, id string, finished bool) ([]WorkflowOp, error) {
	ttl := time.Duration(0)
	if finished {
		ttl = TTLFinishedOps
	}
	return cache.Typed(c.Cache, key("ops", site, id), ttl, false, func() ([]WorkflowOp, error) {
		out, err := c.run(ctx, "workflow:info:operations", site, "--id="+id, "--format=json")
		if err != nil {
			return nil, err
		}
		return ParseWorkflowOps(out)
	})
}

// UpstreamUpdates lists pending upstream commits for a development env
// (upstream:updates:list).
func (c *Client) UpstreamUpdates(ctx context.Context, site, env string, force bool) ([]UpstreamUpdate, error) {
	return cache.Typed(c.Cache, key("upstream", site, env), TTLDefault, force, func() ([]UpstreamUpdate, error) {
		out, err := c.run(ctx, "upstream:updates:list", SiteEnv(site, env), "--format=json")
		if err != nil {
			return nil, err
		}
		return ParseUpstreamUpdates(out)
	})
}

// Version returns the Terminus version string (e.g. "4.2.0").
func (c *Client) Version(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "--version")
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(out))
	v = strings.TrimPrefix(v, "Terminus ")
	return v, nil
}

// ErrNotLoggedIn means Terminus has no authenticated session.
var ErrNotLoggedIn = errors.New("not logged in to Terminus")

// IsNotLoggedIn reports whether err is Terminus refusing for lack of a
// session ("You are not logged in. Run `auth:login` ...").
func IsNotLoggedIn(err error) bool {
	return err != nil && (errors.Is(err, ErrNotLoggedIn) || strings.Contains(strings.ToLower(err.Error()), "not logged in"))
}

// WhoAmI returns the logged-in account's email. Terminus prints nothing on
// stdout (only a notice on stderr) when there is no session, which is
// reported as ErrNotLoggedIn.
func (c *Client) WhoAmI(ctx context.Context) (string, error) {
	out, _, err := c.R.Run(ctx, "auth:whoami")
	if err != nil {
		if IsNotLoggedIn(err) {
			return "", ErrNotLoggedIn
		}
		return "", err
	}
	for l := range strings.SplitSeq(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "[") {
			return l, nil
		}
	}
	return "", ErrNotLoggedIn
}
