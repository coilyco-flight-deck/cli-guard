package lockdown_test

import "forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/lockdown"

// testDriver returns a ClaudeCode-shaped driver pre-wired with sample
// consumer data. Tests parameterize over Driver via
func testDriver() *lockdown.Driver {
	return lockdown.ClaudeCode(
		"app",
		[]string{
			"/opt/homebrew/bin/app",
			"/usr/local/bin/app",
			"/home/linuxbrew/.linuxbrew/bin/app",
		},
		map[string]string{
			"gh":        "app ops gh",
			"aws":       "app ops aws",
			"kubectl":   "app ops kubectl",
			"docker":    "app docker",
			"tailscale": "app tailscale",
			"npm":       "app pkg npm",
			"pnpm":      "app pkg pnpm",
			"yarn":      "app pkg yarn",
			"bun":       "app pkg bun",
			"uv":        "app pkg uv",
			"pip":       "app pkg pip",
			"pipx":      "app pkg pipx",
			"poetry":    "app pkg poetry",
			"cargo":     "app pkg cargo",
			"gem":       "app pkg gem",
			"bundle":    "app pkg bundle",
			"brew":      "app brew",
			"make":      "app exec <verb>",
			"just":      "app exec <verb>",
			"task":      "app exec <verb>",
			"invoke":    "app exec <verb>",
		},
	)
}
