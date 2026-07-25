# Webhook recipes

[English](webhook-recipes.md) · [繁體中文](webhook-recipes.zh-TW.md)

Tunlease keeps the callback URL already configured at the provider. Replace the
example paths and ports below with the values your platform team assigned.
Start the local service before claiming a path.

## Stripe

If the existing endpoint is
`https://callbacks.staging.example.com/webhooks/stripe/events`:

```bash
tunle claim /webhooks/stripe/* --to 8080
```

Keep Stripe signature verification enabled and use the signing secret for this
staging endpoint.

## GitHub

If the repository or organization webhook already targets
`https://callbacks.staging.example.com/webhooks/github`:

```bash
tunle claim /webhooks/github/* --to 3000
```

Keep the webhook secret configured locally. GitHub can redeliver an earlier
delivery from its webhook delivery page.

## Slack

For an existing Slack request URL such as
`https://callbacks.staging.example.com/webhooks/slack/events`:

```bash
tunle claim /webhooks/slack/* --to 4000
```

Your local handler must still verify Slack signatures and answer URL
verification challenges.

## Generic OAuth callback

For an existing callback such as
`https://callbacks.staging.example.com/oauth/provider/callback`:

```bash
tunle claim /oauth/provider/callback/* --to 8080
```

Start a fresh authorization flow after the tunnel connects. Keep validating
the OAuth `state` value; Tunlease does not replace application-level security.

## Finish

Ctrl+C releases a foreground claim. Provider retries and a tunnel failure after
dispatch can produce duplicate delivery, so local handlers should be
idempotent.
