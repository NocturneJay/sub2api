-- 187_composite_route_target_group.sql
-- Composite 路由新增"委托到子分组"能力：一条路由可指向某个具体子分组，
-- 请求由该子分组的账号池调度，并按该子分组的定价计费；配额/限额/扣费仍记在
-- composite（通用）分组头上。
--   - target_group_id：非空即"路由到子分组"，与 target_platform 二选一（分组模式
--     下 target_platform 由子分组平台推导写入，仅作兼容占位）。硬删除由
--     ON DELETE RESTRICT 兜底，避免硬删除把分组路由静默放大为平台路由；正常软删除路径会在
--     groupRepository.DeleteCascade 中禁用引用该目标组的路由并保留审计信息。
--   - rate_multiplier：每路由倍率覆盖；NULL 表示沿用子分组自身倍率。
ALTER TABLE composite_model_routes
    ADD COLUMN IF NOT EXISTS target_group_id BIGINT NULL REFERENCES groups(id) ON DELETE RESTRICT;

ALTER TABLE composite_model_routes
    ADD COLUMN IF NOT EXISTS rate_multiplier DECIMAL(10, 4) NULL;

CREATE INDEX IF NOT EXISTS idx_composite_model_routes_target_group
    ON composite_model_routes (target_group_id)
    WHERE deleted_at IS NULL;
