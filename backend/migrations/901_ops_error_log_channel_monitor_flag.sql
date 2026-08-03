-- 错误请求页的「排除渠道监控」筛选。
--
-- 与 900_usage_log_channel_monitor_flag.sql 同源：监控健康检查是通过本站网关发的
-- 真实请求，失败时既写 usage_logs 也写 ops_error_logs。900 只给用量明细加了标记，
-- 错误看板仍混着监控噪声（2026-08-03 近 24 小时里监控密钥贡献 259 条），
-- 这里补上同名列，让两个页面用同一套口径。
--
-- 编号沿用 aicat 自研的 900 保留区段，不插进上游序列。
--
-- 只加列、不动既有数据：存量记录一律 FALSE。标记只对上线后新产生的记录生效，
-- 历史不回填 —— 事后无法可靠区分（监控与管理员真实使用共用同一把 API 密钥）。
--
-- 不建索引：过滤条件是 is_channel_monitor 为假、几乎匹配全表，索引对它没有帮助。
--
-- PostgreSQL 11 起带非易变默认值的 ADD COLUMN 是元数据操作、不重写表。
ALTER TABLE ops_error_logs
    ADD COLUMN IF NOT EXISTS is_channel_monitor BOOLEAN NOT NULL DEFAULT FALSE;
