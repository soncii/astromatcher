# Astro Matcher

A mobile-first, free astrology-inspired compatibility web app with a Go backend and an anonymous traffic analytics dashboard.

## Quick start

```bash
cp .env.example .env
# Change ADMIN_PASSWORD and ANALYTICS_SECRET in .env
go run .
```

Open `http://localhost:8080` and `http://localhost:8080/admin` (private analytics dashboard).

The server has **zero third-party Go dependencies**. Frontend is built with vanilla JavaScript and CSS and is a Progressive Web App (PWA). `go test ./...` and `go vet ./...` check the backend.

### Included features

- Two-person birth date form, deterministic Sun-sign-only entertainment compatibility reading.
- Rich mobile-first UI, results, share links containing **signs only**, saved local results, no signup or payments.
- Anonymous analytics: timestamp, source and UTM campaign, estimated country, page views, sessions, completed readings, sharing, saving, date trends and CSV export.
- Local JSONL storage for self-hosting or free Supabase Postgres using standard Go HTTP requests.
- Country geolocation using Cloudflare's `CF-IPCountry` or the optional free country.is fallback. Visitor IP and birth details are not persisted by the app.
- `Dockerfile`, `render.yaml`, PWA icons and social preview image.

## Free hosting

Use a **Render Free** Docker Web Service and **Supabase Free** database for durable analytics. Render may sleep after 15 idle minutes and local files disappear after restart, so don't use local JSONL storage on the free Render host.

1. Create a free Supabase project and execute `docs/supabase.sql` in its SQL Editor.
2. Copy the project URL and a backend-only `sb_secret_...` API key from Supabase Project Settings.
3. Push this repo to GitHub without `.env`, deploy it as a Docker Web Service on Render with the Free instance or use the included `render.yaml` Blueprint.
4. Set `ADMIN_PASSWORD`, `ANALYTICS_SECRET` (32+ chars), `SUPABASE_URL`, `SUPABASE_SECRET_KEY`, `TRUST_PROXY_GEO=1` and `COUNTRY_LOOKUP=on` as Render environment variables.
5. Visit your Render URL, then `/admin` to see analytics.

Cloudflare with custom domain is optional; if used, enable **Network → IP Geolocation**. Geo lookups without Cloudflare may send the visitor IP to the country.is public API. No raw IPs are stored by Astro Matcher.

### Attribution examples

```
https://your-site.onrender.com/?utm_source=tiktok&utm_campaign=video_001
https://your-site.onrender.com/?utm_source=youtube&utm_campaign=short_002
```

Campaign/referrer first-touch values are kept in browser `sessionStorage`. The dashboard counts **sessions, not exact unique individuals**, and country geolocation is approximate. No payment processing or accounts are implemented.

## Other deployment

```bash
docker build -t astromatcher .
docker run --rm -p 8080:10000 -v astro-data:/app/data \
  -e ADMIN_PASSWORD='change-me' \
  -e ANALYTICS_SECRET='change-me-to-a-long-random-value' astromatcher
```

## Important notes

This beta uses approximate tropical Sun-sign date ranges and **illustrative deterministic scores**, not an ephemeris, natal chart, or scientifically validated relationship assessment. Full astrological chart features should be implemented with an appropriate open-source/commercial-compatible licensed ephemeris.

Additional privacy and limitations are documented in the [Russian README](README.ru.md) and on the `/privacy` page.
