package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

const exampleCluster = `apiVersion: hcm/v1
kind: Cluster

cluster:
  name: lab
  domain: hpc.lab.local
  timezone: Asia/Dubai

control_plane:
  appliance_host: headnode01
  mgmt_interface: eno1

network:
  provision_subnet: 10.1.1.0/24
  dhcp_range: [10.1.1.100, 10.1.1.250]
  dns_forwarders: [8.8.8.8]
  tftp_root: /var/lib/tftpboot

# Azure is optional. enabled:false = pure on-prem, never phones home.
cloud:
  enabled: false
  provider: azure
  location: uaenorth
  auth:
    ref: secret://azure-sp
  gallery: ""

identity:
  type: local            # freeipa-client | ad | ldap | entra | local
  client_config:
    ref: ""

scheduler:
  type: slurm
  accounting: true

storage:
  - name: home
    mount: /home
    type: nfs
    server: headnode01
    export: /export/home

images:
  - name: alma-cpu
    parity: strict
    base:
      azure: {distro: almalinux, sku: 8-hpc-gen2}
      onprem: {distro: almalinux, version: "8.9"}
    payload_roles: [base, ofed, openmpi, monitoring, sssd]
  - name: alma-gpu
    parity: strict
    base:
      azure: {distro: almalinux, sku: 8-hpc-gen2}
      onprem: {distro: almalinux, version: "8.9"}
    payload_roles: [base, ofed, cuda, openmpi, monitoring, sssd]

partitions:
  - name: cpu
    image: alma-cpu
    target: onprem
    node_group: compute
    gpu: false
  - name: gpu
    image: alma-gpu
    target: onprem
    node_group: gpu
    gpu: true

nodes_file: ./nodes.conf
`

const exampleNodes = `# hostname   boot_mac            boot_if   bmc_ip          group     status
node01       aa:bb:cc:dd:ee:01   eno1      10.1.2.101      compute   active
node02       aa:bb:cc:dd:ee:02   eno1      10.1.2.102      compute   active
gpu01        aa:bb:cc:dd:ee:11   eno1      10.1.2.111      gpu       active
`

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	dir := fs.String("dir", ".", "directory to scaffold into")
	fs.Parse(args)

	files := map[string]string{
		"cluster.yaml": exampleCluster,
		"nodes.conf":   exampleNodes,
	}
	for name, content := range files {
		p := filepath.Join(*dir, name)
		if _, err := os.Stat(p); err == nil {
			fmt.Printf("skip %s (already exists)\n", p)
			continue
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return err
		}
		fmt.Printf("wrote %s\n", p)
	}
	fmt.Println("\nNext: hcm validate  ->  hcm check pending  ->  hcm apply")
	return nil
}
