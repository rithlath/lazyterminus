package main

import "testing"

func TestResolveStart(t *testing.T) {
	cases := []struct {
		site, env, cwd    string
		wantSite, wantEnv string
		wantErr           bool
	}{
		{"", "", "", "", "", false},
		{"", "", "acme-web", "acme-web", "", false},
		{"", "acme-web.test", "", "acme-web", "acme-web.test", false},
		{"", "acme-intranet.live", "acme-web", "acme-intranet", "acme-intranet.live", false},
		{"", "feedback", "acme-web", "acme-web", "acme-web.feedback", false},
		{"acme-intranet", "dev", "acme-web", "acme-intranet", "acme-intranet.dev", false},
		{"", "master", "acme-web", "acme-web", "acme-web.dev", false},
		{"", "acme-web.main", "", "acme-web", "acme-web.dev", false},
		{"", "feedback", "", "", "", true},
		{"acme-intranet", "acme-web.test", "", "acme-web", "acme-web.test", false},
	}
	for _, c := range cases {
		s, e, err := resolveStart(c.site, c.env, c.cwd)
		if (err != nil) != c.wantErr || s != c.wantSite || e != c.wantEnv {
			t.Errorf("resolveStart(%q,%q,%q) = %q,%q,%v", c.site, c.env, c.cwd, s, e, err)
		}
	}
}
