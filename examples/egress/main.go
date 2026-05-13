// Command egress demonstrates the per-invocation CONNECT proxy with a
// pinned allowlist. The proxy logs every CONNECT and (in enforce mode)
// rejects hosts not on the list with HTTP 403. Used by cli-guard's
// passthrough wrapper to gate package-manager network reach.
//
//	go run ./examples/egress allowed
//	# https://example.com/ -> 200
//
//	go run ./examples/egress denied
//	# https://denied.example.invalid/ -> 403 from proxy
//
// Both runs print the captured egress-row summary at the end so the
// audit-trail shape is visible.
package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/coilysiren/cli-guard/egress"
	"github.com/urfave/cli/v3"
)

func dialThrough(proxyAddr, target string) (int, string, error) {
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

func run(_ context.Context, target string, mode egress.Mode) error {
	p := egress.New([]string{"example.com"}, mode)
	addr, err := p.Start(context.Background())
	if err != nil {
		return fmt.Errorf("start proxy: %w", err)
	}
	fmt.Println("proxy listening on", addr)
	code, status, err := dialThrough(addr, target)
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

func main() {
	app := &cli.Command{
		Name:    "egress-demo",
		Usage:   "show the CONNECT-proxy allowlist gate",
		Version: "v0.0.0",
		Commands: []*cli.Command{
			{
				Name:  "allowed",
				Usage: "dial a host that is on the allowlist",
				Action: func(ctx context.Context, _ *cli.Command) error {
					return run(ctx, "https://example.com/", egress.ModeEnforce)
				},
			},
			{
				Name:  "denied",
				Usage: "dial a host that is not on the allowlist",
				Action: func(ctx context.Context, _ *cli.Command) error {
					return run(ctx, "https://www.iana.org/", egress.ModeEnforce)
				},
			},
			{
				Name:  "observe",
				Usage: "log every host without enforcing",
				Action: func(ctx context.Context, _ *cli.Command) error {
					return run(ctx, "https://www.iana.org/", egress.ModeObserve)
				},
			},
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
