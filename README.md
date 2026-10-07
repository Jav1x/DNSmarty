# DNSmarty

[Русский](README.ru.md)

Self-hosted Smart DNS. One panel writes PostgreSQL and pushes config to DNS and proxy agents. Agents answer clients. The panel does not.

One Go image (`panel`, `dns`, `proxy`, `migrate`) and a React UI. No third-party domain lists: the only seeded name is `example.com`.

## Features

- Split control plane and data plane. The panel, DNS nodes, and proxies can share a machine or run apart.
- DNS on 53, DoT on 853, and DoH on 8443 share one decision path.
- SNI proxy splices TCP and does not terminate TLS. No UDP or QUIC.
- Several proxies, dropped from rotation when the panel cannot reach them for 30 seconds.
- Clients outside the allowlist get `REFUSED`. An empty allowlist refuses everyone except the bootstrap CIDR.
- Node keys are shown once and stored only as ciphertext.

## Requirements

- Linux for DNS and proxy (`network_mode: host`, so the allowlist sees the real client IP)
- Docker with Compose v2
- Docker image `ghcr.io/jav1x/dnsmarty`, or a local image build

## Quick start

```bash
bash <(curl -Ls https://raw.githubusercontent.com/Jav1x/DNSmarty/main/scripts/dnsmarty.sh) @ install
```

The installer asks for a language (`en` or `ru`), generates `.env` in `/opt/dnsmarty`, and prints the admin password once. The panel is at `https://127.0.0.1:7443` (Caddy's internal certificate; the browser will warn). Command `dnsmarty` opens the menu.

| Command | What it does |
| --- | --- |
| `install` | Create `.env`, pull images, start Postgres, migrate, panel, and Caddy |
| `update` | Pull images and recreate containers |
| `up` / `down` / `restart` / `status` / `logs` | Compose controls |
| `edit-env` | Edit `/opt/dnsmarty/.env` |
| `uninstall` | Stop containers and keep `/opt/dnsmarty` |
| `install-script` | Install only the `dnsmarty` command |

## Agents

In the panel: Nodes → issue a key. Set the public IP (used in DNS answers; it is not detected from the container) and the address and port the panel will dial.

On the agent machine:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/Jav1x/DNSmarty/main/scripts/dnsmarty-node.sh) @ install
```

Or without the menu:

```bash
dnsmarty-node install --role dns --key KEY --port 9443
dnsmarty-node install --role proxy --key KEY --port 9444
```

`sh install-node.sh dns` does the same and asks for the key and port. Press Next in the panel. A closed port or a wrong key stays on the node card as an error. A successful check pushes the snapshot.

DNS listens on 53, 853, and DoH 8443. The proxy listens on 80 and 443. The management port is separate. On one machine they are two containers.

Files live in `/opt/dnsmarty-dns` or `/opt/dnsmarty-proxy`. `dnsmarty-node update` pulls the image and recreates the container.

## Clients and names

Add the CIDR you will test from. An empty list refuses everyone except the bootstrap CIDR.

Set an upstream recursive resolver, for example `1.1.1.1:53`. DNS and the proxy query it directly, not through their own port 53.

`example.com` is already seeded as a suffix. Attach a proxy and a weight. Round robin and weighted return one or two A records. Sticky `/24` returns one.

From an allowed address:

```bash
dig @DNS_PUBLIC_IPV4 www.example.com A
```

The A record is the live proxy's public address, TTL 30 by default. Unknown names are forwarded. Other clients get `REFUSED` on 53, DoT, and DoH.

If the panel cannot reach a proxy for 30 seconds, that IP leaves the next DNS snapshot. The name is not forwarded upstream. If the panel is down, the agent keeps the last snapshot.

## Develop from this tree

```bash
cp .env.example .env
# replace every change-me

docker compose --profile panel up -d --build
```

`web/` is the UI. `npm run dev` proxies the API on `127.0.0.1:8080`. The image build runs `npm run build` itself. Do not commit `web/node_modules` or `internal/panel/dist/assets`.

```bash
go test ./...
```

Docker is required for the Postgres tests.

## Security

Secrets stay in `.env` or Docker secrets. They are not copied into the image. `.env.example` contains only placeholders. Node keys in Postgres are AES-GCM ciphertext under `PANEL_MASTER_KEY`. Do not publish a `.env`, a private key, or `certs/*.pem`.
