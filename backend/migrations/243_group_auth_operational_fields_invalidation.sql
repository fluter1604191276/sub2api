-- Keep operational API-key auth snapshots fresh after out-of-band group edits.
-- No data defaults or billing rules change; cosmetic name edits remain excluded.
CREATE OR REPLACE FUNCTION enqueue_group_auth_cache_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    target_group_id BIGINT;
BEGIN
    target_group_id := OLD.id;
    IF TG_OP = 'UPDATE'
       AND OLD.platform IS NOT DISTINCT FROM NEW.platform
       AND OLD.is_exclusive IS NOT DISTINCT FROM NEW.is_exclusive
       AND OLD.status IS NOT DISTINCT FROM NEW.status
       AND OLD.subscription_type IS NOT DISTINCT FROM NEW.subscription_type
       AND OLD.rate_multiplier IS NOT DISTINCT FROM NEW.rate_multiplier
       AND OLD.daily_limit_usd IS NOT DISTINCT FROM NEW.daily_limit_usd
       AND OLD.weekly_limit_usd IS NOT DISTINCT FROM NEW.weekly_limit_usd
       AND OLD.monthly_limit_usd IS NOT DISTINCT FROM NEW.monthly_limit_usd
       AND OLD.allow_image_generation IS NOT DISTINCT FROM NEW.allow_image_generation
       AND OLD.allow_batch_image_generation IS NOT DISTINCT FROM NEW.allow_batch_image_generation
       AND OLD.image_rate_independent IS NOT DISTINCT FROM NEW.image_rate_independent
       AND OLD.image_rate_multiplier IS NOT DISTINCT FROM NEW.image_rate_multiplier
       AND OLD.image_price_1k IS NOT DISTINCT FROM NEW.image_price_1k
       AND OLD.image_price_2k IS NOT DISTINCT FROM NEW.image_price_2k
       AND OLD.image_price_4k IS NOT DISTINCT FROM NEW.image_price_4k
       AND OLD.video_rate_independent IS NOT DISTINCT FROM NEW.video_rate_independent
       AND OLD.video_rate_multiplier IS NOT DISTINCT FROM NEW.video_rate_multiplier
       AND OLD.video_price_480p IS NOT DISTINCT FROM NEW.video_price_480p
       AND OLD.video_price_720p IS NOT DISTINCT FROM NEW.video_price_720p
       AND OLD.video_price_1080p IS NOT DISTINCT FROM NEW.video_price_1080p
       AND OLD.video_model_prices IS NOT DISTINCT FROM NEW.video_model_prices
       AND OLD.web_search_price_per_call IS NOT DISTINCT FROM NEW.web_search_price_per_call
       AND OLD.search_price_per_1k IS NOT DISTINCT FROM NEW.search_price_per_1k
       AND OLD.audio_realtime_price_per_min IS NOT DISTINCT FROM NEW.audio_realtime_price_per_min
       AND OLD.audio_tts_price_per_million_chars IS NOT DISTINCT FROM NEW.audio_tts_price_per_million_chars
       AND OLD.audio_stt_price_per_hour IS NOT DISTINCT FROM NEW.audio_stt_price_per_hour
       AND OLD.long_context_pricing_enabled IS NOT DISTINCT FROM NEW.long_context_pricing_enabled
       AND OLD.model_pricing IS NOT DISTINCT FROM NEW.model_pricing
       AND OLD.claude_code_only IS NOT DISTINCT FROM NEW.claude_code_only
       AND OLD.fallback_group_id IS NOT DISTINCT FROM NEW.fallback_group_id
       AND OLD.fallback_group_id_on_invalid_request IS NOT DISTINCT FROM NEW.fallback_group_id_on_invalid_request
       AND OLD.model_routing_enabled IS NOT DISTINCT FROM NEW.model_routing_enabled
       AND OLD.model_routing IS NOT DISTINCT FROM NEW.model_routing
       AND OLD.mcp_xml_inject IS NOT DISTINCT FROM NEW.mcp_xml_inject
       AND OLD.supported_model_scopes IS NOT DISTINCT FROM NEW.supported_model_scopes
       AND OLD.allow_messages_dispatch IS NOT DISTINCT FROM NEW.allow_messages_dispatch
       AND OLD.allow_live IS NOT DISTINCT FROM NEW.allow_live
       AND OLD.force_openai_fast IS NOT DISTINCT FROM NEW.force_openai_fast
       AND OLD.free_openai_fast IS NOT DISTINCT FROM NEW.free_openai_fast
       AND OLD.default_mapped_model IS NOT DISTINCT FROM NEW.default_mapped_model
       AND OLD.messages_dispatch_model_config IS NOT DISTINCT FROM NEW.messages_dispatch_model_config
       AND OLD.model_allowlist IS NOT DISTINCT FROM NEW.model_allowlist
       AND OLD.codex_models_manifest_config IS NOT DISTINCT FROM NEW.codex_models_manifest_config
       AND OLD.rpm_limit IS NOT DISTINCT FROM NEW.rpm_limit
       AND OLD.max_reasoning_effort IS NOT DISTINCT FROM NEW.max_reasoning_effort
       AND OLD.max_reasoning_effort_over_limit IS NOT DISTINCT FROM NEW.max_reasoning_effort_over_limit
       AND OLD.reasoning_effort_mappings IS NOT DISTINCT FROM NEW.reasoning_effort_mappings
       AND OLD.peak_rate_enabled IS NOT DISTINCT FROM NEW.peak_rate_enabled
       AND OLD.peak_start IS NOT DISTINCT FROM NEW.peak_start
       AND OLD.peak_end IS NOT DISTINCT FROM NEW.peak_end
       AND OLD.peak_rate_multiplier IS NOT DISTINCT FROM NEW.peak_rate_multiplier
       AND OLD.profit_control_enabled IS NOT DISTINCT FROM NEW.profit_control_enabled
       AND OLD.profit_min_margin IS NOT DISTINCT FROM NEW.profit_min_margin
       AND OLD.profit_safety_buffer IS NOT DISTINCT FROM NEW.profit_safety_buffer
       AND OLD.batch_image_discount_multiplier IS NOT DISTINCT FROM NEW.batch_image_discount_multiplier
       AND OLD.batch_image_hold_multiplier IS NOT DISTINCT FROM NEW.batch_image_hold_multiplier
       AND OLD.require_oauth_only IS NOT DISTINCT FROM NEW.require_oauth_only
       AND OLD.require_privacy_set IS NOT DISTINCT FROM NEW.require_privacy_set
       AND OLD.smart_scheduler_enabled IS NOT DISTINCT FROM NEW.smart_scheduler_enabled
       AND OLD.stream_sticky_escape_enabled IS NOT DISTINCT FROM NEW.stream_sticky_escape_enabled
       AND OLD.recovery_probe_enabled IS NOT DISTINCT FROM NEW.recovery_probe_enabled
       AND OLD.recovery_probe_mode IS NOT DISTINCT FROM NEW.recovery_probe_mode
       AND OLD.recovery_probe_model IS NOT DISTINCT FROM NEW.recovery_probe_model
       AND OLD.recovery_probe_interval_seconds IS NOT DISTINCT FROM NEW.recovery_probe_interval_seconds
       AND OLD.recovery_probe_attempts_per_round IS NOT DISTINCT FROM NEW.recovery_probe_attempts_per_round
       AND OLD.recovery_probe_idle_threshold_seconds IS NOT DISTINCT FROM NEW.recovery_probe_idle_threshold_seconds
       AND OLD.recovery_probe_backoff_cap_seconds IS NOT DISTINCT FROM NEW.recovery_probe_backoff_cap_seconds
       AND OLD.pool_mode_enabled IS NOT DISTINCT FROM NEW.pool_mode_enabled
       AND OLD.pool_mode_retry_count IS NOT DISTINCT FROM NEW.pool_mode_retry_count
       AND OLD.pool_mode_retry_status_codes IS NOT DISTINCT FROM NEW.pool_mode_retry_status_codes
       AND OLD.custom_error_codes_enabled IS NOT DISTINCT FROM NEW.custom_error_codes_enabled
       AND OLD.custom_error_codes IS NOT DISTINCT FROM NEW.custom_error_codes
       AND OLD.deleted_at IS NOT DISTINCT FROM NEW.deleted_at THEN
        RETURN NEW;
    END IF;

    INSERT INTO auth_cache_invalidation_outbox (cache_key)
    SELECT encode(sha256(convert_to(k.key, 'UTF8')), 'hex')
    FROM api_keys AS k
    WHERE k.group_id = target_group_id
      AND k.deleted_at IS NULL
      AND k.key <> '';
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;
