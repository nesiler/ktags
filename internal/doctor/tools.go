package doctor

import "context"

func init() {
	register(Check{ID: "tools.ssh", Title: "ssh is on PATH", Order: 50, Run: checkSSH})
}

// checkSSH finds the OpenSSH client that Phase 0 connects with.
func checkSSH(_ context.Context, env Env) []Result {
	path, err := env.LookPath("ssh")
	if err != nil {
		// macOS ships ssh in /usr/bin, so a miss there is a PATH without /usr/bin.
		fix := `export PATH="/usr/bin:$PATH"`
		if env.GOOS != "darwin" {
			fix = "sudo apt-get install -y openssh-client"
		}
		return []Result{{Status: StatusFail, Evidence: "ssh is not on PATH", Fix: fix}}
	}
	return []Result{{Status: StatusOK, Evidence: "ssh is " + path}}
}
