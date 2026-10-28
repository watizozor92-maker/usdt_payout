# USDT 代付服务

面向游戏后端的 TRC20-USDT 代付服务。HTTP 收单会立即持久化到 SQLite 队列并返回代付单号；后台单线程热钱包广播、等待确认，完成后可靠回调。下单以 `appId + merchantOrderNo` 幂等。

## 运行

```bash
cp .env.example .env
# 编辑 .env，务必设置 APP_SECRET、CALLBACK_KEY、TRON_PRIVATE_KEY 和回调域名白名单
docker compose up -d --build
```

生产环境建议仅监听内网或由 Nginx 提供 HTTPS，数据库目录必须做持久化备份。热钱包只保留业务所需的小额 USDT 和足够 Energy/TRX；私钥只通过环境变量/Secret 注入，服务不会把私钥发送给 TronGrid。

## 创建代付订单

`POST /api/v1/withdraw`，支持 `application/x-www-form-urlencoded`（游戏当前使用）或 JSON：

| 字段 | 说明 |
|---|---|
| `appId` | 商户 ID，对应 `APP_ID` |
| `merchantOrderNo` | 游戏提现单号，最长 128 字符 |
| `chainType` | 固定 `1`，表示 TRON/TRC20 |
| `crypto` | USDT 数量，最多 6 位小数 |
| `toAddress` | 玩家 TRON 地址 |
| `attach` | 透传信息，可选 |
| `notify_url` | 完成回调地址 |
| `signature` | 小写十六进制 HMAC-SHA256 |

签名规则与 `CmdWithdrawAppCts.java` 一致：排除 `signature`，其余非空参数按 key 升序拼成 `key=value&key=value`，使用 `APP_SECRET` 计算 HMAC-SHA256。

成功响应：

```json
{"code":0,"msg":"success","data":{"withdraw_no":"wd_...","merchant_order_no":"tt...","status":"processing"}}
```

相同商户订单号和相同参数重复提交会返回原订单；参数不同则返回 HTTP 409，绝不会建立第二笔代付。

## 查询订单

`POST /api/v1/withdraw/query`，参数为 `appId`、`merchantOrderNo`、`signature`，签名规则同上。状态为 `processing / success / failed`。

## 完成回调

服务向下单时的 `notify_url` POST JSON，HTTP 2xx 视为接收成功，失败按指数退避重试：

```json
{
  "username":"game-hall",
  "withdrawNo":"wd_...",
  "merchantWithdrawNo":"tt...",
  "toAddress":"T...",
  "amount":"10.5",
  "txid":"...",
  "status":"completed",
  "now":"1793174400",
  "callKey":"configured-value",
  "sign":"hmac-sha256"
}
```

链上失败时 `status=failed`。回调 `sign` 同样对除 `sign` 外的非空字段排序后使用 `APP_SECRET` 计算。现有游戏可将通道 `server_return` 配置为其 `FcipayWithdrawNotify` 地址，并把 `CALLBACK_KEY` 配成该接收端要求的 callKey。

## 安全与可靠性

- 收单验签、TRON 地址校验、金额精确到最小单位，不使用浮点数。
- SQLite WAL 持久化订单、已签名交易、交易哈希及回调次数。
- 广播前先保存已签名交易；宕机恢复只重播同一 txid，避免重复付款。
- 等待可配置确认数后才回调成功，默认 20 个区块。
- `CALLBACK_ALLOWED_HOSTS` 限定回调域名，降低 SSRF 风险。
- `PAYOUT_DRIVER=mock` 仅供本地联调，启动时会打印醒目警告。

## 游戏端通道配置

- `code`: `UPAY`
- `user_code`: 与 `APP_ID` 相同
- `secret`: 与 `APP_SECRET` 相同
- `gateway`: `http://代付服务地址:8088/api/v1/withdraw`
- `server_return`: 游戏现有提现回调 URL

游戏端必须把玩家钱包地址 `payPaymentOrder.getAddress()` 作为 `toAddress`；本次已经修正原代码误用通道地址的问题。
