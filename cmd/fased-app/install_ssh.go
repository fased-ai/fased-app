package main

import (
	"github.com/fased-ai/fased-app/internal/privateaccess"
	"net"
	"os"
	"os/user"
	"regexp"
	"strconv"
	"strings"
)

// Display-only SSH metadata. It never changes network policy or executes text.
func installationSSHPlan(plan privateaccess.Plan, connection string) privateaccess.Plan {
	u, e := user.Current()
	if e != nil {
		return plan
	}
	username := u.Username
	if hint := os.Getenv("FASED_INSTALL_SSH_USER"); hint != "" {
		if !regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,30}$`).MatchString(hint) {
			return plan
		}
		username = hint
	}
	return installationSSHPlanFor(plan, connection, username)
}
func installationSSHPlanFor(plan privateaccess.Plan, connection, username string) privateaccess.Plan {
	fields := strings.Fields(connection)
	if plan.Mode != "ssh" || len(fields) != 4 || !regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,30}$`).MatchString(username) || net.ParseIP(fields[0]) == nil || net.ParseIP(fields[2]) == nil {
		return plan
	}
	for _, p := range []string{fields[1], fields[3]} {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 || strconv.Itoa(n) != p {
			return plan
		}
	}
	destination := username + "@" + fields[2]
	instructions := append([]string(nil), plan.Instructions...)
	for i, line := range instructions {
		line = strings.ReplaceAll(line, "USER@VPS", destination)
		if fields[3] != "22" {
			line = strings.ReplaceAll(line, "ssh -N ", "ssh -p "+fields[3]+" -N ")
		}
		instructions[i] = line
	}
	plan.Instructions = instructions
	return plan
}
