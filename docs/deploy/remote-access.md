# Remote access

Bowtie listens on `:8400` by default and expects a reverse proxy or VPN for TLS
and remote reachability. Below are three complete, copy-pasteable setups that
expose Bowtie at a domain (or private mesh) with TLS.

Prerequisites common to all options:

- Bowtie is running on the LAN (e.g. `docker compose up -d` from `deploy/`).
- You can reach `http://<bowtie-host>:8400/healthz` from the machine that will
  run the proxy / tunnel / Tailscale node.

---

## 1. Caddy reverse proxy (public domain + automatic HTTPS)

Caddy obtains and renews Let's Encrypt certificates automatically.

**Install Caddy** on a host that can reach Bowtie and has ports 80/443 open to
the internet (same box as Bowtie is fine).

`/etc/caddy/Caddyfile`:

```caddy
tv.example.com {
	# Replace 127.0.0.1 with the Bowtie host if Caddy runs elsewhere on the LAN.
	reverse_proxy 127.0.0.1:8400

	# HLS playlists and segments should not be buffered.
	flush_interval -1
}
```

Reload:

```bash
sudo caddy validate --config /etc/caddy/Caddyfile
sudo systemctl reload caddy
```

Point DNS for `tv.example.com` at this host (A/AAAA). Open
`https://tv.example.com`, log in with the first-run admin password from the
Bowtie container logs, and change it immediately.

Optional: restrict admin paths to a private network:

```caddy
tv.example.com {
	@admin path /api/v1/admin/*
	handle @admin {
		# Example: only allow your home LAN + Tailscale CGNAT range.
		@allowed remote_ip 192.168.0.0/16 10.0.0.0/8 100.64.0.0/10
		handle @allowed {
			reverse_proxy 127.0.0.1:8400
		}
		respond "Forbidden" 403
	}
	handle {
		reverse_proxy 127.0.0.1:8400
	}
	flush_interval -1
}
```

---

## 2. Cloudflare Tunnel (no open inbound ports)

Cloudflare Tunnel (`cloudflared`) creates an outbound connection to Cloudflare
so you never open 80/443 on the home router.

1. Create a tunnel in the [Cloudflare Zero Trust dashboard](https://one.dash.cloudflare.com/)
   (Networks → Tunnels → Create a tunnel → **Cloudflared**).
2. Install and authenticate the connector on the Bowtie host (or any LAN host
   that can reach `:8400`):

```bash
# Debian/Ubuntu package install (see Cloudflare docs for other OS):
# https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/
sudo cloudflared service install <TUNNEL_TOKEN_FROM_DASHBOARD>
```

3. In the tunnel **Public Hostname** config:

| Field        | Value                          |
|--------------|--------------------------------|
| Subdomain    | `tv`                           |
| Domain       | `example.com`                  |
| Type         | HTTP                           |
| URL          | `http://127.0.0.1:8400`        |

If `cloudflared` runs on a different machine than Bowtie, set URL to
`http://<bowtie-lan-ip>:8400`.

Equivalent local config file (alternative to the dashboard token service):

`~/.cloudflared/config.yml`:

```yaml
tunnel: <TUNNEL_UUID>
credentials-file: /home/you/.cloudflared/<TUNNEL_UUID>.json

ingress:
  - hostname: tv.example.com
    service: http://127.0.0.1:8400
    originRequest:
      noHappyEyeballs: true
      connectTimeout: 30s
  - service: http_status:404
```

```bash
cloudflared tunnel route dns <TUNNEL_UUID> tv.example.com
cloudflared tunnel run <TUNNEL_UUID>
```

Visit `https://tv.example.com`. TLS is terminated at Cloudflare.

> **Note:** Free Cloudflare plans buffer some responses; if HLS stutters, try
> Caddy or Tailscale, or enable Cloudflare Stream path exclusions is not
> applicable here — prefer a tunnel hostname with caching disabled (default for
> non-static origins).

---

## 3. Tailscale (private mesh, optional HTTPS)

Tailscale is the simplest option when only your household/devices should watch
— no public domain required.

### Basic (HTTP over the tailnet)

1. Install Tailscale on the Bowtie host and on each client device:
   <https://tailscale.com/download>
2. `sudo tailscale up` on each machine; approve them in the admin console.
3. Open `http://<bowtie-magicdns-name>:8400` or `http://100.x.y.z:8400`.

Find the MagicDNS name:

```bash
tailscale status
# e.g. bowtie-nas.tail-scale.ts.net
```

### HTTPS with Tailscale Serve (recommended)

On the Bowtie host (Tailscale v1.44+):

```bash
# Expose Bowtie to your tailnet with a Tailscale-issued HTTPS cert.
sudo tailscale serve --bg --https=443 http://127.0.0.1:8400
```

Clients open:

```text
https://<machine-name>.<tailnet>.ts.net/
```

Disable later with:

```bash
sudo tailscale serve reset
```

### Optional: Funnel (public HTTPS via Tailscale)

If you want a public URL without managing DNS yourself:

```bash
sudo tailscale funnel --bg --https=443 http://127.0.0.1:8400
```

Requires Funnel to be enabled in the Tailscale admin console. Prefer Serve
(private) for family use; Funnel is public to the internet.

---

## Checklist after exposing Bowtie

1. `curl -sf https://<your-host>/healthz` → `ok`
2. Log in as `admin` (password from first-run container logs) and **change the password**
3. Admin → Tuners: add HDHomeRun by IP if UDP discovery is unavailable remotely
4. Enable channels, map EPG if configured, start a stream from the guide
