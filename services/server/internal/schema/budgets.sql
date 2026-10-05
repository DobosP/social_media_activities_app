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
-- The family census below must be exact, so no other writer may change
-- histories or counters until this migration commits (replacing the triggers
-- needs this lock anyway). Taking it first, before the index statements' SHARE
-- lock, avoids a lock upgrade against admissions already holding ROW SHARE.
LOCK TABLE go_rate_budget IN ACCESS EXCLUSIVE MODE;
CREATE INDEX IF NOT EXISTS go_rate_budget_expiry ON go_rate_budget(expires_at);
CREATE INDEX IF NOT EXISTS go_rate_budget_user ON go_rate_budget(user_id) WHERE user_id IS NOT NULL;

-- v1 single capacity row: unused since v2 (per-family capacity below) and kept,
-- never dropped. Its seed no longer counts rows, so it can never fail adoption.
CREATE TABLE IF NOT EXISTS go_rate_budget_capacity (
    singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
    keys bigint NOT NULL CHECK(keys BETWEEN 0 AND 10000),
    events bigint NOT NULL CHECK(events BETWEEN 0 AND 1000000)
);
INSERT INTO go_rate_budget_capacity(singleton,keys,events) VALUES(true,0,0)
 ON CONFLICT(singleton) DO NOTHING;

-- v2: capacity per scope family (ADR-0037), so saturating one family (e.g.
-- minted anonymous peers) never refuses another family's keys. Scope and user
-- are fixed per key, so a key never changes family.
CREATE OR REPLACE FUNCTION go_rate_budget_family(scope text, user_id bigint) RETURNS text
 LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE WHEN user_id IS NOT NULL THEN 'actor'
                WHEN scope IN ('api.anonymous','api.token') THEN 'anonymous'
                WHEN scope LIKE 'ops.%' THEN 'ops'
                ELSE 'other' END
$$;
-- Capacity totals are changed after each statement has locked all affected rows.
-- This preserves row-before-counter lock order during FK cascade and pruning.
CREATE TABLE IF NOT EXISTS go_rate_budget_family_capacity (
    family text PRIMARY KEY,
    keys bigint NOT NULL,
    events bigint NOT NULL,
    max_keys bigint NOT NULL,
    max_events bigint NOT NULL,
    CHECK(keys BETWEEN 0 AND max_keys),
    CHECK(events BETWEEN 0 AND max_events)
);

-- Counting triggers are absent while counters are rebuilt (table still locked).
DROP TRIGGER IF EXISTS go_rate_budget_insert_capacity ON go_rate_budget;
DROP TRIGGER IF EXISTS go_rate_budget_update_capacity ON go_rate_budget;
DROP TRIGGER IF EXISTS go_rate_budget_delete_capacity ON go_rate_budget;
DROP TRIGGER IF EXISTS go_rate_budget_truncate_capacity ON go_rate_budget;

-- Reviewed limits are (re)set on every run. Counters are zeroed first so a
-- lowered limit never trips the CHECK before the census rebuilds them.
INSERT INTO go_rate_budget_family_capacity(family,keys,events,max_keys,max_events) VALUES
    ('actor',0,0,50000,1000000),
    ('anonymous',0,0,10000,200000),
    ('ops',0,0,100,10000),
    ('other',0,0,1000,100000)
 ON CONFLICT(family) DO UPDATE SET keys=0,events=0,max_keys=EXCLUDED.max_keys,max_events=EXCLUDED.max_events;
-- Adopting v1 (one shared cap) can find a family above its own cap. Apply the
-- admission eviction rule: keep that family's latest-expiring keys and evict
-- its soonest-expiring ones. A database within its caps loses nothing.
DELETE FROM go_rate_budget WHERE ctid=ANY(ARRAY(
    SELECT r.row_id FROM (
        SELECT b.ctid AS row_id,c.max_keys,c.max_events,
               row_number() OVER w AS kept_keys,sum(cardinality(b.events)) OVER w AS kept_events
          FROM go_rate_budget b JOIN go_rate_budget_family_capacity c ON c.family=go_rate_budget_family(b.scope,b.user_id)
        WINDOW w AS (PARTITION BY c.family ORDER BY b.expires_at DESC,b.scope,b.subject ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)
    ) r WHERE r.kept_keys>r.max_keys OR r.kept_events>r.max_events
));
UPDATE go_rate_budget_family_capacity c SET keys=n.keys,events=n.events
  FROM (SELECT go_rate_budget_family(scope,user_id) AS family,count(*) AS keys,sum(cardinality(events)) AS events
          FROM go_rate_budget GROUP BY 1) n
 WHERE c.family=n.family;

CREATE OR REPLACE FUNCTION go_count_rate_budgets() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE families text[]; key_deltas bigint[]; event_deltas bigint[];
BEGIN
    -- Deltas are grouped by family. An UPDATE nets its old and new rows per
    -- family, so it would stay exact even if a key ever changed family.
    IF TG_OP='INSERT' THEN
        SELECT array_agg(d.family ORDER BY d.family),array_agg(d.key_delta ORDER BY d.family),array_agg(d.event_delta ORDER BY d.family)
          INTO families,key_deltas,event_deltas
          FROM (SELECT go_rate_budget_family(scope,user_id) AS family,count(*) AS key_delta,sum(cardinality(events)) AS event_delta
                  FROM inserted GROUP BY 1) d;
    ELSIF TG_OP='DELETE' THEN
        SELECT array_agg(d.family ORDER BY d.family),array_agg(d.key_delta ORDER BY d.family),array_agg(d.event_delta ORDER BY d.family)
          INTO families,key_deltas,event_deltas
          FROM (SELECT go_rate_budget_family(scope,user_id) AS family,-count(*) AS key_delta,-sum(cardinality(events)) AS event_delta
                  FROM deleted GROUP BY 1) d;
    ELSE
        SELECT array_agg(d.family ORDER BY d.family),array_agg(d.key_delta ORDER BY d.family),array_agg(d.event_delta ORDER BY d.family)
          INTO families,key_deltas,event_deltas
          FROM (SELECT u.family,sum(u.key_delta) AS key_delta,sum(u.event_delta) AS event_delta FROM (
                    SELECT go_rate_budget_family(scope,user_id) AS family,1 AS key_delta,cardinality(events) AS event_delta FROM updated_new
                    UNION ALL
                    SELECT go_rate_budget_family(scope,user_id),-1,-cardinality(events) FROM updated_old
                ) u GROUP BY u.family) d;
    END IF;
    -- Family counters are taken in name order, so concurrent multi-family
    -- statements (pruning) cannot deadlock on them.
    FOR i IN 1..coalesce(cardinality(families),0) LOOP
        CONTINUE WHEN key_deltas[i]=0 AND event_deltas[i]=0;
        UPDATE go_rate_budget_family_capacity SET keys=keys+key_deltas[i],events=events+event_deltas[i]
          WHERE family=families[i] AND keys+key_deltas[i] BETWEEN 0 AND max_keys AND events+event_deltas[i] BETWEEN 0 AND max_events;
        IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='P5001',MESSAGE='shared budget capacity exhausted'; END IF;
    END LOOP;
    RETURN NULL;
END $$;
-- TRUNCATE holds ACCESS EXCLUSIVE on the histories, so zeroing is exact.
CREATE OR REPLACE FUNCTION go_reset_rate_budget_capacity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE go_rate_budget_family_capacity SET keys=0,events=0;
    RETURN NULL;
END $$;
CREATE TRIGGER go_rate_budget_insert_capacity AFTER INSERT ON go_rate_budget
 REFERENCING NEW TABLE AS inserted FOR EACH STATEMENT EXECUTE FUNCTION go_count_rate_budgets();
CREATE TRIGGER go_rate_budget_update_capacity AFTER UPDATE ON go_rate_budget
 REFERENCING NEW TABLE AS updated_new OLD TABLE AS updated_old FOR EACH STATEMENT EXECUTE FUNCTION go_count_rate_budgets();
CREATE TRIGGER go_rate_budget_delete_capacity AFTER DELETE ON go_rate_budget
 REFERENCING OLD TABLE AS deleted FOR EACH STATEMENT EXECUTE FUNCTION go_count_rate_budgets();
CREATE TRIGGER go_rate_budget_truncate_capacity AFTER TRUNCATE ON go_rate_budget
 FOR EACH STATEMENT EXECUTE FUNCTION go_reset_rate_budget_capacity();

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
    victims tid[];
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
    FOR attempt IN 1..2 LOOP
        BEGIN
            INSERT INTO go_rate_budget(scope,subject,user_id,policy_limit,window_us,events,expires_at)
              VALUES(requested_scope,requested_subject,requested_user,requested_limit,requested_window_us,array_append(kept,admitted_at),admitted_at+window_interval)
              ON CONFLICT(scope,subject) DO UPDATE SET events=EXCLUDED.events,expires_at=EXCLUDED.expires_at,
                  policy_limit=EXCLUDED.policy_limit,window_us=EXCLUDED.window_us,user_id=EXCLUDED.user_id;
            RETURN QUERY SELECT true,0::bigint;
            RETURN;
        EXCEPTION WHEN SQLSTATE 'P5001' THEN
            -- Rolling back to the block's savepoint discarded the write and its
            -- counter delta. The eviction below runs after the block, in the
            -- admission's own transaction, so it persists and the retry gets
            -- a fresh savepoint of its own.
            NULL;
        END;
        EXIT WHEN attempt=2;
        -- Never refuse a key because its family is full (owner rule, ADR-0037):
        -- evict up to 64 of the same family's soonest-expiring keys (expired
        -- first), never this key and never another family. Victims are locked
        -- before the DELETE's statement trigger takes the family counter (row
        -- before counter), and SKIP LOCKED never waits on another admission.
        -- While this transaction then holds the counter, the retry writes only
        -- this key's row, which the advisory lock above already serializes.
        SELECT array_agg(v.ctid) INTO victims FROM (
            SELECT b.ctid FROM go_rate_budget b
             WHERE go_rate_budget_family(b.scope,b.user_id)=go_rate_budget_family(requested_scope,requested_user)
               AND NOT (b.scope=requested_scope AND b.subject=requested_subject)
             ORDER BY b.expires_at LIMIT 64 FOR UPDATE SKIP LOCKED
        ) v;
        EXIT WHEN victims IS NULL;
        DELETE FROM go_rate_budget WHERE ctid=ANY(victims);
    END LOOP;
    -- Nothing evictable: every other key of the family is held by a
    -- concurrent admission.
    RETURN QUERY SELECT false,1000000::bigint;
END $$;
