package fleetconfig_test

import (
	"strings"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/fleetconfig"
)

// fullFleet is the advisor-sketch schema exercised end to end: a defaults block,
// two agents, per-agent argv, and a director seed.
const fullFleet = `
fleet {
    schema-version 2
    defaults {
        agent claude
        attribution name="coilyco-ops" email="coilyco-ops@coilysiren.me"
    }
    agent codex {
        binary codex
        context-level 1
        stream "none"
        auth "codex-file"
        model "gpt-5.4-mini"
        reasoning-effort "low"
        verbosity "low"
        argv { headless codex "exec"; interactive codex }
    }
    agent claude {
        binary claude
        context-level 2
        endpoint "https://api.anthropic.com"
        provider "anthropic"
        argv {
            preflight claude "--version"
            headless claude "-p"
            interactive claude
        }
    }
}
director {
    default-scope "team"
}
`

func TestParseFullFleet(t *testing.T) {
	f, err := fleetconfig.Parse([]byte(fullFleet))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.SchemaVersion != fleetconfig.SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", f.SchemaVersion, fleetconfig.SchemaVersion)
	}
	if f.Defaults.Agent != "claude" {
		t.Errorf("Defaults.Agent = %q, want claude", f.Defaults.Agent)
	}
	if f.Defaults.Attribution.Name != "coilyco-ops" || f.Defaults.Attribution.Email != "coilyco-ops@coilysiren.me" {
		t.Errorf("Defaults.Attribution = %+v", f.Defaults.Attribution)
	}
	if len(f.Agents) != 2 {
		t.Fatalf("len(Agents) = %d, want 2", len(f.Agents))
	}
	codex := f.Agents[0]
	if codex.Name != "codex" || codex.Binary != "codex" {
		t.Errorf("agent[0] name/binary = %q/%q", codex.Name, codex.Binary)
	}
	if codex.ContextLevel != 1 {
		t.Errorf("codex.ContextLevel = %d, want 1", codex.ContextLevel)
	}
	if codex.Stream != "none" || codex.Auth != "codex-file" || codex.Model != "gpt-5.4-mini" {
		t.Errorf("codex knobs = %+v", codex)
	}
	if codex.ReasoningEffort != "low" || codex.Verbosity != "low" {
		t.Errorf("codex effort/verbosity = %q/%q", codex.ReasoningEffort, codex.Verbosity)
	}
	if got := strings.Join(codex.Argv.Headless, " "); got != "codex exec" {
		t.Errorf("codex.Argv.Headless = %q, want `codex exec`", got)
	}
	if got := strings.Join(codex.Argv.Interactive, " "); got != "codex" {
		t.Errorf("codex.Argv.Interactive = %q, want `codex`", got)
	}
	if len(codex.Argv.Preflight) != 0 {
		t.Errorf("codex.Argv.Preflight = %v, want empty", codex.Argv.Preflight)
	}
	claude := f.Agents[1]
	if claude.Endpoint != "https://api.anthropic.com" || claude.Provider != "anthropic" {
		t.Errorf("claude endpoint/provider = %q/%q", claude.Endpoint, claude.Provider)
	}
	if got := strings.Join(claude.Argv.Preflight, " "); got != "claude --version" {
		t.Errorf("claude.Argv.Preflight = %q", got)
	}
	if f.Director == nil || f.Director.DefaultScope != "team" {
		t.Errorf("Director = %+v, want default-scope team", f.Director)
	}
}

// TestContextLevelUnset checks an agent with no context-level lands at -1, not a
// silent 0 that would read as level 0 (minimal context).
func TestContextLevelUnset(t *testing.T) {
	src := `fleet { schema-version 2; agent goose { binary goose } }`
	f, err := fleetconfig.Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Agents[0].ContextLevel != -1 {
		t.Errorf("unset ContextLevel = %d, want -1", f.Agents[0].ContextLevel)
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "permission token mount",
			src:  `fleet { schema-version 2; agent a { binary a; mount "/x" } }`,
			want: "permission token",
		},
		{
			name: "permission token can run",
			src:  `fleet { schema-version 2; agent a { binary a }; can run "git" }`,
			want: "permission token",
		},
		{
			name: "exec token",
			src:  `fleet { schema-version 2; exec "sh"; agent a { binary a } }`,
			want: "permission token",
		},
		{
			name: "unknown top-level node",
			src:  `bogus { }`,
			want: "unknown node",
		},
		{
			name: "unknown agent field",
			src:  `fleet { schema-version 2; agent a { binary a; temperature "0.7" } }`,
			want: "unknown node",
		},
		{
			name: "wrong schema version",
			src:  `fleet { schema-version 99; agent a { binary a } }`,
			want: "not the supported dialect",
		},
		{
			name: "missing schema version",
			src:  `fleet { agent a { binary a } }`,
			want: "missing `schema-version`",
		},
		{
			name: "no agents",
			src:  `fleet { schema-version 2 }`,
			want: "declares no `agent`",
		},
		{
			name: "agent without binary",
			src:  `fleet { schema-version 2; agent a { model "x" } }`,
			want: "missing `binary`",
		},
		{
			name: "duplicate agent",
			src:  `fleet { schema-version 2; agent a { binary a }; agent a { binary b } }`,
			want: "duplicate agent",
		},
		{
			name: "attribution missing email",
			src:  `fleet { schema-version 2; defaults { attribution name="x" }; agent a { binary a } }`,
			want: "needs both name= and email=",
		},
		{
			name: "context-level non-integer",
			src:  `fleet { schema-version 2; agent a { binary a; context-level "high" } }`,
			want: "must be an integer",
		},
		{
			name: "binary non-string",
			src:  `fleet { schema-version 2; agent a { binary 5 } }`,
			want: "must be a string",
		},
		{
			name: "empty argv mode",
			src:  `fleet { schema-version 2; agent a { binary a; argv { headless } } }`,
			want: "needs at least one token",
		},
		{
			name: "duplicate argv mode",
			src:  `fleet { schema-version 2; agent a { binary a; argv { headless a; headless b } } }`,
			want: "duplicate",
		},
		{
			name: "invalid KDL",
			src:  `fleet {`,
			want: "parse KDL",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := fleetconfig.Parse([]byte(tc.src))
			if err == nil {
				t.Fatalf("Parse(%q) = nil error, want %q", tc.src, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Parse error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

// TestSourceSubsets exercises the one-grammar-two-sources rule: `fleet` is
// embed-only, and an operator-local source parses its narrow per-host node set.
func TestSourceSubsets(t *testing.T) {
	t.Run("operator-local accepts director", func(t *testing.T) {
		f, err := fleetconfig.ParseSource([]byte(`director { default-scope "host" }`), fleetconfig.OperatorLocal)
		if err != nil {
			t.Fatalf("ParseSource: %v", err)
		}
		if f.Director == nil || f.Director.DefaultScope != "host" {
			t.Errorf("Director = %+v", f.Director)
		}
		if f.SchemaVersion != 0 || len(f.Agents) != 0 {
			t.Errorf("operator-local carried embed fields: %+v", f)
		}
	})

	t.Run("operator-local rejects fleet", func(t *testing.T) {
		_, err := fleetconfig.ParseSource([]byte(fullFleet), fleetconfig.OperatorLocal)
		if err == nil || !strings.Contains(err.Error(), "embed-only") {
			t.Fatalf("want embed-only rejection, got %v", err)
		}
	})

	t.Run("embedded requires fleet", func(t *testing.T) {
		_, err := fleetconfig.ParseSource([]byte(`director { default-scope "x" }`), fleetconfig.Embedded)
		if err == nil || !strings.Contains(err.Error(), "needs a top-level `fleet`") {
			t.Fatalf("want fleet-required error, got %v", err)
		}
	})

	t.Run("embedded accepts director seed", func(t *testing.T) {
		src := "fleet { schema-version 2; agent a { binary a } }\ndirector { default-scope \"seed\" }"
		f, err := fleetconfig.ParseSource([]byte(src), fleetconfig.Embedded)
		if err != nil {
			t.Fatalf("ParseSource: %v", err)
		}
		if f.Director == nil || f.Director.DefaultScope != "seed" {
			t.Errorf("Director seed = %+v", f.Director)
		}
	})
}

func TestSourceString(t *testing.T) {
	if fleetconfig.Embedded.String() != "embedded" || fleetconfig.OperatorLocal.String() != "operator-local" {
		t.Errorf("Source.String mismatch: %q %q", fleetconfig.Embedded, fleetconfig.OperatorLocal)
	}
}
