package cli

import "fmt"

func VersionCmd(args []string) int {
	fmt.Fprintf(stdout, "trail %s (commit %s, built %s)\n", version, commit, date)
	return 0
}
