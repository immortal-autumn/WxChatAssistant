# Native desktop protocol investigation

The regular desktop client uses a different protocol from ClawBot/iLink. The
ClawBot eligibility rejection does not establish that native desktop login is
unavailable for the same account.

## Independently inspected desktop build

Downloaded the official Linux amd64 package on 2026-10-22, extracted it without
installing or executing it, and inspected its executable and embedded routes:

- Package: `wechat`, version `4.1.13.26`, amd64.
- Source: <https://dldir1v6.qq.com/weixin/Universal/Linux/WeChatLinux_x86_64.deb>.
- Package SHA-256: `a0db8caeee8d1e6cfe0aa5ce58710270f8d74a90ff4920f53a3fc22aa0956fe9`.
- Executable SHA-256: `2749535f299ba67feb8ef80cab898bb0531fa8f376b16f8a72db706811c84132`.
- Executable: `opt/wechat/wechat`, stripped x86-64 ELF.

The package hash also matches the
[Flathub manifest](https://github.com/flathub/com.tencent.WeChat/blob/master/com.tencent.WeChat.yaml).
The executable hash exactly matches the reference artifact recorded by the
[agent-wechat protocol implementation](https://github.com/thisnick/agent-wechat/tree/897ad87cf569f40e53994331e654045ae1c42d4b/packages/native-client-rust/crates/wechat-protocol).
That project was inspected as a protocol reference; it is not a dependency or
backend of this Go CLI. Its reported live capabilities are not our live results.

## Native API and transport

The native client uses MMTLS record encryption with another encrypted protobuf
business envelope inside it. It has long-lived TCP and HTTP-carried shortlink
transports. The HTTP carrier can use port 80 because MMTLS itself authenticates
and encrypts its contents. These are not the JSON bot endpoints at
`ilinkai.weixin.qq.com/ilink/bot/`.

The executable contains the following routes and embedded longlink identifiers.
The business CGI identifiers come from the inspected protocol references; they
are a separate namespace from the longlink commands:

| Native route under `/cgi-bin/micromsg-bin/` | Longlink command | Business CGI | Purpose |
| --- | ---: | ---: | --- |
| `getloginqrcode` | 232 | 502 | Request normal desktop login QR |
| `checkloginqrcode` | 233 | 503 | Poll phone scan/approval |
| `secmanualauth` | 433 | 252 | Exchange phone approval for account session |
| `secautoauth` | Not independently mapped here | 763 | Renew saved authorization |
| `newinit` | 27 | 139 in older reference | Initial synchronization |
| `newsync` | 121 | 138 | Incremental synchronization |
| `newsendmsg` | 237 | 522 | Text message submission |

Some embedded configuration tables are historical. The current QR command 232
was additionally validated by a successful live request; the other mappings
have not all been exercised here. Finding an endpoint string does not by itself
validate its request schema or account behavior.

## New Go probe and live results

```sh
go run ./tools/native-probe
go run ./tools/native-probe --qr /tmp/native-login.png
```

This standalone experimental probe uses Go's cryptographic and networking
primitives. It does not invoke WeChat, Rust, OpenClaw, or a bridge service. It is
also available through `wechat login`, which now defaults to the native backend. The standalone probe itself remains account-free.

Live results on 2026-10-22:

1. Connected to `long.weixin.qq.com:80`, using the modern `0xf104` MMTLS profile.
2. Verified the pinned ECDSA server signature, encrypted handshake records,
   server Finished MAC, and authenticated application NOOP response. The server
   selected region 1 and certificate version 5.
3. Sent the desktop QR request with the `UnifiedPCLinux x86_64` device profile
   and client version `-227271398` (Linux 4.1.13.26 reference).
4. Verified the business response signature and AES-GCM authentication, and
   received a 3,852-byte PNG with native API return status zero.
5. Scanned the returned image successfully: it contains an ordinary
   `http://weixin.qq.com/x/…` native login link, not an iLink pairing link.

An initial experiment incorrectly used business CGI 502 as the longlink
command and received business status `-13`. Inspecting the executable's route
table revealed command 232. With that correction, the QR service succeeded.
This matters when implementing further native calls: the two identifiers are
not interchangeable.

The probe sends one QR request with a zero notice checksum, which this tested
service accepted. This does not establish that the same policy is accepted for
account authentication or other services. It does not poll the QR, save approval
credentials, authenticate an account, or receive/send messages. The QR image is
for protocol verification; scanning it cannot complete a login with this probe.
The socket and input sizes are bounded, and there is no insecure peer/signature
verification switch.

Unit tests cover RFC 5869 HKDF, record tampering/sequence rejection, pinned-key
signature verification, an independently generated encrypted business fixture,
and malformed structure/protobuf rejection. The native probe is account-free;
these tests are not evidence of successful account authorization.

## Existing research that can inform the next layer

[agent-wechat](https://github.com/thisnick/agent-wechat) now contains a standalone
native protocol runtime targeting this exact Linux build. Its
[supported-scope documentation](https://thisnick.github.io/agent-wechat/getting-started/native-client/)
describes operation without the official executable, primarily by taking over a
stopped existing Linux desktop profile. Fresh-profile creation remains less
established than saved-profile takeover. Its source covers native phone
approval, session renewal, sync, message crypto, and official-format storage.

This provides useful protocol evidence without making our client depend on its
Rust daemon or requiring a desktop sidecar. Our earlier local legacy Windows
probe also fetched native QR data, but it is not the modern implementation here
and does not validate current Linux account authentication.

## What this establishes about conversation history

Existing local history and native network sync are different data sources.
The referenced runtime reads preexisting history from the selected official
encrypted profile and applies subsequent native sync updates. Its documented
history support is not evidence that a fresh account session can retrieve all
old conversations from a server.

An older reference includes `syncMsg=1` in the phone-side
`extdeviceloginconfirmok` approval request. That is a lead for the login-time
recent-message option, not a tested arbitrary-history endpoint. We still need
an approved native session and controlled first-login observations to determine
what history the current desktop protocol transfers, how far back it goes, and
whether any part is transferred from the phone rather than the server.

The inspected executable contains 193 distinct `/cgi-bin/micromsg-bin/`
paths, including `extdeviceuploadlaninfo` and `bakchatcreateqrcode`. Those are
additional leads for phone/LAN login checks and backup transfer. No obvious
general-purpose history-download path appeared in that string inventory, but
string absence is not proof that such a service does not exist. History-related
protobuf names are present and do not, by themselves, identify callable APIs.

## Integrated Go CLI and approved-account result

The CLI now implements native QR request/poll, phone-grant authentication,
region redirects, encrypted account session storage, incremental sync, and text
submission. The old bot backend is selected with `--backend ilink`.

On 2026-10-23 the user scanned and approved a fresh QR. Authentication completed
and followed the server redirect to `sglong.wechat.com`. Native synchronization
returned 503 decodable messages across five archived batches, plus other native
sync commands. This demonstrates some existing-message retrieval on this
account, not a complete-history API. No message contents or authorization
material are included in this repository. Outgoing send receipts are covered
by synthetic tests; no live outgoing message has been sent.

Zero notice checksums were accepted for this account's QR polling,
authentication, and sync as well as QR retrieval. This remains an experimental
compatibility choice, rather than a claim of implementing the full official
client checksum algorithm.

The login displaced the user's other desktop session. The CLI currently uses
the Linux desktop session class; it has no tested coexistence mode. The
[reference project's published limitations](https://www.npmjs.com/package/@agent-wechat/agent-wechat)
also describe a single desktop session. A tablet protocol may be a separate
research direction, but merely changing the device label is not a validated
solution.

Remaining gaps include session renewal, reconnect handling, phone/LAN device
verification, official profile import, media transfer, and comprehensive
contact discovery. Unknown sync commands are retained privately before cursor
advancement so the current decoder does not discard them.

## Contact updates

Contact management uses authenticated shortlink requests: `/cgi-bin/micromsg-bin/getcontact` (CGI 182) fetches the current contact, and `/cgi-bin/micromsg-bin/oplog` (CGI 681) applies one `ModContact` operation (command 2). Remarks use optional field 10 (SKBuiltinString); pins use only flag mask `0x800`. The request omits unrelated optional properties and retains required identity fields from the fresh contact. Successful changes are journaled as contact updates so they survive restarts and flow through the existing projection.

The oplog response has a top-level integer status and a counted, packed list of per-operation statuses. Both must indicate success before a change is displayed as accepted. No update is automatically retried after a transport error.

Wire references: [GetContact schema](https://github.com/nsky99/wechatclient/blob/main/pkg/wechat_sdk/mmpb/pb/GetContact.proto), [ModContact schema](https://github.com/nsky99/wechatclient/blob/main/pkg/wechat_sdk/mmpb/pb/ModContact.proto), [Oplog schema](https://github.com/nsky99/wechatclient/blob/main/pkg/wechat_sdk/mmpb/pb/Oplog.proto), and the [Ipad860 remark operation](https://github.com/o14547/Ipad860/blob/main/models/Friend/SetRemarks.go). The implementation uses independently encoded protobuf fields rather than copied source code.

Live validation: authenticated self-contact lookup and an empty oplog request succeeded. The empty request submitted no contact changes. Actual remark/pin mutations and their propagation to another device remain unverified; unit tests cover targeted field selection, per-operation rejection, Unicode/empty remarks, and UI save/failure behavior.
