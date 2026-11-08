-- Run this once in the Supabase SQL Editor (Free plan).
-- The server uses a secret key. The browser never connects to this table directly.
CREATE TABLE IF NOT EXISTS public.analytics_events (
  id text PRIMARY KEY,
  created_at timestamptz NOT NULL DEFAULT now(),
  event_type text NOT NULL,
  session_hash text NOT NULL,
  country text NOT NULL DEFAULT 'Unknown',
  source text NOT NULL DEFAULT 'Direct / unknown',
  campaign text NOT NULL DEFAULT '',
  referrer_host text NOT NULL DEFAULT '',
  pathname text NOT NULL DEFAULT '/'
);
CREATE INDEX IF NOT EXISTS analytics_events_created_idx ON public.analytics_events (created_at DESC);
CREATE INDEX IF NOT EXISTS analytics_events_type_idx ON public.analytics_events (event_type);
ALTER TABLE public.analytics_events ENABLE ROW LEVEL SECURITY;
-- No anon/authenticated policies: those roles cannot read or change analytics.
-- The service-only secret key bypasses RLS and must never appear in frontend code.
