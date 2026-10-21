# wechat-cli

A standalone Go CLI for Tencent's WeChat **iLink bot** API. No OpenClaw or Node.js required.

Supports QR login in your terminal and as a PNG, verification codes, QR refresh, saved credentials, receiving messages, and sending text replies.

This is a bot conversation client, **not a personal WeChat inbox client**. It cannot enumerate your personal contacts, fetch your chat history, or send arbitrary messages to friends. Your WeChat account/app must support the iLink / ClawBot pairing flow. Incoming attachments are shown as placeholders (full metadata is available with `--json`); media upload/download is not implemented.

## Build

Requires Go 1.25 or newer.

Install directly from GitHub:

```sh
go install github.com/kevmo314/wechat-cli@latest
wechat-cli --help
```

Or build a local binary named `wechat`:

```sh
go build -o bin/wechat .
./bin/wechat --help
```

## Login and exchange messages

```sh
# Scan the terminal QR in WeChat; confirm on your phone.
# Enter the verification code if prompted. PNG output is optional.
./bin/wechat login --qr login.png
./bin/wechat status

# Send a message to the paired bot from your phone, then receive it.
./bin/wechat read --follow

# In another terminal, list IDs learned from incoming messages and reply.
./bin/wechat contacts
./bin/wechat send --to 'USER_ID_FROM_READ' --text 'Hello from Go!'
printf 'A multiline\nreply\n' | ./bin/wechat send --to 'USER_ID_FROM_READ' --stdin
```

`read` without `--follow` performs one long poll (normally up to 40 seconds). It returns successfully with no output when no messages arrive. `--follow` retries transport failures with bounded backoff and stops on API errors. Ctrl-C stops polling. An expired session (`-14`) requires another login.

`read --json` emits each original message as one JSON line, preserving message IDs and unknown fields. This output includes conversation context tokens and any media metadata; treat it as private. Example:

```sh
./bin/wechat read --follow --json | jq -c '{from: .from_user_id, items: .item_list}'
```

Replies use the most recently received `context_token` for the recipient. Run `read` first. If a token expires, have the user send a fresh message and receive it before replying. You can override it with `send --context-token TOKEN`. The CLI does not automatically retry sends because a network failure can leave delivery uncertain.

## Local state

Credentials, conversation tokens, and the polling cursor are saved in `state.json` under the OS user config directory (`~/.config/wechat-cli` on Linux, respecting `XDG_CONFIG_HOME`). New directories use mode `0700`; state files use `0600`. State is plaintext, not encrypted. Do not share it.

Use a separate config directory per account; global flags go **before** the command:

```sh
./bin/wechat --config-dir ~/.config/wechat-work login
./bin/wechat --config-dir ~/.config/wechat-work read --follow
./bin/wechat logout
```

`logout` clears local state; it does not revoke the binding in WeChat. Remove that in the app if needed. Delete any saved login PNG after pairing.

Only one `read`, `login`, or `logout` process can run per config directory. `send`, `contacts`, and `status` can run alongside a reader. State updates use a lock and atomic replacement. A receive batch is checkpointed after successful output, so interruption may replay messages; consumers should deduplicate by message ID. The CLI does not archive messages locally.

Do not run the OpenClaw plugin and this CLI against the same bot simultaneously: they can compete for updates.

## Protocol reference and validation

Implemented against [Tencent/openclaw-weixin](https://github.com/Tencent/openclaw-weixin), its [protocol reference](https://github.com/Tencent/openclaw-weixin/blob/main/docs/protocol.md), and QR/API/message source code. Reviewed upstream commit `874637a0dbc24050f72d1385add4fc6170a2d726`, plugin version `2.4.10`. The CLI declares its own `WeChatCLI/0.1.0` agent identity and uses the plugin's compatible iLink version headers. It is an independent client, not an official Tencent product.

```sh
go test -race ./...
go vet ./...
```

Tests use a local HTTP server for QR refresh/verification, request headers, polling cursors, message payloads, error handling, cancellation, and concurrent state updates. A live smoke check also successfully fetched a QR code from Tencent and generated a valid PNG; the unscanned login timed out as expected. Authenticated receive/send and end-to-end delivery still require pairing with a real WeChat account.
