package inventory

// SchemaVersion is the only ktags.yml schema this build reads and writes.
const SchemaVersion = 1

// Environment classifies a customer for confirmation rules (security.md §5).
type Environment string

// The environments a customer can have.
const (
	EnvProd    Environment = "prod"
	EnvStaging Environment = "staging"
	EnvTest    Environment = "test"
)

// Role is the RKE2 role of a node.
type Role string

// The node roles.
const (
	RoleServer Role = "server"
	RoleAgent  Role = "agent"
)

// Record is the content of group_vars/all/ktags.yml. The top-level keys carry the ktags_ prefix
// because Ansible loads them as ordinary group variables.
type Record struct {
	Schema   int      `yaml:"ktags_schema"`
	Customer Customer `yaml:"ktags_customer"`
	Cluster  Cluster  `yaml:"ktags_cluster"`
}

// Customer is the customer's identity. ID never changes after creation and must equal the name
// of the customer directory; Name is the display name and can change freely.
type Customer struct {
	ID          string      `yaml:"id"`
	Name        string      `yaml:"name"`
	Environment Environment `yaml:"environment"`
}

// Cluster is the one cluster a customer has in the first schema.
type Cluster struct {
	ID         string `yaml:"id"`
	Name       string `yaml:"name"`
	RancherURL string `yaml:"rancher_url"`
	// RancherCA is the public PEM CA the Rancher certificate chains to; empty means the system
	// trust store.
	RancherCA string `yaml:"rancher_ca,omitempty"`
	// KubeAPIURL is the Kubernetes API server; empty means the kube checks are not configured.
	KubeAPIURL string `yaml:"kube_api_url,omitempty"`
	// KubeCA is the public PEM cluster CA; it needs KubeAPIURL. CAs are not secrets
	// (security.md §1); private keys are refused.
	KubeCA string `yaml:"kube_ca,omitempty"`
	Nodes  []Node `yaml:"nodes"`
	Access Access `yaml:"access"`
}

// Node is one machine of the cluster. ID is unique within the cluster and never changes.
type Node struct {
	ID      string `yaml:"id"`
	Name    string `yaml:"name"`
	Role    Role   `yaml:"role"`
	Address string `yaml:"address"`
}

// Access lists the enabled access methods. A method is enabled when its block is present.
type Access struct {
	Direct    *DirectAccess    `yaml:"direct,omitempty"`
	Jump      *JumpAccess      `yaml:"jump,omitempty"`
	Tailscale *TailscaleAccess `yaml:"tailscale,omitempty"`
}

// DirectAccess is SSH straight to each node address.
type DirectAccess struct {
	User string `yaml:"user"`
	Port int    `yaml:"port"`
}

// JumpAccess is SSH to the nodes through one jump host.
type JumpAccess struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	User string `yaml:"user"`
}

// TailscaleAccess is SSH over the customer's tailnet. AuthKeyRef names the secret-store key that
// holds the Tailscale auth key; the key itself never appears here.
type TailscaleAccess struct {
	AuthKeyRef string `yaml:"auth_key_ref"`
}
