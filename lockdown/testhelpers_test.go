package lockdown_test

import "github.com/coilysiren/cli-guard/lockdown"

// testDriver returns a ClaudeCode-shaped driver pre-wired with the
// historical coily-specific data. Tests parameterize over Driver via
func testDriver() *lockdown.Driver {
	return lockdown.ClaudeCode(
		"coily",
		[]string{
			"/opt/homebrew/bin/coily",
			"/usr/local/bin/coily",
			"/home/linuxbrew/.linuxbrew/bin/coily",
		},
		map[string]string{
			"gh":        "coily ops gh",
			"aws":       "coily ops aws",
			"kubectl":   "coily ops kubectl",
			"docker":    "coily docker",
			"tailscale": "coily tailscale",
			"ssh":       "coily ssh",
			"scp":       "coily ssh copy",
			"npm":       "coily pkg npm",
			"pnpm":      "coily pkg pnpm",
			"yarn":      "coily pkg yarn",
			"bun":       "coily pkg bun",
			"uv":        "coily pkg uv",
			"pip":       "coily pkg pip",
			"pipx":      "coily pkg pipx",
			"poetry":    "coily pkg poetry",
			"cargo":     "coily pkg cargo",
			"gem":       "coily pkg gem",
			"bundle":    "coily pkg bundle",
			"brew":      "coily brew",
			"make":      "coily exec <verb>",
			"just":      "coily exec <verb>",
			"task":      "coily exec <verb>",
			"invoke":    "coily exec <verb>",
		},
	)
}
