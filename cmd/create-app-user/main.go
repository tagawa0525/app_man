package main

import (
	"flag"
	"fmt"
	"os"
)

const binaryName = "appmgr-create-app-user"

func main() {
	flag.String("username", "", "username for the local admin account")
	flag.String("role", "system_admin", "role to grant (system_admin / department_security_admin / license_manager / viewer)")
	flag.Bool("reset-password", false, "reset password for an existing user")
	flag.String("notify-email", "", "notification email address (recommended for local admins)")
	flag.Parse()

	fmt.Fprintf(os.Stdout, "%s: not implemented (flag skeleton only)\n", binaryName)
	os.Exit(0)
}
