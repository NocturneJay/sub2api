CREATE TABLE IF NOT EXISTS user_affiliate_devices (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_hash VARCHAR(64) NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, device_hash)
);

CREATE INDEX IF NOT EXISTS idx_user_affiliate_devices_device_hash
    ON user_affiliate_devices(device_hash);

INSERT INTO user_affiliate_devices (user_id, device_hash, first_seen_at, last_seen_at)
SELECT user_id,
       signup_device_hash,
       COALESCE(created_at, NOW()),
       COALESCE(updated_at, NOW())
FROM user_affiliates
WHERE signup_device_hash IS NOT NULL
  AND signup_device_hash <> ''
ON CONFLICT (user_id, device_hash) DO NOTHING;

COMMENT ON TABLE user_affiliate_devices IS 'Historical affiliate signup device hashes used to suppress same-device invite rebates per order';
