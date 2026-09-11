package connect

import "fmt"

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
		return fmt.Sprintf("the Kubernetes API at %s does not answer; check the node path first: %s", where, access)
	case KindKubeAuth, KindRancherAuth:
		return fmt.Sprintf("%s rejected the stored credentials of %s; check them: ktags doctor", where, customer)
	case KindKubeForbidden, KindRancherForbidden:
		return fmt.Sprintf("the credentials of %s log in to %s but lack the minimal read permission; ask the cluster owner, then: ktags doctor", customer, where)
	case KindRancherAPI:
		return fmt.Sprintf("Rancher at %s does not answer; open it in a browser on this laptop, then: ktags doctor", where)
	case KindTimeout:
		return fmt.Sprintf("%s did not answer in time; check the network path, then re-run: %s", where, access)
	case KindCancelled:
		return "the check was cancelled before it finished; run it again"
	default:
		return "the failure could not be classified; read the detail, then: ktags doctor"
	}
}
