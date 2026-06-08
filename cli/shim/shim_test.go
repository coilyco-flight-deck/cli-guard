package shim

import (
	"strings"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/hook"
)

func TestFor_DenyShimContents(t *testing.T) {
	specs, err := For([]hook.Protected{
		{Name: "gcloud", Hint: "Use kap for cloud ops."},
	})
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("got %d specs, want 1", len(specs))
	}
	s := specs[0]
	if s.Name != "gcloud" {
		t.Errorf("Name = %q, want gcloud", s.Name)
	}
	for _, want := range []string{
		"#!/bin/sh",
		"PATH shim for 'gcloud'",
		"cli-guard: direct `gcloud` is blocked. Recovery: Use kap for cloud ops.",
		"exit 2",
	} {
		if !strings.Contains(s.Body, want) {
			t.Errorf("body missing %q\n---\n%s", want, s.Body)
		}
	}
}

func TestFor_RecoveryPrecedence(t *testing.T) {
	cases := []struct {
		name      string
		p         hook.Protected
		wantInMsg string
	}{
		{
			name:      "explicit hint wins over wrappers",
			p:         hook.Protected{Name: "gcloud", Hint: "Use kap.", Wrappers: []string{"kap", "ward"}},
			wantInMsg: "Recovery: Use kap.",
		},
		{
			name:      "wrappers synthesize a hint when none given",
			p:         hook.Protected{Name: "vault", Wrappers: []string{"kap", "ward"}},
			wantInMsg: "Recovery: route through an audited wrapper: kap, ward.",
		},
		{
			name:      "no hint and no wrappers is a bare deny",
			p:         hook.Protected{Name: "ssh"},
			wantInMsg: "cli-guard: direct `ssh` is blocked.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			specs, err := For([]hook.Protected{tc.p})
			if err != nil {
				t.Fatalf("For: %v", err)
			}
			if !strings.Contains(specs[0].Body, tc.wantInMsg) {
				t.Errorf("body missing %q\n---\n%s", tc.wantInMsg, specs[0].Body)
			}
			// A bare deny must not carry the "Recovery:" label.
			if tc.p.Hint == "" && len(tc.p.Wrappers) == 0 && strings.Contains(specs[0].Body, "Recovery:") {
				t.Errorf("bare deny should not mention Recovery:\n%s", specs[0].Body)
			}
		})
	}
}

func TestFor_BasenameAndDedup(t *testing.T) {
	// An absolute-path Name reduces to its basename; a duplicate basename
	// is emitted once. Every body still passes sh -n (For would have erred).
	specs, err := For([]hook.Protected{
		{Name: "/opt/homebrew/bin/gcloud", Wrappers: []string{"kap"}},
		{Name: "gcloud"},
		{Name: "aws", Wrappers: []string{"kap"}},
	})
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	names := make([]string, len(specs))
	for i, s := range specs {
		names[i] = s.Name
	}
	// Sorted, deduped: aws then gcloud.
	if len(names) != 2 || names[0] != "aws" || names[1] != "gcloud" {
		t.Fatalf("names = %v, want [aws gcloud]", names)
	}
}

func TestFor_EmptyInput(t *testing.T) {
	specs, err := For(nil)
	if err != nil {
		t.Fatalf("For(nil): %v", err)
	}
	if len(specs) != 0 {
		t.Errorf("got %d specs, want 0", len(specs))
	}
}

func TestFor_QuotesAreEscaped(t *testing.T) {
	// A single quote in the hint must not break the rendered script; For's
	// internal sh -n guard would reject it otherwise.
	specs, err := For([]hook.Protected{
		{Name: "gcloud", Hint: "don't run this directly"},
	})
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if !strings.Contains(specs[0].Body, `don'\''t run this directly`) {
		t.Errorf("single quote not escaped via '\\'' idiom\n%s", specs[0].Body)
	}
}

func TestSQ(t *testing.T) {
	cases := map[string]string{
		"gcloud":  `'gcloud'`,
		"a'b":     `'a'\''b'`,
		"":        `''`,
		"$(rm x)": `'$(rm x)'`, // metacharacters stay literal inside single quotes
	}
	for in, want := range cases {
		if got := sq(in); got != want {
			t.Errorf("sq(%q) = %q, want %q", in, got, want)
		}
	}
}
