package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Cluster mirrors cluster.yaml. It holds POLICY only: services, partitions,
// storage, identity, images. Node inventory lives in a separate nodes file
// (see nodes.go) and is referenced here by path.
type Cluster struct {
	APIVersion   string       `yaml:"apiVersion"`
	Kind         string       `yaml:"kind"`
	Meta         Meta         `yaml:"cluster"`
	ControlPlane ControlPlane `yaml:"control_plane"`
	Network      Network      `yaml:"network"`
	Cloud        Cloud        `yaml:"cloud"`
	Identity     Identity     `yaml:"identity"`
	Scheduler    Scheduler    `yaml:"scheduler"`
	Storage      []Storage    `yaml:"storage"`
	Images       []Image      `yaml:"images"`
	Partitions   []Partition  `yaml:"partitions"`
	NodesFile    string       `yaml:"nodes_file"`

	// dir holds the directory of the loaded file, so NodesFile can be resolved
	// relative to the config.
	dir string `yaml:"-"`
}

type Meta struct {
	Name     string `yaml:"name"`
	Domain   string `yaml:"domain"`
	Timezone string `yaml:"timezone"`
}

type ControlPlane struct {
	ApplianceHost string `yaml:"appliance_host"`
	MgmtInterface string `yaml:"mgmt_interface"`
}

type Network struct {
	ProvisionSubnet string   `yaml:"provision_subnet"`
	DHCPRange       []string `yaml:"dhcp_range"`
	DNSForwarders   []string `yaml:"dns_forwarders"`
	TFTPRoot        string   `yaml:"tftp_root"` // default /var/lib/tftpboot if empty
}

// Cloud is OPTIONAL. Enabled:false (or omitted) = pure on-prem, never phones home.
type Cloud struct {
	Enabled  bool   `yaml:"enabled"`
	Provider string `yaml:"provider"`
	Location string `yaml:"location"`
	Auth     Ref    `yaml:"auth"`    // a reference, never the credential itself
	Gallery  string `yaml:"gallery"`
}

// Identity is neutral: we consume a ready client config, the admin runs the server.
type Identity struct {
	Type         string `yaml:"type"` // freeipa-client | ad | ldap | entra | local
	ClientConfig Ref    `yaml:"client_config"`
}

type Scheduler struct {
	Type       string `yaml:"type"` // slurm | pbspro
	Accounting bool   `yaml:"accounting"`
}

type Storage struct {
	Name   string `yaml:"name"`
	Mount  string `yaml:"mount"`
	Type   string `yaml:"type"` // nfs (v1)
	Server string `yaml:"server"`
	Export string `yaml:"export"`
}

// Image is one PROFILE that emits two artifacts (Azure gallery + on-prem OCI).
// payload_roles are the shared Ansible roles baked into both.
type Image struct {
	Name         string    `yaml:"name"`
	Parity       string    `yaml:"parity"` // strict (default) | loose
	Base         ImageBase `yaml:"base"`
	PayloadRoles []string  `yaml:"payload_roles"`
}

type ImageBase struct {
	Azure  map[string]string `yaml:"azure"`
	Onprem map[string]string `yaml:"onprem"`
}

// Partition is the keystone object: it binds a node group -> an image -> a
// scheduler queue, and declares onprem (static) or cloud (elastic).
type Partition struct {
	Name      string `yaml:"name"`
	Image     string `yaml:"image"`
	Target    string `yaml:"target"`     // onprem | cloud
	NodeGroup string `yaml:"node_group"` // matches the 'group' column in nodes file
	GPU       bool   `yaml:"gpu"`        // drives Gres=gpu in the scheduler config
}

// Ref is a pointer to a secret or file living outside the config. It never
// carries the secret material itself.
type Ref struct {
	Ref string `yaml:"ref"`
}

// Load reads and parses a cluster.yaml.
func Load(path string) (*Cluster, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Cluster
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	abs, _ := filepath.Abs(path)
	c.dir = filepath.Dir(abs)
	return &c, nil
}

// NodesPath resolves the nodes file relative to the config file's directory.
func (c *Cluster) NodesPath() string {
	if c.NodesFile == "" {
		return filepath.Join(c.dir, "nodes.conf")
	}
	if filepath.IsAbs(c.NodesFile) {
		return c.NodesFile
	}
	return filepath.Join(c.dir, c.NodesFile)
}

// ImageByName returns the image profile with the given name, or nil.
func (c *Cluster) ImageByName(name string) *Image {
	for i := range c.Images {
		if c.Images[i].Name == name {
			return &c.Images[i]
		}
	}
	return nil
}

// Validate runs the cheap, catch-it-early checks the wizard would run before
// touching anything. This is where strict image parity is enforced.
func (c *Cluster) Validate() []error {
	var errs []error
	if c.APIVersion == "" {
		errs = append(errs, fmt.Errorf("apiVersion is required (e.g. hcm/v1)"))
	}
	if c.Meta.Name == "" {
		errs = append(errs, fmt.Errorf("cluster.name is required"))
	}
	// Strict-by-default image base parity: cloud and on-prem bases must match
	// distro unless parity is explicitly set to loose.
	for _, img := range c.Images {
		if img.Parity == "loose" {
			continue
		}
		az := img.Base.Azure["distro"]
		if az == "" {
			az = img.Base.Azure["publisher"] // best-effort when publisher encodes the distro
		}
		on := img.Base.Onprem["distro"]
		if az != "" && on != "" && az != on {
			errs = append(errs, fmt.Errorf(
				"image %q: strict parity violated: azure base %q != onprem base %q "+
					"(set parity: loose to override)", img.Name, az, on))
		}
	}
	// Every partition must reference an existing image.
	for _, p := range c.Partitions {
		if c.ImageByName(p.Image) == nil {
			errs = append(errs, fmt.Errorf("partition %q references unknown image %q", p.Name, p.Image))
		}
		if p.Target != "onprem" && p.Target != "cloud" {
			errs = append(errs, fmt.Errorf("partition %q: target must be onprem or cloud", p.Name))
		}
	}
	// Cloud partitions with cloud disabled is a contradiction worth catching.
	if !c.Cloud.Enabled {
		for _, p := range c.Partitions {
			if p.Target == "cloud" {
				errs = append(errs, fmt.Errorf(
					"partition %q targets cloud but cloud.enabled is false", p.Name))
			}
		}
	}
	return errs
}
