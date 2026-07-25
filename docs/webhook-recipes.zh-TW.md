# Webhook recipes

[English](webhook-recipes.md) · [繁體中文](webhook-recipes.zh-TW.md)

Tunlease 會保留 provider 既有的 callback URL。請把下列 path 與 port
替換成 platform team 分配的值，並在 claim 前先啟動 local service。

## Stripe

若既有 endpoint 是
`https://callbacks.staging.example.com/webhooks/stripe/events`：

```bash
tunle claim /webhooks/stripe/* --to 8080
```

請保持 Stripe signature verification 啟用，並使用此 staging endpoint
的 signing secret。

## GitHub

若 repository 或 organization webhook 已指向
`https://callbacks.staging.example.com/webhooks/github`：

```bash
tunle claim /webhooks/github/* --to 3000
```

請在本機設定 webhook secret。GitHub 可從 webhook delivery 頁面重新傳送
先前的 delivery。

## Slack

對於既有 Slack request URL，例如
`https://callbacks.staging.example.com/webhooks/slack/events`：

```bash
tunle claim /webhooks/slack/* --to 4000
```

Local handler 仍須驗證 Slack signatures 並回應 URL verification
challenges。

## 一般 OAuth callback

對於既有 callback，例如
`https://callbacks.staging.example.com/oauth/provider/callback`：

```bash
tunle claim /oauth/provider/callback/* --to 8080
```

Tunnel 連線後再啟動新的 authorization flow，並持續驗證 OAuth `state`；
Tunlease 不會取代 application-level security。

## 結束

Ctrl+C 會釋放 foreground claim。Provider retries 與 dispatch 後的 tunnel
failure 可能造成重複傳送，因此 local handler 應保持 idempotent。
