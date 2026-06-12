# specgen SSM resolver: credentials-shadowing retry

The consumer `main.go` that `specgen.Render` emits resolves the auth token from SSM through aws-sdk-go-v2's default config chain. That chain prefers static keys in `~/.aws/credentials` over an SSO profile of the same name in `~/.aws/config`, so a stale static `[default]` silently shadows SSO and the API rejects the dead key. The aws CLI resolves SSO in that situation, so the two disagree and only the SDK path breaks.

The generated resolver retries once with the credentials file excluded, so the config-file SSO profile can resolve, and prints a one-line note naming the shadowing file. The exclusion is `WithSharedCredentialsFiles([]string{})` - a non-nil empty slice means "read no credentials file", while nil would fall back to the default chain and re-read the shadowing keys.

Static-only users never reach the retry - their first attempt already succeeded. Users whose first attempt fails for any other reason (no network, bad SSM path) see the original error: the retry only swallows the failure when the SSO path actually produces the parameter.
