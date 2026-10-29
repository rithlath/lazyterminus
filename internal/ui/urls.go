package ui

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/rithlath/lazyterminus/internal/terminus"
	"github.com/rithlath/lazyterminus/internal/ui/theme"
)

type (
	domainsMsg struct {
		site, env string
		domains   []terminus.Domain
		err       error
	}
	openedMsg struct {
		url string
		err error
	}
)

func loadDomains(c *terminus.Client, site, env string, force bool) tea.Cmd {
	return func() tea.Msg {
		cx, cancel := ctx()
		defer cancel()
		ds, err := c.Domains(cx, site, env, force)
		return domainsMsg{site: site, env: env, domains: ds, err: err}
	}
}

// ensureDomains loads the selected environment's domains (for custom
// domains; the platform domain is already in env:list).
func (m *Model) ensureDomains() tea.Cmd {
	site, env := m.site(), m.selectedEnv()
	st := m.state(site)
	if st.env(env) == nil {
		return nil
	}
	if _, ok := st.domains[env]; ok || !st.begin("domains:"+env) {
		return nil
	}
	return loadDomains(m.client, site, env, false)
}

func (m *Model) onDomains(msg domainsMsg) {
	st := m.state(msg.site)
	delete(st.inflight, "domains:"+msg.env)
	if msg.err != nil {
		// Not worth an error state: the platform URL still works.
		m.logOutput(theme.Fail.Render(theme.SymFailed + " domain:list " + msg.site + "." + msg.env + ": " + msg.err.Error()))
		st.domains[msg.env] = nil
		return
	}
	st.domains[msg.env] = msg.domains
}

// envURLs is what can be opened for env: the address the open key uses
// first, then the rest (platform domain and other custom domains).
func (m *Model) envURLs(site, env string) []string {
	st := m.state(site)
	e := st.env(env)
	if e == nil {
		return nil
	}
	ds := st.domains[env]
	urls := []string{terminus.PrimaryURL(*e, ds)}
	for _, u := range append([]string{terminus.URL(e.Domain)}, customURLs(ds)...) {
		if u != "" && u != urls[0] {
			urls = append(urls, u)
		}
	}
	if urls[0] == "" {
		urls = urls[1:]
	}
	return urls
}

func customURLs(ds []terminus.Domain) []string {
	var out []string
	for _, d := range terminus.CustomDomains(ds) {
		out = append(out, terminus.URL(d.ID))
	}
	return out
}

// openEnvURL opens the selected environment in the browser.
func (m *Model) openEnvURL() tea.Cmd {
	site, e, ok := m.target()
	if !ok {
		return nil
	}
	urls := m.envURLs(site, e.ID)
	if len(urls) == 0 {
		m.setStatus(e.ID+" has no domain yet.", true)
		return nil
	}
	return m.openURL(urls[0])
}

func (m *Model) openURL(url string) tea.Cmd {
	open := m.opts.OpenURL
	if open == nil {
		open = openBrowser
	}
	m.setStatus("Opening "+url+"…", false)
	return func() tea.Msg { return openedMsg{url: url, err: open(url)} }
}

func (m *Model) onOpened(msg openedMsg) {
	if msg.err != nil {
		m.reportErr("open "+msg.url, msg.err)
		return
	}
	m.setStatus("Opened "+msg.url+" in your browser.", false)
}

// openBrowser hands url to the system: $BROWSER if set, wslview or
// explorer.exe under WSL, open on macOS, xdg-open elsewhere. It doesn't
// wait for the browser.
func openBrowser(url string) error {
	var name string
	var args []string
	switch {
	case os.Getenv("BROWSER") != "":
		f := strings.Fields(strings.Split(os.Getenv("BROWSER"), ":")[0])
		name, args = f[0], f[1:]
		if i := indexContaining(args, "%s"); i >= 0 {
			args[i] = strings.ReplaceAll(args[i], "%s", url)
		} else {
			args = append(args, url)
		}
	case runtime.GOOS == "darwin":
		name, args = "open", []string{url}
	case runtime.GOOS == "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	case os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "":
		name, args = "explorer.exe", []string{url}
		if _, err := exec.LookPath("wslview"); err == nil {
			name = "wslview"
		}
	default:
		name, args = "xdg-open", []string{url}
	}
	c := exec.Command(name, args...)
	if err := c.Start(); err != nil {
		return err
	}
	go c.Wait() //nolint:errcheck // explorer.exe exits 1 even on success
	return nil
}

func indexContaining(ss []string, sub string) int {
	for i, s := range ss {
		if strings.Contains(s, sub) {
			return i
		}
	}
	return -1
}

// urlSpan is where a URL landed on a rendered line.
type urlSpan struct {
	x, w int
	url  string
}

// urlLine is the pipeline footer's first line: the selected environment's
// addresses, each clickable, and the open key.
func (m *Model) urlLine(width int) (string, []urlSpan) {
	urls := m.envURLs(m.site(), m.selectedEnv())
	if len(urls) == 0 {
		return "", nil
	}
	label := "Open "
	line, x := theme.MutedText.Render(label), ansi.StringWidth(label)
	var spans []urlSpan
	sep := theme.MutedText.Render(" · ")
	for i, u := range urls {
		start, w := x, ansi.StringWidth(u)
		if i > 0 {
			start += ansi.StringWidth(" · ")
		}
		if start+w > width {
			break
		}
		if i > 0 {
			line += sep + u
		} else {
			line += theme.Key.Render(u)
		}
		spans = append(spans, urlSpan{x: start, w: w, url: u})
		x = start + w
	}
	if k := m.keys.Key(ActOpenURL); k != "" && k != "—" {
		line += theme.MutedText.Render("   " + k + " or click to open")
	}
	return line, spans
}
