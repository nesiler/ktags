package doctor

import "context"

func init() {
	register(Check{ID: "platform", Title: "platform", Order: 10, Run: checkPlatform})
}

// checkPlatform is informational: it names what the other checks were measured on.
func checkPlatform(_ context.Context, env Env) []Result {
	how := "the ktags service starts in the background through launchd"
	if env.Foreground {
		how = "no background service manager integration; the ktags service runs with `ktags service run`"
	}
	return []Result{{Status: StatusOK, Evidence: env.GOOS + "/" + env.GOARCH + ": " + how}}
}
