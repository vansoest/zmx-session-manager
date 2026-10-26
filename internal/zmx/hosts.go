package zmx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// HostsFile returns the path to the za hosts config:
// $XDG_CONFIG_HOME/za/hosts or ~/.config/za/hosts.
// Each non-empty, non-comment line is an ssh alias serving zmx sessions.
func HostsFile() (string, bool) {
	if dir, err := os.UserConfigDir(); err == nil {
		p := filepath.Join(dir, "za", "hosts")
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
		return p, false
	}
	return "", false
}

// LoadHosts parses the za hosts file, one ssh alias per line.
// '#' starts a comment; blank lines are skipped.
func LoadHosts() []string {
	path, ok := HostsFile()
	if !ok {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var hosts []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line != "" {
			hosts = append(hosts, line)
		}
	}
	return hosts
}

// FetchAll merges local sessions with sessions fetched over ssh from the
// configured hosts. Remote rows get Host set to their alias; local rows keep
// Host empty. Per-host fetch errors are returned separately so the TUI can
// log them without failing the whole list.
func FetchAll() ([]Session, []error) {
	hosts := LoadHosts()
	results := make([][]Session, len(hosts))
	errs := make([]error, len(hosts))
	var wg sync.WaitGroup
	for i, host := range hosts {
		wg.Add(1)
		go func(i int, host string) {
			defer wg.Done()
			results[i], errs[i] = FetchSessionsFrom(host)
		}(i, host)
	}
	wg.Wait()

	local, err := FetchSessions()
	if err != nil {
		errs = append([]error{fmt.Errorf("local: %w", err)}, errs...)
	}
	merged := local
	for i := range results {
		for _, s := range results[i] {
			s.Host = hosts[i]
			merged = append(merged, s)
		}
	}
	return merged, nonNilErrs(errs)
}

func nonNilErrs(errs []error) []error {
	var out []error
	for _, e := range errs {
		if e != nil {
			out = append(out, e)
		}
	}
	return out
}

// FetchSessionsFrom runs `zmx list` on the given host over ssh and parses the
// output.
func FetchSessionsFrom(host string) ([]Session, error) {
	out, err := runCombinedOutput("ssh", sshArgs(host, "list")...)
	if err != nil {
		return nil, fmt.Errorf("ssh %s zmx list: %w\n%s", host, err, shownErrorTail(out))
	}
	return parseList(string(out)), nil
}

// shownErrorTail trims warning spam from stderr mixed into ssh output, keeping
// the last lines that carry the real error.
func shownErrorTail(out []byte) string {
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) > 4 {
		lines = lines[len(lines)-4:]
	}
	return strings.Join(lines, "\n")
}

// sshArgs builds the ssh invocation for a remote subcommand over a login
// shell so profile PATHs resolve zmx under non-interactive ssh. Options make
// headless use fail fast instead of prompting.
func sshArgs(host string, sub string) []string {
	return []string{
		"-o", "ConnectTimeout=3",
		"-o", "BatchMode=yes",
		host,
		"sh", "-lc", "zmx " + sub,
	}
}
