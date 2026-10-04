-- No raw IPs, paths, content, credentials or target identities. User-owned rows
-- cascade on account erasure. Histories contain admitted timestamps only.
CREATE TABLE IF NOT EXISTS go_rate_budget (
    scope varchar(64) NOT NULL,
    subject bytea NOT NULL CHECK (octet_length(subject)=32),
    user_id bigint REFERENCES accounts_user(id) ON DELETE CASCADE,
    policy_limit integer NOT NULL CHECK (policy_limit BETWEEN 1 AND 10000),
    window_us bigint NOT NULL CHECK (window_us BETWEEN 1000000 AND 86400000000),
    events timestamptz[] NOT NULL CHECK (cardinality(events) BETWEEN 1 AND 10000),
    expires_at timestamptz NOT NULL,
    PRIMARY KEY(scope,subject)
);
CREATE INDEX IF NOT EXISTS go_rate_budget_expiry ON go_rate_budget(expires_at);
CREATE INDEX IF NOT EXISTS go_rate_budget_user ON go_rate_budget(user_id) WHERE user_id IS NOT NULL;

-- Capacity totals are changed after each statement has locked all affected rows.
-- This preserves row-before-counter lock order during FK cascade and pruning.
CREATE TABLE IF NOT EXISTS go_rate_budget_capacity (
    singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
    keys bigint NOT NULL CHECK(keys BETWEEN 0 AND 10000),
    events bigint NOT NULL CHECK(events BETWEEN 0 AND 1000000)
);
INSERT INTO go_rate_budget_capacity(singleton,keys,events)
 SELECT true,count(*),coalesce(sum(cardinality(events)),0) FROM go_rate_budget
 ON CONFLICT(singleton) DO NOTHING;

CREATE OR REPLACE FUNCTION go_count_rate_budgets() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE key_delta bigint := 0; event_delta bigint := 0;
BEGIN
    IF TG_OP='INSERT' THEN
        SELECT count(*),coalesce(sum(cardinality(events)),0) INTO key_delta,event_delta FROM inserted;
    ELSIF TG_OP='DELETE' THEN
        SELECT -count(*),-coalesce(sum(cardinality(events)),0) INTO key_delta,event_delta FROM deleted;
    ELSE
        SELECT coalesce(sum(cardinality(events)),0) INTO event_delta FROM updated_new;
        SELECT event_delta-coalesce(sum(cardinality(events)),0) INTO event_delta FROM updated_old;
    END IF;
    IF key_delta=0 AND event_delta=0 THEN RETURN NULL; END IF;
    UPDATE go_rate_budget_capacity SET keys=keys+key_delta,events=events+event_delta
      WHERE singleton AND keys+key_delta BETWEEN 0 AND 10000 AND events+event_delta BETWEEN 0 AND 1000000;
    IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P5001',MESSAGE='shared budget capacity exhausted'; END IF;
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS go_rate_budget_insert_capacity ON go_rate_budget;
CREATE TRIGGER go_rate_budget_insert_capacity AFTER INSERT ON go_rate_budget
 REFERENCING NEW TABLE AS inserted FOR EACH STATEMENT EXECUTE FUNCTION go_count_rate_budgets();
DROP TRIGGER IF EXISTS go_rate_budget_update_capacity ON go_rate_budget;
CREATE TRIGGER go_rate_budget_update_capacity AFTER UPDATE ON go_rate_budget
 REFERENCING NEW TABLE AS updated_new OLD TABLE AS updated_old FOR EACH STATEMENT EXECUTE FUNCTION go_count_rate_budgets();
DROP TRIGGER IF EXISTS go_rate_budget_delete_capacity ON go_rate_budget;
CREATE TRIGGER go_rate_budget_delete_capacity AFTER DELETE ON go_rate_budget
 REFERENCING OLD TABLE AS deleted FOR EACH STATEMENT EXECUTE FUNCTION go_count_rate_budgets();

CREATE OR REPLACE FUNCTION go_prune_rate_budgets(batch integer) RETURNS bigint LANGUAGE plpgsql AS $$
DECLARE victims tid[]; removed bigint; sweep_at timestamptz := clock_timestamp();
BEGIN
    IF batch NOT BETWEEN 1 AND 1000 THEN RAISE EXCEPTION 'invalid shared budget prune batch'; END IF;
    -- Materialize/lock every victim before the statement-level counter trigger.
    SELECT array_agg(v.ctid) INTO victims FROM (
        SELECT b.ctid FROM go_rate_budget b WHERE b.expires_at<=sweep_at
        ORDER BY b.expires_at LIMIT batch FOR UPDATE SKIP LOCKED
    ) v;
    DELETE FROM go_rate_budget WHERE ctid=ANY(victims);
    GET DIAGNOSTICS removed=ROW_COUNT;
    RETURN removed;
END $$;

CREATE OR REPLACE FUNCTION go_admit_rate_budget(
    requested_scope text, requested_subject bytea, requested_user bigint,
    requested_limit integer, requested_window_us bigint
) RETURNS TABLE(allowed boolean,retry_us bigint) LANGUAGE plpgsql AS $$
DECLARE
    admitted_at timestamptz;
    window_interval interval;
    previous go_rate_budget%ROWTYPE;
    kept timestamptz[];
BEGIN
    IF requested_scope !~ '^[a-z0-9_.]{1,64}$' OR octet_length(requested_subject)<>32
       OR requested_limit NOT BETWEEN 1 AND 10000 OR requested_window_us NOT BETWEEN 1000000 AND 86400000000 THEN
        RAISE EXCEPTION 'invalid shared rate policy';
    END IF;
    IF requested_user IS NOT NULL THEN
        PERFORM id FROM accounts_user WHERE id=requested_user FOR KEY SHARE;
        IF NOT FOUND THEN RAISE EXCEPTION 'invalid shared rate actor'; END IF;
    END IF;
    -- Dedicated namespace, never the audit/global mutation lock. Per-key
    -- serialization protects missing buckets as well as existing row histories.
    PERFORM pg_advisory_xact_lock(hashtextextended(requested_scope||encode(requested_subject,'hex'),904271013));
    admitted_at := clock_timestamp();
    window_interval := requested_window_us * interval '1 microsecond';
    SELECT * INTO previous FROM go_rate_budget
      WHERE scope=requested_scope AND subject=requested_subject FOR UPDATE;
    IF FOUND AND previous.expires_at>admitted_at THEN
        admitted_at := greatest(admitted_at,previous.events[cardinality(previous.events)]);
        IF previous.window_us<>requested_window_us OR previous.policy_limit<>requested_limit
           OR previous.user_id IS DISTINCT FROM requested_user THEN
            RETURN QUERY SELECT false,greatest(1000000::bigint,(extract(epoch FROM previous.expires_at-admitted_at)*1000000)::bigint);
            RETURN;
        END IF;
        SELECT coalesce(array_agg(e ORDER BY e),'{}'::timestamptz[]) INTO kept
          FROM unnest(previous.events) e WHERE e>admitted_at-window_interval;
        IF cardinality(kept)>=requested_limit THEN
            RETURN QUERY SELECT false,greatest(1000000::bigint,(extract(epoch FROM kept[1]+window_interval-admitted_at)*1000000)::bigint);
            RETURN;
        END IF;
    ELSE
        kept := '{}'::timestamptz[];
    END IF;
    BEGIN
        INSERT INTO go_rate_budget(scope,subject,user_id,policy_limit,window_us,events,expires_at)
          VALUES(requested_scope,requested_subject,requested_user,requested_limit,requested_window_us,array_append(kept,admitted_at),admitted_at+window_interval)
          ON CONFLICT(scope,subject) DO UPDATE SET events=EXCLUDED.events,expires_at=EXCLUDED.expires_at,
              policy_limit=EXCLUDED.policy_limit,window_us=EXCLUDED.window_us,user_id=EXCLUDED.user_id;
    EXCEPTION WHEN SQLSTATE 'P5001' THEN
        RETURN QUERY SELECT false,1000000::bigint;
        RETURN;
    END;
    RETURN QUERY SELECT true,0::bigint;
END $$;
