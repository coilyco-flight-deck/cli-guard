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

// TestParseRoles exercises the per-role capability roster: a flat
// list, a prefix, an empty-set role, and that the parsed shape round-trips.
func TestParseRoles(t *testing.T) {
	src := `
fleet {
    schema-version 2
    agent claude { binary claude }
    roles {
        role engineer { }
        role director {
            guardfiles "ward-kdl.tailscale.guardfile.kdl"
        }
        role advisor {
            guardfiles "ward-kdl.aws.guardfile.kdl" "ward-kdl.tailscale.guardfile.kdl"
        }
        role observer {
            guardfiles prefix="ward-kdl.observe"
        }
    }
}
`
	f, err := fleetconfig.Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(f.Roles) != 4 {
		t.Fatalf("len(Roles) = %d, want 4", len(f.Roles))
	}
	byName := map[string]fleetconfig.Role{}
	for _, r := range f.Roles {
		byName[r.Name] = r
	}
	if g := byName["engineer"].Guardfiles; len(g.List) != 0 || g.Prefix != "" {
		t.Errorf("engineer holds an unexpected set: %+v", g)
	}
	if g := byName["director"].Guardfiles; len(g.List) != 1 || g.List[0] != "ward-kdl.tailscale.guardfile.kdl" {
		t.Errorf("director list = %+v", g)
	}
	if g := byName["advisor"].Guardfiles; len(g.List) != 2 {
		t.Errorf("advisor list = %+v (want 2)", g)
	}
	if g := byName["observer"].Guardfiles; g.Prefix != "ward-kdl.observe" || len(g.List) != 0 {
		t.Errorf("observer guardfiles = %+v", g)
	}
}

// TestParseRoleAgentOverrides exercises the per-agent override overlay
// guardfiles-only, agent-only, and a role carrying both.
func TestParseRoleAgentOverrides(t *testing.T) {
	src := `
fleet {
    schema-version 2
    agent claude { binary claude }
    roles {
        role plain {
            guardfiles "ward-kdl.aws.guardfile.kdl"
        }
        role engineer {
            agent claude {
                model "claude-opus-4-8"
                reasoning-effort "high"
            }
        }
        role advisor {
            guardfiles "ward-kdl.tailscale.guardfile.kdl"
            agent claude {
                model "claude-sonnet-5"
                reasoning-effort "low"
            }
            agent codex {
                endpoint "https://api.example.com"
                verbosity "low"
            }
        }
    }
}
`
	f, err := fleetconfig.Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	byName := map[string]fleetconfig.Role{}
	for _, r := range f.Roles {
		byName[r.Name] = r
	}
	if r := byName["plain"]; r.AgentConfig != nil {
		t.Errorf("plain carried an overlay: %+v", r.AgentConfig)
	}
	eng := byName["engineer"].AgentConfig
	if len(eng) != 1 {
		t.Fatalf("engineer overlay size = %d, want 1", len(eng))
	}
	if eng["claude"].Model != "claude-opus-4-8" || eng["claude"].ReasoningEffort != "high" {
		t.Errorf("engineer claude override = %+v", eng["claude"])
	}
	if eng["claude"].Endpoint != "" || eng["claude"].Verbosity != "" {
		t.Errorf("engineer claude override leaked unset knobs: %+v", eng["claude"])
	}
	adv := byName["advisor"]
	if len(adv.Guardfiles.List) != 1 || adv.Guardfiles.List[0] != "ward-kdl.tailscale.guardfile.kdl" {
		t.Errorf("advisor guardfiles = %+v", adv.Guardfiles)
	}
	if len(adv.AgentConfig) != 2 {
		t.Fatalf("advisor overlay size = %d, want 2", len(adv.AgentConfig))
	}
	if adv.AgentConfig["claude"].Model != "claude-sonnet-5" || adv.AgentConfig["claude"].ReasoningEffort != "low" {
		t.Errorf("advisor claude override = %+v", adv.AgentConfig["claude"])
	}
	if adv.AgentConfig["codex"].Endpoint != "https://api.example.com" || adv.AgentConfig["codex"].Verbosity != "low" {
		t.Errorf("advisor codex override = %+v", adv.AgentConfig["codex"])
	}
}

// TestParseDescription checks the first-class top-level `description` node: it
// parses into Fleet.Description, is optional, and is accepted in both sources.
func TestParseDescription(t *testing.T) {
	t.Run("embedded carries description", func(t *testing.T) {
		src := `
description "Fleet config for ward: the agent roster and per-agent launch shape."
fleet { schema-version 2; agent a { binary a } }
`
		f, err := fleetconfig.Parse([]byte(src))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if f.Description != "Fleet config for ward: the agent roster and per-agent launch shape." {
			t.Errorf("Description = %q", f.Description)
		}
	})

	t.Run("absent description is empty, not an error", func(t *testing.T) {
		f, err := fleetconfig.Parse([]byte(`fleet { schema-version 2; agent a { binary a } }`))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if f.Description != "" {
			t.Errorf("Description = %q, want empty", f.Description)
		}
	})

	t.Run("operator-local carries description", func(t *testing.T) {
		src := `description "Per-host director settings."` + "\n" + `director { default-scope "host" }`
		f, err := fleetconfig.ParseSource([]byte(src), fleetconfig.OperatorLocal)
		if err != nil {
			t.Fatalf("ParseSource: %v", err)
		}
		if f.Description != "Per-host director settings." {
			t.Errorf("Description = %q", f.Description)
		}
	})

	t.Run("multi-paragraph description via escaped newlines", func(t *testing.T) {
		src := `description "line one\n\nline two"` + "\n" + `fleet { schema-version 2; agent a { binary a } }`
		f, err := fleetconfig.Parse([]byte(src))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if !strings.Contains(f.Description, "line one") || !strings.Contains(f.Description, "line two") {
			t.Errorf("multi-paragraph Description = %q", f.Description)
		}
	})
}

func TestParseSparseAgentData(t *testing.T) {
	src := `fleet { schema-version 2; agent claude { model "bundle-override"; context-level 1 } }`
	f, err := fleetconfig.Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(f.Agents) != 1 {
		t.Fatalf("len(Agents) = %d, want 1", len(f.Agents))
	}
	a := f.Agents[0]
	if a.Name != "claude" {
		t.Errorf("Name = %q, want claude", a.Name)
	}
	if a.Binary != "" {
		t.Errorf("Binary = %q, want empty sparse data", a.Binary)
	}
	if a.ContextLevel != 1 {
		t.Errorf("ContextLevel = %d, want 1", a.ContextLevel)
	}
	if a.Model != "bundle-override" {
		t.Errorf("Model = %q, want bundle-override", a.Model)
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
			name: "roles guardfiles list and prefix",
			src:  `fleet { schema-version 2; agent a { binary a }; roles { role r { guardfiles "x" prefix="y" } } }`,
			want: "flat list OR a prefix",
		},
		{
			name: "roles guardfiles empty node",
			src:  `fleet { schema-version 2; agent a { binary a }; roles { role r { guardfiles } } }`,
			want: "needs a flat list of names or a prefix",
		},
		{
			name: "roles guardfiles unknown property",
			src:  `fleet { schema-version 2; agent a { binary a }; roles { role r { guardfiles suffix="z" } } }`,
			want: "unknown property",
		},
		{
			name: "roles unknown child",
			src:  `fleet { schema-version 2; agent a { binary a }; roles { role r { bogus "x" } } }`,
			want: "unknown node",
		},
		{
			name: "roles non-role child",
			src:  `fleet { schema-version 2; agent a { binary a }; roles { bogus { } } }`,
			want: "unknown node",
		},
		{
			name: "duplicate role",
			src:  `fleet { schema-version 2; agent a { binary a }; roles { role r { }; role r { } } }`,
			want: "duplicate role",
		},
		{
			name: "empty roles block",
			src:  `fleet { schema-version 2; agent a { binary a }; roles { } }`,
			want: "declares no `role`",
		},
		{
			name: "duplicate roles block",
			src:  `fleet { schema-version 2; agent a { binary a }; roles { role r { } }; roles { role s { } } }`,
			want: "duplicate `roles`",
		},
		{
			name: "permission token in roles",
			src:  `fleet { schema-version 2; agent a { binary a }; roles { role r { mount "/x" } } }`,
			want: "permission token",
		},
		{
			name: "duplicate role agent override",
			src:  `fleet { schema-version 2; agent a { binary a }; roles { role r { agent a { model "x" }; agent a { model "y" } } } }`,
			want: "duplicate `agent a` override",
		},
		{
			name: "role agent unknown property",
			src:  `fleet { schema-version 2; agent a { binary a }; roles { role r { agent a { temperature "0.7" } } } }`,
			want: "unknown node",
		},
		{
			name: "role agent structural knob rejected",
			src:  `fleet { schema-version 2; agent a { binary a }; roles { role r { agent a { binary b } } } }`,
			want: "unknown node",
		},
		{
			name: "role agent missing name",
			src:  `fleet { schema-version 2; agent a { binary a }; roles { role r { agent { model "x" } } } }`,
			want: "needs a single name",
		},
		{
			name: "role agent non-string knob",
			src:  `fleet { schema-version 2; agent a { binary a }; roles { role r { agent a { model 5 } } } }`,
			want: "must be a string",
		},
		{
			name: "permission token in role agent",
			src:  `fleet { schema-version 2; agent a { binary a }; roles { role r { agent a { exec "sh" } } } }`,
			want: "permission token",
		},
		{
			name: "duplicate description",
			src:  `description "a"; description "b"; fleet { schema-version 2; agent a { binary a } }`,
			want: "duplicate top-level `description`",
		},
		{
			name: "empty description",
			src:  `description ""; fleet { schema-version 2; agent a { binary a } }`,
			want: "must be a non-empty string",
		},
		{
			name: "description non-string",
			src:  `description 5; fleet { schema-version 2; agent a { binary a } }`,
			want: "must be a string",
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
