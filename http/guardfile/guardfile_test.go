package guardfile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseFixture(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "forgejo.kdl"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	gf, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if got, want := gf.Group, []string{"ward", "ops", "forgejo"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Group = %v, want %v", got, want)
	}
	if got, want := gf.Spec, "forgejo.swagger.v1.json"; got != want {
		t.Errorf("Spec = %q, want %q", got, want)
	}
	if got, want := gf.BaseURL, "https://forgejo.coilysiren.me/api/v1"; got != want {
		t.Errorf("BaseURL = %q, want %q", got, want)
	}

	wantAuth := Auth{Scheme: "header-token", Header: "Authorization", Prefix: "token ", SSM: "/forgejo/api-token"}
	if !reflect.DeepEqual(gf.Auth, wantAuth) {
		t.Errorf("Auth = %+v, want %+v", gf.Auth, wantAuth)
	}

	wantGrants := []Grant{
		{Modal: "can", Verb: "read", Resource: "repos"},
		{Modal: "can", Verb: "create", Resource: "repos"},
		{Modal: "can", Verb: "delete", Resource: "repos"},
	}
	if !reflect.DeepEqual(gf.Grants, wantGrants) {
		t.Errorf("Grants = %+v, want %+v", gf.Grants, wantGrants)
	}
}

// TestBareTokensAreStrings asserts the flat policy body and the dotted spec
// filename parse as bare identifiers, so authors never quote outside the header.
func TestBareTokensAreStrings(t *testing.T) {
	src := []byte(`wrap ward ops forgejo {
    spec forgejo.swagger.v1.json
    auth header-token {
        header Authorization
        ssm "/forgejo/api-token"
    }
    can delete labels created-by-me
}`)
	gf, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if gf.Spec != "forgejo.swagger.v1.json" {
		t.Errorf("dotted bare spec did not round-trip: %q", gf.Spec)
	}
	want := Grant{Modal: "can", Verb: "delete", Resource: "labels", Qualifiers: []string{"created-by-me"}}
	if len(gf.Grants) != 1 || !reflect.DeepEqual(gf.Grants[0], want) {
		t.Errorf("flat qualifier sentence = %+v, want %+v", gf.Grants, want)
	}
}

// TestGrantDescribeAnnotation asserts a grant-body `describe "..."` child flows
// into Grant.Describe, the per-grant slot that enriches the thin upstream spec.
func TestGrantDescribeAnnotation(t *testing.T) {
	src := []byte(`wrap ward ops forgejo {
	    spec s
	    auth header-token { header H; ssm S }
	    can delete repos {
	        describe "irreversible: deletes the repo and all its data"
	    }
	}`)
	gf, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := Grant{Modal: "can", Verb: "delete", Resource: "repos", Describe: "irreversible: deletes the repo and all its data"}
	if len(gf.Grants) != 1 || !reflect.DeepEqual(gf.Grants[0], want) {
		t.Errorf("grant with describe = %+v, want %+v", gf.Grants, want)
	}
}

// TestGrantProperties asserts KDL key=value properties land in Grant.Props,
// distinct from positional bareword qualifiers.
func TestGrantProperties(t *testing.T) {
	src := []byte(`wrap ward ops forgejo {
	    spec s
	    auth header-token { header H; ssm S }
	    can delete repos org="coilyco-flight-deck"
	}`)
	gf, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := Grant{Modal: "can", Verb: "delete", Resource: "repos", Props: map[string]string{"org": "coilyco-flight-deck"}}
	if len(gf.Grants) != 1 || !reflect.DeepEqual(gf.Grants[0], want) {
		t.Errorf("grant with org property = %+v, want %+v", gf.Grants, want)
	}
}

// TestParseAction asserts the complex-action grammar round-trips: inputs, the
// poll primitive with its bounds, the multiline `until`, and `fail-when`.
func TestParseAction(t *testing.T) {
	src := []byte(`wrap ward ops forgejo {
    spec s
    auth header-token { header H; ssm S }
    can list tasks

    action ci-watch {
        describe "Watch a CI run to completion."
        input repo { positional; required; help "owner/name" }
        input run  { flag; help "run number" }
        poll list tasks {
            args { owner-repo $repo }
            until """
                length([?run_number==$run && status!='success'
                        && status!='failure']) == ` + "`0`" + `
                """
            every "10s"
            timeout "30m"
            as run_tasks
        }
        fail-when "length(run_tasks[?status=='failure']) > ` + "`0`" + `"
    }
}`)
	gf, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(gf.Actions) != 1 {
		t.Fatalf("Actions = %d, want 1", len(gf.Actions))
	}
	act := gf.Actions[0]
	if act.Name != "ci-watch" {
		t.Errorf("Name = %q, want ci-watch", act.Name)
	}
	if act.Describe != "Watch a CI run to completion." {
		t.Errorf("Describe = %q", act.Describe)
	}
	wantInputs := []Input{
		{Name: "repo", Positional: true, Required: true, Help: "owner/name"},
		{Name: "run", Positional: false, Required: false, Help: "run number"},
	}
	if !reflect.DeepEqual(act.Inputs, wantInputs) {
		t.Errorf("Inputs = %+v, want %+v", act.Inputs, wantInputs)
	}
	if act.Poll == nil {
		t.Fatal("Poll is nil")
	}
	if act.Poll.Verb != "list" || act.Poll.Resource != "tasks" {
		t.Errorf("Poll target = %q %q, want list tasks", act.Poll.Verb, act.Poll.Resource)
	}
	wantArgs := []ArgBind{{Name: "owner-repo", Value: "$repo"}}
	if !reflect.DeepEqual(act.Poll.Args, wantArgs) {
		t.Errorf("Poll.Args = %+v, want %+v", act.Poll.Args, wantArgs)
	}
	if act.Poll.Every != "10s" || act.Poll.Timeout != "30m" || act.Poll.As != "run_tasks" {
		t.Errorf("Poll bounds = every %q timeout %q as %q", act.Poll.Every, act.Poll.Timeout, act.Poll.As)
	}
	// The multiline `until` keeps its inner alignment, dedented to the close.
	if !strings.Contains(act.Poll.Until, "length([?run_number==$run") || !strings.Contains(act.Poll.Until, "status!='failure'") {
		t.Errorf("Until did not round-trip the multiline expression:\n%s", act.Poll.Until)
	}
	if act.FailWhen != "length(run_tasks[?status=='failure']) > `0`" {
		t.Errorf("FailWhen = %q", act.FailWhen)
	}
}

// TestParseActionFailsClosed asserts the action grammar rejects every malformed
// or reserved shape, never silently dropping a node.
func TestParseActionFailsClosed(t *testing.T) {
	hdr := "wrap w {\n spec s\n auth header-token { header H; ssm S }\n"
	cases := map[string]string{
		"no poll":            hdr + `action a { describe "x" } }`,
		"poll missing every": hdr + `action a { poll list tasks { until "x"; timeout "1m"; as r } } }`,
		"poll missing until": hdr + `action a { poll list tasks { every "1s"; timeout "1m"; as r } } }`,
		"poll missing as":    hdr + `action a { poll list tasks { until "x"; every "1s"; timeout "1m" } } }`,
		"two polls":          hdr + `action a { poll list tasks { until "x"; every "1s"; timeout "1m"; as r }; poll list tasks { until "y"; every "1s"; timeout "1m"; as q } } }`,
		"input no kind":      hdr + `action a { input repo { required }; poll list tasks { until "x"; every "1s"; timeout "1m"; as r } } }`,
		"reserved each":      hdr + `action a { each "x" { }; poll list tasks { until "x"; every "1s"; timeout "1m"; as r } } }`,
		"reserved emit":      hdr + `action a { poll list tasks { until "x"; every "1s"; timeout "1m"; as r; emit "x" } } }`,
		"unknown poll node":  hdr + `action a { poll list tasks { until "x"; every "1s"; timeout "1m"; as r; bogus "x" } } }`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(src)); err == nil {
				t.Errorf("expected error for %s, got nil", name)
			}
		})
	}
}

func TestParseFailsClosed(t *testing.T) {
	cases := map[string]string{
		"unknown node": `wrap ward ops forgejo {
			spec s
			auth header-token { header H; ssm S }
			allow read repos
		}`,
		"missing spec": `wrap ward ops forgejo {
			auth header-token { header H; ssm S }
			can read repos
		}`,
		"missing auth": `wrap ward ops forgejo {
			spec s
			can read repos
		}`,
		"no group": `wrap {
			spec s
			auth header-token { header H; ssm S }
		}`,
		"grant missing resource": `wrap ward ops forgejo {
			spec s
			auth header-token { header H; ssm S }
			can read
		}`,
		"unsupported auth scheme": `wrap ward ops forgejo {
			spec s
			auth bearer { header H; ssm S }
		}`,
		"unknown grant-body node": `wrap ward ops forgejo {
			spec s
			auth header-token { header H; ssm S }
			can delete repos { explain "nope" }
		}`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(src)); err == nil {
				t.Errorf("expected error for %s, got nil", name)
			}
		})
	}
}
