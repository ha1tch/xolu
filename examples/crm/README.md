# examples/crm — a medium-sized CRM dataset

Two scripts: one launches a correctly-configured xolu instance, the other seeds it with a realistic, cross-referenced CRM data model (users, companies, contacts, deals, activities, tasks — six entity types, all linked via REF fields). Useful as a demo, a smoke test after a build, or a starting point for testing against real-shaped multi-entity data instead of a single flat table.

## Prerequisites

- Go toolchain (the launcher builds `xolu` and `iolu` from source)
- `python3` with `requests` installed (`pip install requests`)

## Quick start

```bash
cd examples/crm
./launch_xolu_for_crm.sh --daemon
# ... wait for "ready", then in another shell or after backgrounding:
python3 xolu_crm_seed.py --tenant acme_crm --skip-tenant-create
```

The launcher prints the exact follow-up command (including `--api-key` if you started it with `--with-auth`) once the server is ready — copy that instead of retyping the above by hand if you changed any settings.

## What `launch_xolu_for_crm.sh` actually does

Builds `cmd/xolu` and `cmd/iolu`, provisions a tenant via `iolu tenant create --mode shared` **before** starting the server (required under `TenantMode=strict` — the server's own tenant registry loads once at boot and never re-reads it), then starts `xolu` configured with:

- `GraphMode=flat` — REF fields (used throughout this schema: company owners, contact-to-company links, deal-to-contact links) need graph support on
- `APIV2Enabled=true` — otherwise every single entity write logs a spurious warning about a missing `event_defs` table (a real, currently-unfixed xolu gap, filed as T-148)
- `TenantMode=strict`, `AuthType=none` by default (`--with-auth KEY` switches to `AuthType=apikey`)

Every setting is a plain shell variable with an `XOLU_CRM_*` environment override — see the top of the script. Data lands in `examples/crm/xolu-crm-data/` by default (`XOLU_CRM_BASE_DIR` to change it), gitignored, safe to delete between runs.

## What `xolu_crm_seed.py` actually does

Registers six schemas (global, not tenant-scoped — confirmed directly against the server's own routing), then creates entities in dependency order so every REF field points at something that genuinely exists by the time it's written: users → companies → contacts → deals → activities/tasks. Deals reference a contact from the *same* company as the deal, not just any random contact.

`--scale` multiplies the default volumes (5 users, 15 companies, ~35 contacts, 25 deals, 50 activities, 20 tasks) up or down. `--seed` for a reproducible run.

## Why two separate scripts, not one

The launcher is infrastructure (build, configure, boot) — bash is the natural fit. The seed script is data assembly (schema definitions, realistic name pools, cross-referenced entity generation) — Python is the natural fit there. Keeping them separate also means you can point the seed script at any already-running xolu instance, launcher-provisioned or not, as long as the tenant exists and the server config matches what the seed script expects (see `xolu_crm_seed.py --help` for the full prerequisites if you're running it standalone).

## Known gap

The seed script's own `iolu tenant create` fallback (for running it against a server the launcher didn't start) only works correctly if the tenant is provisioned *before* that server's own most recent boot — the same `TenantMode=strict` registry-loads-once-at-startup constraint the launcher itself works around by sequencing correctly. If you hit `"Unknown tenant"` after the seed script's own iolu step reported success, restart the server and re-run with `--skip-tenant-create`, or just use the launcher, which gets the ordering right by construction.
