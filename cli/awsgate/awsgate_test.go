package awsgate

import (
	"strings"
	"testing"
)

func TestIsReadOnly(t *testing.T) {
	cases := map[string]bool{
		"s3 ls s3://bucket":                          true,
		"ec2 describe-instances":                     true,
		"iam list-roles":                             true,
		"sts get-caller-identity":                    true,
		"dynamodb scan --table-name t":               true,
		"ssm get-parameter --name /x":                true,
		"s3 cp a b":                                  false,
		"ec2 terminate-instances --instance-ids i-1": false,
		"ssm put-parameter --name /x":                false,
		"s3":                                         false, // service only
		"--region us-east-1 ec2 describe-instances":  true,  // global flag skipped
	}
	for argv, want := range cases {
		if got := IsReadOnly(split(argv)); got != want {
			t.Errorf("IsReadOnly(%q) = %v, want %v", argv, got, want)
		}
	}
}

func TestCheckDeniesSensitiveRead(t *testing.T) {
	g := Gate{}
	token, pattern, denied := g.Check(split("s3 ls s3://prod-secrets-bucket"))
	if !denied {
		t.Fatal("sensitive read not denied")
	}
	if token != "s3://prod-secrets-bucket" || pattern != "*secret*" {
		t.Errorf("denial = (%q, %q), want the secrets token and pattern", token, pattern)
	}
}

func TestCheckPassesWritesAndCleanReads(t *testing.T) {
	g := Gate{}
	if _, _, denied := g.Check(split("s3 cp local s3://prod-secrets-bucket")); denied {
		t.Error("write verb must pass the read gate")
	}
	if _, _, denied := g.Check(split("ec2 describe-instances")); denied {
		t.Error("non-sensitive read must pass")
	}
}

func TestCheckEscapes(t *testing.T) {
	g := Gate{AllowPatterns: []string{"*prod-secrets*"}}
	if _, _, denied := g.Check(split("s3 ls s3://prod-secrets-bucket")); denied {
		t.Error("allow glob must escape the gate")
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, s string
		want       bool
	}{
		{"*secret*", "s3://my-secrets", true},
		{"arn:aws:iam::*:role/*admin*", "arn:aws:iam::123:role/super-admin", true},
		{"*tfstate*", "bucket-of-cats", false},
		{"exact", "exact", true},
		{"exact", "exactly", false},
	}
	for _, c := range cases {
		if got := GlobMatch(c.pattern, c.s); got != c.want {
			t.Errorf("GlobMatch(%q, %q) = %v, want %v", c.pattern, c.s, got, c.want)
		}
	}
}

// split is a whitespace argv splitter for test readability.
func split(s string) []string {
	return strings.Fields(s)
}
