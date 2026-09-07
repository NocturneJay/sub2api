-- 邀请「首单双向奖励」（首充券）：被邀请人的第一笔达标订单同时给双方发钱。
--
-- 编号说明：沿用 aicat 自研的 900 保留区段，不插进上游序列。上游仍在 23x 一带
-- 持续新增迁移，沿用相邻编号迟早撞车。本迁移只新建表 + 加一个可空列，
-- 按文件名排序落在最后执行没有任何影响。
--
-- 为什么单独建表而不是往 user_affiliates 加几列：
--   1. 「每个账号最多领一次」直接由主键保证，不需要应用层判重；
--   2. 要能回溯是哪一笔订单触发的、当时的金额与两侧奖励各是多少（设置是全局
--      实时值、不做每人快照，事后改配置就再也反推不出当时发了多少）；
--   3. 作废（不到阈值 / 已过期）也要落表可查，否则用户问「我的券呢」只能猜。
--
-- ON DELETE 语义是逐个想过的，与 134 迁移的 SET NULL 有意不同：
--   * order_id → RESTRICT：奖励记录不该随订单清理而消失，否则删掉一笔历史订单
--     就等于把那个账号的券还回去了。已核全仓没有删 payment_orders 的路径
--     （退款只改状态），所以 RESTRICT 不会在正常运行中挡住任何写入。
--     另注：payment_orders.user_id 上本来就没有 users 外键，所以这条 RESTRICT
--     不会影响删用户。
--   * user_id → CASCADE、inviter_id → SET NULL：User 是软删除，这两条实际永不触发。
--     写出来只是表达意图；被邀请人软删后记录仍在，正好防「删号重领」。
--
-- 两种 DECIMAL 精度各自对齐来源，不要统一：
--   * order_amount 对齐 payment_orders.amount 的 DECIMAL(20,2)（订单美元面额）；
--   * invitee_bonus / inviter_bonus 对齐 user_affiliates.aff_quota 的 DECIMAL(20,8)
--     （返利额度一直是 8 位小数，混用会在对账时出现末位差）。
CREATE TABLE IF NOT EXISTS user_affiliate_first_order_bonus (
    user_id        BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    inviter_id     BIGINT NULL REFERENCES users(id) ON DELETE SET NULL,
    order_id       BIGINT NOT NULL REFERENCES payment_orders(id) ON DELETE RESTRICT,
    order_amount   DECIMAL(20,2) NOT NULL,
    invitee_bonus  DECIMAL(20,8) NOT NULL DEFAULT 0,
    inviter_bonus  DECIMAL(20,8) NOT NULL DEFAULT 0,
    status         VARCHAR(32) NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_affiliate_first_order_bonus_inviter_id ON user_affiliate_first_order_bonus(inviter_id);
CREATE INDEX IF NOT EXISTS idx_user_affiliate_first_order_bonus_order_id ON user_affiliate_first_order_bonus(order_id);

COMMENT ON TABLE user_affiliate_first_order_bonus IS '邀请首单双向奖励记录（一人一行，含作废）';
COMMENT ON COLUMN user_affiliate_first_order_bonus.user_id IS '被邀请人用户ID，主键即「每人最多一次」';
COMMENT ON COLUMN user_affiliate_first_order_bonus.inviter_id IS '发放时的邀请人用户ID';
COMMENT ON COLUMN user_affiliate_first_order_bonus.order_id IS '触发判定的订单ID';
COMMENT ON COLUMN user_affiliate_first_order_bonus.order_amount IS '触发判定时的订单美元面额';
COMMENT ON COLUMN user_affiliate_first_order_bonus.invitee_bonus IS '被邀请人实得余额奖励；作废记录为 0';
COMMENT ON COLUMN user_affiliate_first_order_bonus.inviter_bonus IS '邀请人实得返利额度奖励；作废记录为 0';
COMMENT ON COLUMN user_affiliate_first_order_bonus.status IS 'applied | void_below_threshold | void_expired';

-- 返利流水加 kind：把首单奖励从「单人返利上限」的统计里剔除
-- （GetAccruedRebateFromInvitee 只累加 kind IS NULL OR kind <> 'first_order_bonus'）。
-- 其余台账查询（解冻、转余额、管理端返利记录）都不过滤 kind —— 首单奖励就是
-- 真实累计的返利额度，本来就该被解冻、被转余额、在记录里看得见。
--
-- 历史行保持 NULL（= 常规返利/转余额）。PostgreSQL 11 起加可空列是元数据操作、
-- 不重写表，在大表上也是瞬时完成的。
ALTER TABLE user_affiliate_ledger
    ADD COLUMN IF NOT EXISTS kind VARCHAR(32) NULL;

COMMENT ON COLUMN user_affiliate_ledger.kind IS '流水类别；NULL=常规返利/转余额，first_order_bonus=首单双向奖励';
