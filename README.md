# Random Scripts

One-off ops, migration, DNS, and repro tools for SpareEye / ShelfTalker. Not part of the product runtime.

Keep this folder as a **sibling** of `Prototype-Backend/` and `spareeye-prototype-store-ui/` under `Code/` so relative paths in the Go scripts keep working.

## Shell scripts

| Script | Purpose |
|--------|---------|
| `subdomain-phase2-dns.sh` | Map prod hostnames (`www`, `app`, `admin`) to Cloud Run |
| `test-domain-mappings.sh` | Map test hostnames (`www-test`, `app-test`, `admin-test`) to the test Cloud Run service |

Both need `gcloud` auth, `GOOGLE_CLOUD_PROJECT`, and DNS ready at your registrar.

```bash
export GOOGLE_CLOUD_PROJECT=spareeye-prototype-project
export CLOUD_RUN_REGION=europe-west1
./test-domain-mappings.sh
```

**Deploy scripts** live in `Prototype-Backend/scripts/` (`deploy-prod.sh`, `deploy-test.sh`), not here.

## Go one-offs (Firestore migrations)

Run from `Prototype-Backend/` so dependencies come from its `go.mod`. All scripts default to **dry-run**; pass `-apply` to write.

| Script | Purpose |
|--------|---------|
| `setup_organizations.go` | Create org docs and link admins |
| `backfill_admin_emails.go` | Migrate admin doc IDs to email-shaped IDs |
| `migrate_organization_to_uuid.go` | Move a legacy slug org id to a UUID |
| `migrate_qr_target_url.go` | Set `qrCode.targetUrl` on surfaces |

```bash
cd Prototype-Backend
export GOOGLE_CLOUD_PROJECT=spareeye-prototype-project

# dry-run
go run ../Random-Scripts/migrate_qr_target_url.go

# apply
go run ../Random-Scripts/migrate_qr_target_url.go -apply
```

Requires Application Default Credentials (`gcloud auth application-default login`).

## Node / Playwright repro scripts

Used for A/B testing and bug reproduction against live or local surfaces.

| Script | Purpose |
|--------|---------|
| `ab-kindly-bcomplex.mjs` | A/B local vs production; saves captures on failure |
| `repro-lg-ac-bug.mjs` | Repro for LG AC bug |
| `repro-local-lg-ac.mjs` | Local repro variant |
| `repro-lg-ac-mcp-script.js` | MCP helper for LG AC repro |

```bash
cd Random-Scripts
npm install playwright
node ab-kindly-bcomplex.mjs [surfaceId] [prompt]
```

Captures go to `ab-captures/<surfaceId>/` (gitignored).

## Other utilities

- `google_image_finder.py` — image search helper
- `ImageSelectorTool.html` — local image picker UI
- `*.tsv` — ad-hoc data files for one-off tasks
