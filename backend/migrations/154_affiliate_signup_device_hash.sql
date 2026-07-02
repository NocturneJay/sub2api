ALTER TABLE user_affiliates
    ADD COLUMN IF NOT EXISTS signup_device_hash VARCHAR(64) NULL;

CREATE INDEX IF NOT EXISTS idx_user_affiliates_signup_device_hash
    ON user_affiliates(signup_device_hash)
    WHERE signup_device_hash IS NOT NULL;

COMMENT ON COLUMN user_affiliates.signup_device_hash IS 'SHA-256 hash of the browser-local affiliate device id captured during signup or affiliate-page visit';
