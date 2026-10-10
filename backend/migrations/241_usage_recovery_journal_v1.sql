-- Additive journal activation schema, r4 repair branch only.
-- Runner owns the transaction and checksum ledger; incompatible existing objects fail closed.
-- No financial/history backfill, roles, grants or destructive down.
SET LOCAL lock_timeout='500ms';
SET LOCAL statement_timeout='5s';
CREATE TABLE usage_recovery_journal_v1 (
 request_id text NOT NULL CHECK(length(request_id)>0),
 api_key_id bigint NOT NULL CHECK(api_key_id>0),
 request_fingerprint text NOT NULL,
 payload_version smallint NOT NULL DEFAULT 1 CHECK(payload_version=1),
 -- Keep canonical encoded bytes: jsonb rewriting would invalidate application SHA.
 payload_json text NOT NULL CHECK(jsonb_typeof(payload_json::jsonb)='object' AND octet_length(payload_json)<=1048576),
 snapshot_sha256 text NOT NULL CHECK(length(snapshot_sha256)=64),
 billing_result_json text NOT NULL CHECK(jsonb_typeof(billing_result_json::jsonb)='object'),
 billing_result_sha256 text NOT NULL CHECK(length(billing_result_sha256)=64),
 commit_token text NOT NULL CHECK(length(commit_token)=32),
 state text NOT NULL DEFAULT 'pending' CHECK(state IN('pending','done','quarantined')),
 retry_after timestamptz NOT NULL DEFAULT now(),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0),
 last_error_class text,
 created_at timestamptz NOT NULL DEFAULT now(),
 completed_at timestamptz,
 PRIMARY KEY(request_id,api_key_id),
 CHECK((state='done')=(completed_at IS NOT NULL))
);
CREATE INDEX usage_recovery_journal_v1_pending_idx ON usage_recovery_journal_v1(retry_after,created_at) WHERE state='pending';
CREATE INDEX usage_recovery_journal_v1_observe_idx ON usage_recovery_journal_v1(state,created_at) INCLUDE(attempts,last_error_class) WHERE state<>'done';
COMMENT ON TABLE usage_recovery_journal_v1 IS 'sub2api.usage-journal.v1.activation-1';
CREATE FUNCTION usage_journal_v1_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(OLD.request_id,OLD.api_key_id,OLD.request_fingerprint,OLD.payload_version,OLD.payload_json,OLD.snapshot_sha256,OLD.billing_result_json,OLD.billing_result_sha256,OLD.commit_token,OLD.created_at)
 IS DISTINCT FROM ROW(NEW.request_id,NEW.api_key_id,NEW.request_fingerprint,NEW.payload_version,NEW.payload_json,NEW.snapshot_sha256,NEW.billing_result_json,NEW.billing_result_sha256,NEW.commit_token,NEW.created_at) THEN
  RAISE EXCEPTION 'immutable usage journal payload cannot change';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER usage_journal_v1_immutable BEFORE UPDATE ON usage_recovery_journal_v1 FOR EACH ROW EXECUTE FUNCTION usage_journal_v1_immutable();
