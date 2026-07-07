package opcore_test

import (
	"net/http"
	"reflect"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/opcore"
)

// inlineSrc is a full ward-mcp inline source exercising the frozen grammar: wrap
// header, base-url, auth, restrict, and three ops (create, a `set` toggle, delete).
const inlineSrc = `wrap ward mcp forgejo {
    base-url "forgejo.coilysiren.me/api/v1"
    auth header-token {
        header "Authorization"
        prefix "token "
        value env "FORGEJO_TOKEN"
    }
    restrict owner matches "coilyco-*" "kai"

    can create issue {
        path "/repos/{owner}/{repo}/issues"
        query "state"
        body "title" "body"
    }
    can close issue {
        path "/repos/{owner}/{repo}/issues/{index}"
        set state="closed"
    }
    can delete repo {
        path "/repos/{owner}/{repo}"
    }
}`

func parseInline(t *testing.T, src string) ([]opcore.Descriptor, opcore.RuntimeConfig) {
	t.Helper()
	descs, cfg, err := opcore.ParseInline([]byte(src))
	if err != nil {
		t.Fatalf("ParseInline: %v", err)
	}
	return descs, cfg
}

// descByLeaf finds the stated descriptor for a leaf verb, failing if absent.
func descByLeaf(t *testing.T, descs []opcore.Descriptor, leaf string) opcore.Descriptor {
	t.Helper()
	for _, d := range descs {
		if d.Leaf == leaf {
			return d
		}
	}
	t.Fatalf("no descriptor with leaf %q", leaf)
	return opcore.Descriptor{}
}

func TestParseInlineMethodFromVerb(t *testing.T) {
	descs, _ := parseInline(t, inlineSrc)
	cases := map[string]string{
		"create": http.MethodPost,
		"close":  http.MethodPatch,
		"delete": http.MethodDelete,
	}
	for leaf, want := range cases {
		if got := descByLeaf(t, descs, leaf).Method; got != want {
			t.Errorf("leaf %q method = %q, want %q", leaf, got, want)
		}
	}
}

func TestParseInlinePathParamsFromTemplate(t *testing.T) {
	descs, _ := parseInline(t, inlineSrc)
	create := descByLeaf(t, descs, "create")
	if want := []string{"owner", "repo"}; !reflect.DeepEqual(create.PathParams, want) {
		t.Errorf("create path params = %v, want %v", create.PathParams, want)
	}
	closeOp := descByLeaf(t, descs, "close")
	if want := []string{"owner", "repo", "index"}; !reflect.DeepEqual(closeOp.PathParams, want) {
		t.Errorf("close path params = %v, want %v", closeOp.PathParams, want)
	}
}

func TestParseInlineBodyAndQueryFields(t *testing.T) {
	descs, _ := parseInline(t, inlineSrc)
	create := descByLeaf(t, descs, "create")
	wantQuery := []opcore.Field{{Name: "state", Type: "string"}}
	if !reflect.DeepEqual(create.QueryFlags, wantQuery) {
		t.Errorf("create query = %+v, want %+v", create.QueryFlags, wantQuery)
	}
	wantBody := []opcore.Field{{Name: "title", Type: "string"}, {Name: "body", Type: "string"}}
	if !reflect.DeepEqual(create.BodyFlags, wantBody) {
		t.Errorf("create body = %+v, want %+v", create.BodyFlags, wantBody)
	}
}

func TestParseInlineSetToFixedBody(t *testing.T) {
	descs, _ := parseInline(t, inlineSrc)
	closeOp := descByLeaf(t, descs, "close")
	if want := map[string]any{"state": "closed"}; !reflect.DeepEqual(closeOp.FixedBody, want) {
		t.Errorf("close fixed body = %v, want %v", closeOp.FixedBody, want)
	}
	// A `set` toggle owns its body: no body flags mount alongside it.
	if closeOp.BodyFlags != nil {
		t.Errorf("close body flags = %v, want nil (the set toggle owns the body)", closeOp.BodyFlags)
	}
}

func TestParseInlineSetKeepsKDLTypes(t *testing.T) {
	descs, _ := parseInline(t, `wrap x {
        auth bearer { value env "T" }
        can archive repo { path "/repos/{owner}/{repo}"; set archived=#true }
    }`)
	got := descByLeaf(t, descs, "archive").FixedBody
	if want := map[string]any{"archived": true}; !reflect.DeepEqual(got, want) {
		t.Errorf("fixed body = %v (types: %T), want %v with a bool", got, got["archived"], want)
	}
}

func TestParseInlineDestructiveDelete(t *testing.T) {
	descs, _ := parseInline(t, inlineSrc)
	if !descByLeaf(t, descs, "delete").Destructive {
		t.Error("delete should be flagged destructive")
	}
	if descByLeaf(t, descs, "create").Destructive {
		t.Error("create should not be destructive")
	}
}

func TestParseInlineRuntimeConfig(t *testing.T) {
	_, cfg := parseInline(t, inlineSrc)
	if cfg.BaseURL != "forgejo.coilysiren.me/api/v1" {
		t.Errorf("base-url = %q", cfg.BaseURL)
	}
	if cfg.Auth.Scheme != "header-token" || cfg.Auth.Header != "Authorization" || cfg.Auth.Prefix != "token " {
		t.Errorf("auth = %+v", cfg.Auth)
	}
	if len(cfg.Auth.Value) != 1 || cfg.Auth.Value[0].Provider != "env" || cfg.Auth.Value[0].Address != "FORGEJO_TOKEN" {
		t.Errorf("auth value chain = %+v", cfg.Auth.Value)
	}
	if len(cfg.Restrict) != 1 || cfg.Restrict[0].Param != "owner" {
		t.Fatalf("restrict = %+v", cfg.Restrict)
	}
	if want := []string{"coilyco-*", "kai"}; !reflect.DeepEqual(cfg.Restrict[0].Globs, want) {
		t.Errorf("restrict globs = %v, want %v", cfg.Restrict[0].Globs, want)
	}
	// Providers and Client are the consumer's to fill, never stated by the KDL.
	if cfg.Providers != nil || cfg.Client != nil {
		t.Errorf("Providers/Client should be nil until the consumer fills them")
	}
}

func TestParseInlineBaseURLValueBlock(t *testing.T) {
	_, cfg := parseInline(t, `wrap x {
        base-url { value env "FORGEJO_HOST" }
        auth bearer { value env "T" }
        can get repo { path "/repos/{owner}/{repo}" }
    }`)
	if cfg.BaseURL != "" {
		t.Errorf("static base-url should be empty for the block form, got %q", cfg.BaseURL)
	}
	if cfg.BaseURLValue.IsZero() || cfg.BaseURLValue[0].Provider != "env" {
		t.Errorf("base-url value chain = %+v", cfg.BaseURLValue)
	}
}

func TestParseInlineReservedFlagCollisionFailsClosed(t *testing.T) {
	_, _, err := opcore.ParseInline([]byte(`wrap x {
        auth bearer { value env "T" }
        can list issue { path "/issues"; query "output" }
    }`))
	if err == nil {
		t.Fatal("a query field named `output` shadows a reserved engine flag; want a fail-closed error")
	}
}

func TestParseInlineDuplicateFieldFailsClosed(t *testing.T) {
	_, _, err := opcore.ParseInline([]byte(`wrap x {
        auth bearer { value env "T" }
        can create issue { path "/issues"; query "state"; body "state" }
    }`))
	if err == nil {
		t.Fatal("a query and body field both named `state` collide; want a fail-closed error")
	}
}

func TestParseInlineFailClosedCases(t *testing.T) {
	cases := map[string]string{
		"no wrap": `spec "x"`,
		"empty wrap path": `wrap {
            auth bearer { value env "T" }
        }`,
		"unknown wrap node": `wrap x {
            auth bearer { value env "T" }
            spec "y"
            can get repo { path "/r" }
        }`,
		"missing auth": `wrap x {
            can get repo { path "/repos/{owner}" }
        }`,
		"no ops": `wrap x {
            auth bearer { value env "T" }
        }`,
		"missing path": `wrap x {
            auth bearer { value env "T" }
            can get repo { query "state" }
        }`,
		"unknown grant child": `wrap x {
            auth bearer { value env "T" }
            can get repo { path "/r"; describe "no" }
        }`,
		"can wrong arity": `wrap x {
            auth bearer { value env "T" }
            can get { path "/r" }
        }`,
		"empty set": `wrap x {
            auth bearer { value env "T" }
            can close issue { path "/r"; set }
        }`,
		"empty query field list": `wrap x {
            auth bearer { value env "T" }
            can get repo { path "/r"; query }
        }`,
		"base-url both forms": `wrap x {
            auth bearer { value env "T" }
            base-url "a"
            base-url { value env "H" }
            can get repo { path "/r" }
        }`,
	}
	for name, src := range cases {
		if _, _, err := opcore.ParseInline([]byte(src)); err == nil {
			t.Errorf("%s: expected a fail-closed error, got nil", name)
		}
	}
}
