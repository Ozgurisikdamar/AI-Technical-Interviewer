-- Application roles (cluster-wide). Created by an administrator before migrations run.
-- Passwords and authentication methods are set by infrastructure, never stored in Git.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ati_control_plane') THEN
    CREATE ROLE ati_control_plane LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION;
  END IF;
  -- Reads only the allowlisted Tier-1 context view (ADR-011, ADR-016).
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ati_tier1_ctx') THEN
    CREATE ROLE ati_tier1_ctx LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION;
  END IF;
  -- Phase 2 evaluator identity: cannot log in until Phase 2, can never write live-interview tables (ADR-015).
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ati_tier2_evaluator') THEN
    CREATE ROLE ati_tier2_evaluator NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION;
  END IF;
END
$$;
