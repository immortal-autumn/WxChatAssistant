# wechat-cli

A standalone Go CLI for personal WeChat messages using the native desktop protocol. No WeChat desktop process, OpenClaw, Rust daemon, or bridge service is required. The original iLink/ClawBot backend remains available with `--backend ilink`.

**Experimental:** the default desktop profile can log out another computer using the same account. The new Android tablet profile completed login and message sync, and the user reported that the other clients stayed signed in. One explicitly authorized text send also returned server acceptance; recipient delivery/read status was not independently checked. See the [tablet investigation and live results](docs/tablet-protocol.md).

## Build

Requires Go 1.25 or newer.

```sh
go build -o bin/wechat .
./bin/wechat --help
# Or: go install github.com/kevmo314/wechat-cli@latest
# The installed command is named wechat-cli.
```

## Use

To try the Android tablet profile while retaining your existing desktop CLI state:

```sh
./bin/wechat --config-dir ~/.config/wechat-tablet login --profile android-tablet
./bin/wechat --config-dir ~/.config/wechat-tablet read --follow
./bin/wechat --config-dir ~/.config/wechat-tablet history --limit 50
./bin/wechat --config-dir ~/.config/wechat-tablet send --to 'RECIPIENT_ID' --text 'Hello!'
```

The saved account remembers its protocol profile, so subsequent commands only need the same config directory. To resume a tablet QR attempt, use `login --resume --profile android-tablet` in that directory. Coexistence has been observed on one account; it is not guaranteed for every account or device combination.

For the default desktop profile:

```sh
# Scan the terminal QR and approve on your phone.
./bin/wechat login
# Optional PNG output, in addition to the terminal QR:
./bin/wechat login --qr login.png
./bin/wechat status

# Synchronize available messages and keep receiving updates.
./bin/wechat read --follow
./bin/wechat read --json

# Browse messages already synchronized by this CLI.
./bin/wechat history --limit 50
./bin/wechat contacts

# Use a native username/wxid or chatroom ID learned from read/contacts.
./bin/wechat send --to 'RECIPIENT_ID' --text 'Hello from Go!'
printf 'A multiline\nmessage\n' | ./bin/wechat send --to 'RECIPIENT_ID' --stdin
```

`read` drains available sync pages and exits. `--follow` checks for updates every three seconds after draining them. It stops on transport/session errors; session renewal and automatic reconnection are not implemented. Run `read` again after a temporary connection failure. An expired or superseded authorization requires another phone-approved login. Sends are never automatically retried because an interrupted response can leave delivery uncertain.

`read --json` and `history` output one decoded message per JSON line. Messages include server ID, sender, recipient, peer, message type, content, timestamp, and direction. Non-text content may contain raw XML or other native payload text; media downloading and rich formatting are not implemented. `contacts` lists peer IDs observed in the local archive, rather than fetching a complete address book.

**History scope:** first-login sync can return existing messages. The first desktop login synchronized 503 entries. The tablet login synchronized 496 entries, including 121 text messages; other entries included media and system events. This does not establish that all conversations or all historical messages are available. `history` only reads this CLI's local archive. Phone migration/backup and official desktop database import are not implemented.

The terminal QR uses explicit black-on-white colors. Server PNG/JPEG QR images are scanned and rerendered as terminal blocks. A saved temporary QR/approval can be resumed with `login --resume` until it expires. New-device phone/LAN verification challenges are detected but not implemented.

## State and concurrency

State defaults to `~/.config/wechat-cli` on Linux, respecting `XDG_CONFIG_HOME`. Native state lives in its `native/` subdirectory, separately from existing iLink state. Credentials and approval grants are plaintext, private files (`0600` in `0700` directories). Do not share the config directory or sync journals.

All sync commands, including unknown types, are archived under `native/batches/` before the cursor is committed. `history` deduplicates messages by account and message ID. If output is interrupted after a cursor commit, inspect `history` to recover the archived messages. Files are written atomically and synced to disk.

Only one reader/login/logout process can run per config directory. Sending can run alongside a reader; account updates are serialized. Multiple processes do not provide additional desktop sessions.

```sh
# Global flags must precede the command.
./bin/wechat --config-dir ~/.config/wechat-work login
./bin/wechat --config-dir ~/.config/wechat-work read --follow
./bin/wechat logout
```

`logout` deletes local credentials and cursor but retains the archive. It does not remotely revoke the device authorization; manage that on your phone. It also does not restore a desktop session displaced during login.

## Protocol and validation

The Go implementation verifies MMTLS server signatures and encrypted records, then uses encrypted native protobuf calls for QR request/poll, phone-grant authentication, sync, and text sends. It follows the inspected Linux 4.1.13.26 profile. There is no insecure signature-verification switch. Request notice checksums currently use zero; the tested server accepted QR, authentication, and sync requests, but compatibility with other accounts/builds is not established.

See [native protocol findings](docs/native-protocol.md) for the inspected official binary, route IDs, protocol references, and live validation limits. See [iLink usage](docs/ilink.md) for the original bot backend. This is an independent client, not an official Tencent product.

```sh
go test -race ./...
go vet ./...
```

Tests cover authenticated transport records, encrypted business fixtures, phone approval/challenge handling, cursor merge/reset, send-receipt identity matching, unknown sync payload retention, private archive permissions, replay deduplication, and separation from bot credentials. Existing iLink HTTP and QR image/rendering tests remain in place. Live tests verified desktop and tablet QR login, account synchronization, and server acceptance of one user-authorized outgoing tablet text message.
