package mgr

import (
	"fmt"
	"os/exec"
	"regexp"
)

var gitCommitSHA = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// isGitCommitSHA reports whether version is a 40-character git object id.
func isGitCommitSHA(version string) bool {
	return gitCommitSHA.MatchString(version)
}

func cloneModuleRepo(repoURL, version, buildDir string) error {
	if isGitCommitSHA(version) {
		return cloneModuleAtCommit(repoURL, version, buildDir)
	}
	cmd := exec.Command("git", "clone", "--depth", "1", "--branch", version, repoURL, buildDir) //nolint:gosec,noctx // repoURL/version from spool config
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("clone %s@%s: %w\n%s", repoURL, version, err, out)
	}
	return nil
}

func cloneModuleAtCommit(repoURL, commit, buildDir string) error {
	cmd := exec.Command("git", "clone", "--depth", "1", repoURL, buildDir) //nolint:gosec,noctx // repoURL/buildDir from spool config
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("clone %s: %w\n%s", repoURL, err, out)
	}
	fetch := exec.Command("git", "-C", buildDir, "fetch", "--depth", "1", "origin", commit) //nolint:gosec,noctx // commit from spool config
	if out, err := fetch.CombinedOutput(); err != nil {
		return fmt.Errorf("fetch %s@%s: %w\n%s", repoURL, commit, err, out)
	}
	checkout := exec.Command("git", "-C", buildDir, "checkout", commit) //nolint:gosec,noctx // commit from spool config
	if out, err := checkout.CombinedOutput(); err != nil {
		return fmt.Errorf("checkout %s@%s: %w\n%s", repoURL, commit, err, out)
	}
	return nil
}
