package guardfile

import (
	"os"
	"path/filepath"
	"reflect"
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
