package specverb

import (
	"context"
	"strings"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/opcore"
	"github.com/urfave/cli/v3"
)

func TestQueryAliasReferenceUsesLocalAndUpstreamNames(t *testing.T) {
	params := paramsOf(opDescriptor{QueryFlags: []fieldFlag{
		{Name: "search_query", UpstreamName: "query", Type: "string"},
	}})
	if len(params) != 1 {
		t.Fatalf("params = %+v, want one query input", params)
	}
	if params[0].Name != "search_query" || params[0].UpstreamName != "query" {
		t.Errorf("aliased param = %+v", params[0])
	}
	for _, rendered := range []string{
		strings.Join(optionLines(params), "\n"),
		strings.Join(paramHelpLines(params), "\n"),
	} {
		if !strings.Contains(rendered, "search_query") || !strings.Contains(rendered, "query") {
			t.Errorf("reference omitted local or upstream name: %q", rendered)
		}
	}
}

func TestAssembleQueryUsesUpstreamName(t *testing.T) {
	f := opcore.Field{Name: "search_query", UpstreamName: "query"}
	got := ""
	cmd := &cli.Command{
		Flags: fieldFlagsToCLI([]fieldFlag{f}),
		Action: func(_ context.Context, c *cli.Command) error {
			got = assembleQuery(c, []fieldFlag{f})
			return nil
		},
	}
	if err := cmd.Run(context.Background(), []string{"test", "--search_query", "cards"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != "?query=cards" {
		t.Errorf("query = %q, want ?query=cards", got)
	}
}

func TestAssembleQueryRepeatsArrayValuesInInputOrder(t *testing.T) {
	f := opcore.Field{Name: "author_id", Type: "array", Items: "string"}
	got := ""
	cmd := &cli.Command{
		Flags: fieldFlagsToCLI([]fieldFlag{f}),
		Action: func(_ context.Context, c *cli.Command) error {
			got = assembleQuery(c, []fieldFlag{f})
			return nil
		},
	}
	if err := cmd.Run(context.Background(), []string{
		"test",
		"--author_id", "second",
		"--author_id", "first",
		"--author_id", "third",
	}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != "?author_id=second&author_id=first&author_id=third" {
		t.Errorf("query = %q, want repeated keys in input order", got)
	}
}
