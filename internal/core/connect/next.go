package connect

import (
	"fmt"
	"strings"
)

// Runbook ids. They are stable names of the runbook pages (docs/runbooks/, later), linked by
// name so they survive a repository move (development.md §9).
const (
	RunbookSSHUnreachable       = "ssh-unreachable"
	RunbookHostKeyChanged       = "host-key-changed"
	RunbookSSHAuthFailed        = "ssh-auth-failed"
	RunbookSSHSudoMissing       = "ssh-sudo-missing"
	RunbookKubeAPIUnreachable   = "kube-api-unreachable"
	RunbookKubeAuthFailed       = "kube-auth-failed"
	RunbookKubeForbidden        = "kube-forbidden"
	RunbookRancherUnreachable   = "rancher-unreachable"
	RunbookRancherAuthFailed    = "rancher-auth-failed"
	RunbookRancherForbidden     = "rancher-forbidden"
	RunbookCheckUnclassified    = "check-unclassified"
	RunbookSSHNotConfigured     = "ssh-not-configured"
	RunbookKubeNotConfigured    = "kube-not-configured"
	RunbookRancherNotConfigured = "rancher-not-configured"
)

// runbook is the runbook of a failed check. A network failure (DNS, TCP, timeout) names the
// layer of the check that saw it; a cancelled check has none.
func runbook(check string, kind Kind) string {
	layer, _, _ := strings.Cut(check, ".")
	switch kind {
	case KindDNS, KindTCP, KindTimeout:
		switch layer {
		case "ssh":
			return RunbookSSHUnreachable
		case "kube":
			return RunbookKubeAPIUnreachable
		case "rancher":
			return RunbookRancherUnreachable
		}
	case KindHostKey:
		return RunbookHostKeyChanged
	case KindSSHAuth:
		return RunbookSSHAuthFailed
	case KindSudo:
		return RunbookSSHSudoMissing
	case KindKubeAPI:
		return RunbookKubeAPIUnreachable
	case KindKubeAuth:
		return RunbookKubeAuthFailed
	case KindKubeForbidden:
		return RunbookKubeForbidden
	case KindRancherAPI:
		return RunbookRancherUnreachable
	case KindRancherAuth:
		return RunbookRancherAuthFailed
	case KindRancherForbidden:
		return RunbookRancherForbidden
	case KindCancelled:
		return ""
	}
	return RunbookCheckUnclassified
}

// notConfiguredRunbook is the runbook of a check whose adapter has nothing to run with.
func notConfiguredRunbook(check string) string {
	layer, _, _ := strings.Cut(check, ".")
	switch layer {
	case "ssh":
		return RunbookSSHNotConfigured
	case "kube":
		return RunbookKubeNotConfigured
	case "rancher":
		return RunbookRancherNotConfigured
	}
	return RunbookCheckUnclassified
}

// nextStep is the read-only next step for a failure kind. It never proposes accepting a host
// key, skipping a confirmation or changing a machine.
func nextStep(kind Kind, customer, where string) string {
	access := "ktags cluster access " + customer + " --check"
	switch kind {
	case KindDNS:
		return fmt.Sprintf("check that %s resolves from this laptop (VPN, DNS settings), then re-run: %s", where, access)
	case KindTCP:
		return fmt.Sprintf("check the network path to %s (VPN, Tailscale, firewall), then re-run: %s", where, access)
	case KindHostKey:
		return fmt.Sprintf("do not accept the new key; compare the fingerprint of %s with the customer out of band, then re-run: %s", where, access)
	case KindSSHAuth:
		return "check that your SSH agent holds your team key (ssh-add -l), then re-run: " + access
	case KindSudo:
		return fmt.Sprintf("the SSH user has no passwordless sudo on %s; re-run: %s", where, access)
	case KindKubeAPI:
		return fmt.Sprintf("the Kubernetes API at %s does not answer or its certificate does not chain to ktags_cluster.kube_ca; check the node path first: %s", where, access)
	case KindKubeAuth, KindRancherAuth:
		return fmt.Sprintf("%s rejected the stored credentials of %s; check them: ktags doctor", where, customer)
	case KindKubeForbidden, KindRancherForbidden:
		return fmt.Sprintf("the credentials of %s log in to %s but lack the minimal read permission; ask the cluster owner, then: ktags doctor", customer, where)
	case KindRancherAPI:
		return fmt.Sprintf("Rancher at %s does not answer or its certificate does not chain to ktags_cluster.rancher_ca (or the system trust store without one); open it in a browser on this laptop, then: ktags doctor", where)
	case KindTimeout:
		return fmt.Sprintf("%s did not answer in time; check the network path, then re-run: %s", where, access)
	case KindCancelled:
		return "the check was cancelled before it finished; run it again"
	default:
		return "the failure could not be classified; read the detail, then: ktags doctor"
	}
}
