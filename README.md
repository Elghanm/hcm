# hcm — hybrid cluster manager (v0 scaffold)

A single Go binary that manages HPC clusters across on-prem bare metal and
Azure from one declarative model. This v0 implements the **provisioning core**:
it turns a `cluster.yaml` + `nodes.conf` into real dnsmasq / chrony / NFS
exports / per-node iPXE / SLURM-partition configs, with a `plan → apply`
reconcile loop.

## Build

Requires Go 1.22+. On a networked machine:

```
go build -o hcm .
```

(This repo vendors `yaml.v3` under `third_party/` via a filesystem `replace`
only because the build sandbox blocked the module proxy. On a normal machine
you can drop the `replace` line in `go.mod` and run `go mod tidy` instead.)

## Try it

```
./hcm init            # scaffold example cluster.yaml + nodes.conf
./hcm validate        # pre-flight: parity, refs, cloud sanity
./hcm check pending   # diff desired state vs last applied
./hcm apply           # write configs under ./hcm-out (add --reload on a real host)
```

Generated files land under `--root` (default `./hcm-out`) so you can inspect
them safely on a test VM without touching the real `/etc`. Point `--root /` on
the actual headnode and add `--reload` to bounce daemons.

### Node lifecycle

```
./hcm node add --hostname node03 --mac aa:bb:cc:dd:ee:03 --bmc 10.1.2.103 --group compute
./hcm node discover --hostname gpu09 --mac aa:bb:cc:dd:ee:19 --group gpu   # -> staging (pending)
./hcm node list
./hcm node approve gpu09           # promote staged node into the inventory
```

Manual entries land active immediately; discovered nodes wait in
`nodes.conf.staging` until approved. The human-authored inventory is never
touched by discovery.

## How the pieces map

- **`cluster.yaml`** — policy: network, cloud (optional), identity, scheduler,
  storage, images, partitions. Versioned (`apiVersion: hcm/v1`) so it can be
  migrated as the schema evolves. Secrets appear only as `ref:` pointers.
- **`nodes.conf`** — columnar inventory. `boot_mac` is the primary key.
- **partition** — the keystone object binding a node group → an image → a
  scheduler queue, tagged `onprem` or `cloud`.
- **reconcile** (`internal/reconcile`) — pure `Render()` functions produce
  artifacts; `Plan()` diffs them against the embedded store; `Apply()` writes
  and records them. Idempotent by construction.

## Where later features slot in (not in v0)

- **CPU vs GPU / CUDA / OFED** — already modeled: a GPU partition points at an
  image whose `payload_roles` include `cuda`/`ofed`. The control plane *selects*
  the image and tags the SLURM partition `Gres=gpu`; the drivers themselves are
  baked by the Ansible payload roles at image-build time, not installed here.
- **HA** — `hcm node add-ha` (stub). Peers share an external Postgres backend
  behind a VIP; embedded-store single-binary stays the default for non-HA.
- **`hcm serve`** — the daemon (HTTP API + continuous reconcile loop). The CLI
  already exercises the same `internal/` packages the daemon will.
- **Azure backend** — `cloud.enabled: true` unlocks gallery images + VMSS;
  a `target: cloud` partition is burst capacity on the same image profile.
- **Monitoring / k8s** — future `monitoring:` and `kubernetes:` sections in
  `cluster.yaml`, each emitting their own artifacts through the same loop.

## Layout

```
main.go, init.go, node.go        CLI (stdlib flag dispatch)
internal/config/                 cluster.yaml + nodes.conf types & loaders
internal/store/                  embedded state (JSON now; bbolt/Postgres later)
internal/reconcile/              generators + plan/apply engine
```
