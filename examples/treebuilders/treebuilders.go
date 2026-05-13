// Package treebuilders exports each examples/<name>/main.go's *cli.Command
// tree so scripts/gen-webdocs can render it, and so each example main
// stays a thin shim that drives the tree.
package treebuilders

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/coilysiren/cli-guard/audit"
	"github.com/coilysiren/cli-guard/egress"
	"github.com/coilysiren/cli-guard/exitcode"
	"github.com/coilysiren/cli-guard/gittree"
	"github.com/coilysiren/cli-guard/passthrough"
	"github.com/coilysiren/cli-guard/policy"
	"github.com/coilysiren/cli-guard/repocfg"
	"github.com/coilysiren/cli-guard/scope"
	"github.com/coilysiren/cli-guard/shell"
	"github.com/coilysiren/cli-guard/verb"
	"github.com/urfave/cli/v3"
)

// Audit is the tree for examples/audit. writer may be nil for doc
// rendering since Actions are not executed.
func Audit(writer *audit.Writer) *cli.Command {
	return &cli.Command{
		Name:    "demo",
		Usage:   "tiny cli-guard demo app",
		Version: "v0.0.0",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  verb.CommitScopeFlag,
				Value: "auto",
				Usage: "bind audit rows to a commit scope",
			},
		},
		Commands: []*cli.Command{
			{
				Name:      "hello",
				Usage:     "print a greeting",
				ArgsUsage: "<name>",
				Action: verb.Wrap(verb.Spec{
					Name: "hello",
					ArgsFunc: func(c *cli.Command) (map[string]string, []string) {
						return nil, c.Args().Slice()
					},
					Action: func(_ context.Context, c *cli.Command) error {
						name := c.Args().First()
						if name == "" {
							name = "world"
						}
						fmt.Printf("hello, %s\n", name)
						return nil
					},
				}, writer),
			},
		},
	}
}

func egressDialThrough(proxyAddr, target string) (int, string, error) {
	proxyURL, err := url.Parse("http://" + proxyAddr)
	if err != nil {
		return 0, "", err
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}
	req, err := http.NewRequest("HEAD", target, nil)
	if err != nil {
		return 0, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, resp.Status, nil
}

func egressRun(_ context.Context, target string, mode egress.Mode) error {
	p := egress.New([]string{"example.com"}, mode)
	addr, err := p.Start(context.Background())
	if err != nil {
		return fmt.Errorf("start proxy: %w", err)
	}
	fmt.Println("proxy listening on", addr)
	code, status, err := egressDialThrough(addr, target)
	if err != nil {
		fmt.Println("dial error:", err)
	} else {
		fmt.Println("response:", code, status)
	}
	rows := p.Stop()
	fmt.Println("egress rows:")
	for _, r := range rows {
		fmt.Printf("  host=%-32s decision=%-7s up=%d down=%d ms=%d\n", r.Host, r.Decision, r.BytesUp, r.BytesDown, r.DurationMS)
	}
	return nil
}

// Egress is the tree for examples/egress.
func Egress() *cli.Command {
	return &cli.Command{
		Name:    "egress-demo",
		Usage:   "show the CONNECT-proxy allowlist gate",
		Version: "v0.0.0",
		Commands: []*cli.Command{
			{
				Name:  "allowed",
				Usage: "dial a host that is on the allowlist",
				Action: func(ctx context.Context, _ *cli.Command) error {
					return egressRun(ctx, "https://example.com/", egress.ModeEnforce)
				},
			},
			{
				Name:  "denied",
				Usage: "dial a host that is not on the allowlist",
				Action: func(ctx context.Context, _ *cli.Command) error {
					return egressRun(ctx, "https://www.iana.org/", egress.ModeEnforce)
				},
			},
			{
				Name:  "observe",
				Usage: "log every host without enforcing",
				Action: func(ctx context.Context, _ *cli.Command) error {
					return egressRun(ctx, "https://www.iana.org/", egress.ModeObserve)
				},
			},
		},
	}
}

// Exitcode is the tree for examples/exitcode.
func Exitcode() *cli.Command {
	return &cli.Command{
		Name:    "exitcode-demo",
		Usage:   "show the public exit-code taxonomy",
		Version: "v0.0.0",
		Commands: []*cli.Command{
			{Name: "success", Action: func(_ context.Context, _ *cli.Command) error { return nil }},
			{Name: "policy", Action: func(_ context.Context, _ *cli.Command) error {
				return exitcode.New(exitcode.PolicyDenied, "policy", errors.New("argv rejected"), "fix the input")
			}},
			{Name: "upstream", Action: func(_ context.Context, _ *cli.Command) error {
				return exitcode.New(exitcode.UpstreamFailed, "upstream_failed", errors.New("wrapped tool exited 7"), "check the tool")
			}},
			{Name: "internal", Action: func(_ context.Context, _ *cli.Command) error {
				return exitcode.New(exitcode.Internal, "internal", errors.New("config load failed"), "report a bug")
			}},
		},
	}
}

// Gittree is the tree for examples/gittree.
func Gittree() *cli.Command {
	return &cli.Command{
		Name:    "gittree-demo",
		Usage:   "show the clean+synced gate",
		Version: "v0.0.0",
		Commands: []*cli.Command{
			{
				Name:  "build",
				Usage: "pretend-build verb, gated on a clean tree",
				Action: func(_ context.Context, _ *cli.Command) error {
					cwd, _ := os.Getwd()
					state, err := gittree.CheckClean(cwd)
					if err != nil {
						return fmt.Errorf("refused: %w", err)
					}
					_ = state
					fmt.Println("ok: tree is clean, pretend-build runs")
					return nil
				},
			},
		},
	}
}

// Passthrough is the tree for examples/passthrough. writer/runner may be
// nil for doc rendering. When nil, a placeholder echo command is
// constructed so the rendered shape matches the runtime example.
func Passthrough(runner *shell.Runner, writer *audit.Writer) *cli.Command {
	var echoCmd *cli.Command
	if runner != nil && writer != nil {
		echoCmd = passthrough.Command("echo", runner, writer)
	} else {
		echoCmd = &cli.Command{
			Name:  "echo",
			Usage: "audited passthrough wrapper around /bin/echo",
		}
	}
	return &cli.Command{
		Name:     "passthrough-demo",
		Usage:    "wrap an existing binary as an audited subcommand",
		Version:  "v0.0.0",
		Commands: []*cli.Command{echoCmd},
	}
}

// Policy is the tree for examples/policy.
func Policy() *cli.Command {
	return &cli.Command{
		Name:    "policy-demo",
		Usage:   "show shell-metacharacter argv rejection",
		Version: "v0.0.0",
		Commands: []*cli.Command{
			{
				Name:      "safe",
				Usage:     "validates a single positional arg",
				ArgsUsage: "<value>",
				Action: func(_ context.Context, c *cli.Command) error {
					vals := c.Args().Slice()
					if err := policy.ValidateArgSlice("positional", vals); err != nil {
						return err
					}
					fmt.Printf("accepted: %v\n", vals)
					return nil
				},
			},
			{
				Name:      "unsafe",
				Usage:     "demonstrate the rejection path",
				ArgsUsage: "<value-with-shell-metachar>",
				Action: func(_ context.Context, c *cli.Command) error {
					vals := c.Args().Slice()
					if err := policy.ValidateArgSlice("positional", vals); err != nil {
						return err
					}
					fmt.Printf("accepted (unexpected): %v\n", vals)
					return nil
				},
			},
		},
	}
}

// Repocfg is the tree for examples/repocfg. cfg may be nil for doc
// rendering since Actions are not executed.
func Repocfg(cfg *repocfg.Config) *cli.Command {
	return &cli.Command{
		Name:    "repocfg-demo",
		Usage:   "show per-repo command allowlist loading",
		Version: "v0.0.0",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "print the verbs declared in coily.yaml",
				Action: func(_ context.Context, _ *cli.Command) error {
					if cfg == nil {
						return errors.New("no config loaded")
					}
					for _, v := range cfg.Commands {
						fmt.Printf("%s: %s\n", v.Name, strings.Join(v.Argv, " "))
					}
					return nil
				},
			},
			{
				Name:      "run",
				Usage:     "execute a declared verb by name",
				ArgsUsage: "<verb>",
				Action: func(ctx context.Context, c *cli.Command) error {
					if cfg == nil {
						return errors.New("no config loaded")
					}
					name := c.Args().First()
					if name == "" {
						return errors.New("usage: run <verb>")
					}
					for _, v := range cfg.Commands {
						if v.Name == name {
							cmd := exec.CommandContext(ctx, v.Argv[0], v.Argv[1:]...) //nolint:gosec // tokens validated at load time
							cmd.Stdout = os.Stdout
							cmd.Stderr = os.Stderr
							return cmd.Run()
						}
					}
					return fmt.Errorf("no such verb: %s", name)
				},
			},
		},
	}
}

// Scope is the tree for examples/scope.
func Scope() *cli.Command {
	return &cli.Command{
		Name:    "scope-demo",
		Usage:   "show --commit-scope resolution",
		Version: "v0.0.0",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  verb.CommitScopeFlag,
				Value: "auto",
				Usage: "bind audit rows to a commit scope (auto resolves to git toplevel of cwd)",
			},
		},
		Commands: []*cli.Command{
			{
				Name:  "where",
				Usage: "print the resolved commit scope",
				Action: func(_ context.Context, c *cli.Command) error {
					cwd, _ := os.Getwd()
					flagVal := c.String(verb.CommitScopeFlag)
					resolved, err := scope.Resolve(flagVal, "", cwd)
					if err != nil {
						return fmt.Errorf("scope.Resolve: %w", err)
					}
					fmt.Printf("scope: %s\n", resolved)
					return nil
				},
			},
		},
	}
}
