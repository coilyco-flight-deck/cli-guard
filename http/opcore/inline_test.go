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

const nestedInlineSrc = `wrap ward mcp forgejo {
    auth bearer {
        value env "FORGEJO_TOKEN"
    }
    can query issue {
        path "/query"
        body {
            field "start" type="integer" required=true
            field "requestType" type="string" required=true
            object "variables" raw=true
            field "labels" type="array" items="string"
            object "compositeQuery" required=true {
                field "start" type="integer" required=true
                field "end" type="integer" required=true
            }
        }
    }
}`

const proxyInlineSrc = `wrap ward mcp grubhub {
    auth bearer {
        value env "GRUBHUB_TOKEN"
    }
    proxy browser_snapshot {
        upstream playwright browser_snapshot
        allow url matches "^https://www\\.grubhub\\.com/"
        deny text matches "forbidden"
        post-call content matches "grubhub\\.com"
        post-call state matches "forbidden"
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

func TestParseInlineQueryAlias(t *testing.T) {
	descs, _ := parseInline(t, `wrap x {
        auth bearer { value env "T" }
        can search card {
            path "/search"
            query "search_query" upstream="query"
        }
    }`)
	search := descByLeaf(t, descs, "search")
	want := []opcore.Field{{Name: "search_query", UpstreamName: "query", Type: "string"}}
	if !reflect.DeepEqual(search.QueryFlags, want) {
		t.Errorf("search query flags = %+v, want %+v", search.QueryFlags, want)
	}
}

func TestParseInlineTypedQueryBlock(t *testing.T) {
	descs, _ := parseInline(t, `wrap x {
        auth bearer { value env "T" }
        can list message {
            path "/channels/{channel_id}/messages"
            query {
                field "limit" type="integer" minimum=1 maximum=100 required=true
                field "pinned" type="boolean"
                field "score" type="number" minimum=0.5 maximum=9.5
                array "author_id" items="string" min-items=1 max-items=25
                array "enabled" items="boolean"
                array "page" items="integer"
                array "weight" items="number"
                field "search_query" type="string" upstream="query"
                field "before" type="string"
                field "after" type="string"
                field "around" type="string"
                mutually-exclusive "before" "after" "around"
            }
        }
    }`)
	list := descByLeaf(t, descs, "list")
	minLimit, maxLimit := float64(1), float64(100)
	minScore, maxScore := 0.5, 9.5
	minAuthors, maxAuthors := 1, 25
	want := []opcore.Field{
		{Name: "limit", Type: "integer", Required: true, Minimum: &minLimit, Maximum: &maxLimit},
		{Name: "pinned", Type: "boolean"},
		{Name: "score", Type: "number", Minimum: &minScore, Maximum: &maxScore},
		{Name: "author_id", Type: "array", Items: "string", MinItems: &minAuthors, MaxItems: &maxAuthors},
		{Name: "enabled", Type: "array", Items: "boolean"},
		{Name: "page", Type: "array", Items: "integer"},
		{Name: "weight", Type: "array", Items: "number"},
		{Name: "search_query", UpstreamName: "query", Type: "string"},
		{Name: "before", Type: "string"},
		{Name: "after", Type: "string"},
		{Name: "around", Type: "string"},
	}
	if !reflect.DeepEqual(list.QueryFlags, want) {
		t.Errorf("typed query fields = %#v, want %#v", list.QueryFlags, want)
	}
	if wantExclusive := [][]string{{"before", "after", "around"}}; !reflect.DeepEqual(list.QueryExclusive, wantExclusive) {
		t.Errorf("query exclusions = %v, want %v", list.QueryExclusive, wantExclusive)
	}
}

func TestParseInlineNestedBodySchema(t *testing.T) {
	descs, _ := parseInline(t, nestedInlineSrc)
	query := descByLeaf(t, descs, "query")
	wantBody := []opcore.Field{
		{Name: "start", Type: "integer", Required: true},
		{Name: "requestType", Type: "string", Required: true},
		{Name: "variables", Type: "object", Raw: true},
		{Name: "labels", Type: "array", Items: "string"},
		{
			Name:     "compositeQuery",
			Type:     "object",
			Required: true,
			Fields: []opcore.Field{
				{Name: "start", Type: "integer", Required: true},
				{Name: "end", Type: "integer", Required: true},
			},
		},
	}
	if !reflect.DeepEqual(query.BodyFlags, wantBody) {
		t.Errorf("query body = %+v, want %+v", query.BodyFlags, wantBody)
	}
}

func TestParseInlineProxyGrant(t *testing.T) {
	descs, _ := parseInline(t, proxyInlineSrc)
	if len(descs) != 1 {
		t.Fatalf("proxy descriptors = %d, want 1", len(descs))
	}
	got := descs[0]
	if got.Proxy == nil {
		t.Fatal("proxy descriptor missing Proxy payload")
	}
	if got.Leaf != "browser_snapshot" || got.Group != "mcp" {
		t.Fatalf("proxy identity = %+v, want leaf browser_snapshot and group mcp", got)
	}
	if got.Proxy.Upstream.Server != "playwright" || got.Proxy.Upstream.Tool != "browser_snapshot" {
		t.Fatalf("proxy upstream = %+v, want playwright/browser_snapshot", got.Proxy.Upstream)
	}
	wantAllow := []opcore.ProxyRule{{Field: "url", Patterns: []string{"^https://www\\.grubhub\\.com/"}}}
	wantDeny := []opcore.ProxyRule{{Field: "text", Patterns: []string{"forbidden"}}}
	if !reflect.DeepEqual(got.Proxy.Allow, wantAllow) || !reflect.DeepEqual(got.Proxy.Deny, wantDeny) {
		t.Fatalf("proxy request guards = allow %+v deny %+v, want allow %+v deny %+v", got.Proxy.Allow, got.Proxy.Deny, wantAllow, wantDeny)
	}
	wantPostCall := []opcore.ProxyRule{{Field: "content", Patterns: []string{"grubhub\\.com"}}, {Field: "state", Patterns: []string{"forbidden"}}}
	if !reflect.DeepEqual(got.Proxy.PostCall, wantPostCall) {
		t.Fatalf("proxy post-call guards = %+v, want %+v", got.Proxy.PostCall, wantPostCall)
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

func TestParseInlineBodyBlockRejectsMixingShorthandAndBlock(t *testing.T) {
	_, _, err := opcore.ParseInline([]byte(`wrap x {
        auth bearer { value env "T" }
        can create issue { path "/issues"; body "title" { field "nested" type="string" } }
    }`))
	if err == nil {
		t.Fatal("mixing flat body shorthand and a body block should fail closed")
	}
}

func TestParseInlineRawBodyFieldRequiresObjectOrArray(t *testing.T) {
	_, _, err := opcore.ParseInline([]byte(`wrap x {
        auth bearer { value env "T" }
        can create issue { path "/issues"; body { field "title" type="string" raw=true } }
    }`))
	if err == nil {
		t.Fatal("raw=true on a scalar field should fail closed")
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

func TestParseInlineQueryAliasFailsClosed(t *testing.T) {
	cases := map[string]string{
		"reserved local name":                      `query "query" upstream="q"`,
		"multiple local names":                     `query "search_query" "filter" upstream="query"`,
		"same local and upstream name":             `query "search_query" upstream="search_query"`,
		"duplicate upstream mapping":               `query "search_query" upstream="query"; query "advanced_query" upstream="query"`,
		"alias conflicts with unaliased wire name": `query "q"; query "search_query" upstream="q"`,
		"unknown property":                         `query "search_query" target="query"`,
		"body alias":                               `body "search_query" upstream="query"`,
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			src := `wrap x {
                auth bearer { value env "T" }
                can search card { path "/search"; ` + query + ` }
            }`
			if _, _, err := opcore.ParseInline([]byte(src)); err == nil {
				t.Fatal("ambiguous or invalid query alias should fail closed")
			}
		})
	}
}

func TestParseInlineTypedQueryFailsClosed(t *testing.T) {
	cases := map[string]string{
		"mixed shorthand and block": `query "limit" {
            field "after" type="string"
        }`,
		"query block property": `query mode="typed" {
            field "limit" type="integer"
        }`,
		"object value": `query {
            object "filter" raw=true
        }`,
		"unknown node": `query {
            tuple "filter"
        }`,
		"unsupported scalar type": `query {
            field "filter" type="object"
        }`,
		"duplicate local name": `query {
            field "limit" type="integer"
            field "limit" type="number"
        }`,
		"unknown property": `query {
            field "limit" type="integer" default=10
        }`,
		"minimum above maximum": `query {
            field "limit" type="integer" minimum=101 maximum=100
        }`,
		"numeric bound on string": `query {
            field "cursor" type="string" minimum=1
        }`,
		"array bound on scalar": `query {
            field "limit" type="integer" min-items=1
        }`,
		"negative min-items": `query {
            array "ids" items="string" min-items=-1
        }`,
		"fractional max-items": `query {
            array "ids" items="string" max-items=2.5
        }`,
		"unsupported array items": `query {
            array "ids" items="object"
        }`,
		"nested query field": `query {
            field "filter" type="string" { field "nested" type="string" }
        }`,
		"exclusive group too small": `query {
            field "before" type="string"
            mutually-exclusive "before"
        }`,
		"exclusive unknown field": `query {
            field "before" type="string"
            mutually-exclusive "before" "after"
        }`,
		"exclusive duplicate name": `query {
            field "before" type="string"
            mutually-exclusive "before" "before"
        }`,
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			src := `wrap x {
                auth bearer { value env "T" }
                can list message { path "/messages"; ` + query + ` }
            }`
			if _, _, err := opcore.ParseInline([]byte(src)); err == nil {
				t.Fatal("invalid typed query should fail closed")
			}
		})
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
		"proxy missing upstream": `wrap x {
            auth bearer { value env "T" }
            proxy browser_snapshot { allow url matches "^https://example.com/" }
        }`,
		"proxy bad selector": `wrap x {
            auth bearer { value env "T" }
            proxy browser_snapshot {
                upstream playwright browser_snapshot
                allow body matches "^x"
            }
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
