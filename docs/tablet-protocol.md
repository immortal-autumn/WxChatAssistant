# Android tablet / iPad protocol investigation

Investigated 2026-10-23 to find a standalone login mode that can potentially
coexist with an official desktop session. **An experimental standalone Android tablet profile now works on the tested
account.** The user approved login and reported that the other clients remained
signed in. Native sync and one user-authorized text send also succeeded. The
default profile remains Linux desktop; select tablet explicitly in a separate
config directory as shown below.

## Official Android artifact

Downloaded directly from Tencent, without installing or executing the app:

- APK: [WeChat 8.0.79, version-code 3200 download](https://dldir1v6.qq.com/weixin/android/weixin8079android3200_0x28004f30_arm64.apk).
- APK SHA-256: `5feb100337981467fd257c3ad66bb171f54a69d2579b2ecc70d5a628db8e7282`.
- Filename protocol version: `0x28004f30`. Do not assume all tablet requests use
  that value until the runtime version path has been traced.
- Inspected ARM64 `libMMProtocalJni.so` SHA-256:
  `2f89db54af1ab73fa5a5de3cb9abdb09bc3570fb9b159333f088115d34ce6bb6`.
- Selected DEX classes decompiled with [JADX 1.5.6](https://github.com/skylot/jadx/releases/tag/v1.5.6).
- Native JNI entry points inspected with `readelf` and ARM64 `objdump`.

Class names below are specific to this artifact; they are obfuscated and may
change in another build. The route strings and generated wire serializers are
more reliable anchors. Decompiled vendor code and the APK are not redistributed
in this repository.

## Device selection is more than a display name

`com.tencent.mm.app.s7.b(int)` in `classes11.dex` initializes the device profile.
Its tablet branches select `pad-android-` plus the SDK/version suffix, set
`kd5.bg.a = 28`, and call `MMProtocalJni.setDeviceTypeId(bg.a)`. The phone profile
uses native device type 2 and the `android-` prefix. Runtime preferences and
foldable/tablet detection affect which branch is selected.

The exported native setter is at ELF virtual address `0x7c194`, branching to
`0x7743c`. The latter stores the supplied integer in a global at `0xe8028`.
The global is referenced through GOT relocation `0xe3ac8`. Packing code at
`0x5c1a4` loads it and calls `0x5ffe4`, which writes the 16-bit device field at
packet-object offset 47. Serialization passes the object plus 8 bytes to the
header writer; at `0x65760` the writer loads that field at relative offset 39
and writes it as a varint. This identifies the device-type header slot after
the key-version field. The existing desktop reference called that slot
`cert_version`; treating it as an unrelated certificate selector was
misleading. The Go tablet profile now writes 28 there, rather than changing
only the protobuf device string.

`com.tencent.mm.ui.j9.a()` has a separate local Lenovo TB-9707F model check.
That helper is **not** the entire tablet selection policy.

## Tablet eligibility request

The official `l41.j1.a(...)` class in `classes5.dex` configures:

- Route: `/cgi-bin/micromsg-bin/checkloginaspad`.
- Business CGI / scene identifier: **9931**. Its longlink command, if any, has
  not been independently mapped. Do not pass 9931 as a guessed longlink command.
- Request type: `nd5.a30`, `classes5.dex`.
- Response type: `nd5.b30`, `classes2.dex`.

The generated serializers and request builder establish these fields:

| Message | Field | Wire type | Meaning |
| --- | ---: | --- | --- |
| Request | 1 | nested message | BaseRequest |
| Request | 2 | string | DeviceBrand |
| Request | 3 | string | DeviceModel |
| Request | 4 | nested SKBuffer | RandomEncryKey |
| Response | 1 | nested message | BaseResponse |
| Response | 2 | varint boolean | LoginAsPad |

The caller supplies lowercased `Build.BRAND` and a model string from `w0.m()`
concatenated with `Build.CPU_ABI`. The random key is also installed as the
request pass key. The request selects RSA configuration `kd5.ui.d()`; in this
build its fallback is public-key version **200**, exponent 65537. Cached,
server-updated key settings can override that fallback.

A failed network/API result becomes `false` in this eligibility helper. A
successful result returns the server's `LoginAsPad` boolean.

The [wx-pad module](https://github.com/Slacker-LLC/wx-pad/tree/acc6647aa7dad752ba311c3d0366e09398a7aba1)
provided useful string anchors for independently locating these classes. Its
hook changes the local model-check result and the eligibility helper's returned
boolean. It leaves the underlying network call in place. That is evidence of
client-side gating, **not** proof that the server accepts every fabricated
profile, or that its hook is a standalone protocol implementation. No hook was
installed or run in WeChat during this investigation.

## Official secondary-device flow

`com.tencent.mm.plugin.account.ui.LoginSelectUI`, in `classes5.dex`, initially
hides the `phone_and_pad` preference and shows it after successful eligibility.
Choosing that preference opens
`com.tencent.mm.plugin.login_exdevice.ui.LoginAsExDeviceUI`. The other option,
`only_pad`, enters the ordinary mobile-number login flow. A tablet can therefore
be a secondary device or the primary mobile device; those are different modes.

The secondary-device flow uses familiar endpoints:

| Step | Business CGI | Inspected implementation |
| --- | ---: | --- |
| Get login QR | 502 | `li3.b` |
| Poll approval | 503 | `li3.a` |
| Authenticate approval | 252 / 701 handlers | `LoginAsExDeviceUI` and `com.tencent.mm.modelsimple.v0` |

The UI polls, decrypts the notification using the QR notify key through
`MMProtocalJni.aesDecrypt`, and handles notification status 2 by passing the two
phone-issued strings to the authentication scene. These are temporary approval
credentials, rather than a request for the user's ordinary account password.
The exact native risk-data construction still needs tracing. The minimal Go phone-grant exchange has nevertheless succeeded in the live experiment below.

Important differences from the current Linux implementation:

1. **Poll timestamp:** `li3.a` sends `System.currentTimeMillis() / 1000`, a full
   Unix-seconds value. Our Linux desktop profile uses a truncated-millisecond
   timestamp from its matching protocol reference. A tablet implementation
   should not inherit that desktop behavior without verification.
2. **QR request metadata:** the generated request `nd5.jp3` (`classes4.dex`)
   serializes BaseRequest at 1, the random key at 2, opcode at 3, optional strings
   at 4/5, and an extra SKBuffer at **11**. The builder populates field 11 with
   serialized `nd5.gh7` whose field `f` contains opaque bytes from `dv3.q.d.h()`.
   The meaning, generation, and server requirements for those bytes are not yet
   established.
3. **Pre-login key configuration:** QR request/poll use `kd5.ui.a()` with public
   RSA key version **199**, exponent 65537. Eligibility uses the different
   configuration above. These are Java request-layer settings; native packing
   must be traced before inferring the actual envelope from them. The desktop
   implementation's pinned ECDH business envelope is not automatically a valid
   replacement.
4. **Device type:** the Android tablet string and native ID 28 must be handled
   consistently across QR, authentication, and later session requests. Changing
   the computer name or just one protobuf field does not reproduce this path.

## Public iPad/Android-pad implementations

Inspected [meteor-nb/Ipad860 at 4d567bd](https://github.com/meteor-nb/Ipad860/tree/4d567bd6ee3206b5f112768225d7498abfc442b1)
as another protocol reference, without executing it or copying its source.
It has separate iPad and Android-pad QR/auth paths using the same CGI family,
plus device-token initialization through `/cgi-bin/micromsg-bin/fpinitnl` and
WCExtInfo security fields during authentication.

It is not a verified drop-in solution for our APK: its Android-pad version is
`0x28003a35`, its iPad version is `0x18003b20`, and some Android authentication
code still builds iPad-specific metadata and invokes an iOS packer. Its README's
capability claims do not establish current live compatibility. Several other
search results were commercial API/source offerings rather than inspectable,
current standalone clients.

The Android APK is a better immediate basis than those old profiles because
it exposes the official current secondary-device UI, policy check, request
serializers, and native libraries. This investigation does not establish a
current iPad binary's exact packet format.

## Go implementation and live experiment

```sh
# Account-free QR request and one polling request; no authentication:
go run ./tools/native-probe --profile android-tablet --qr /tmp/tablet.png --poll

# Phone-approved login, with isolated local account state:
go build -o bin/WxChatAssistant .
./bin/WxChatAssistant --config-dir ~/.config/wechat-tablet login --profile android-tablet
./bin/WxChatAssistant --config-dir ~/.config/wechat-tablet read --json
./bin/WxChatAssistant --config-dir ~/.config/wechat-tablet history --limit 50
```

Live results on 2026-10-23:

1. An authenticated MMTLS session accepted a QR request with version
   `0x28004f30`, protobuf device type `pad-android-34`, and header device ID 28.
   The verified response contained a 3,691-byte QR image. One polling request
   with full Unix seconds returned the unscanned state, status zero.
2. The user ran the CLI themselves and approved the tablet QR. Authentication
   completed, following the regional redirect to `sglong.wechat.com`. The user
   reported that their other clients remained signed in. We did not automate
   or instrument those other clients to verify their session state.
3. The tablet account synchronized **496 entries across 27 peer/conversation
   IDs**, including **121 text messages**. Other entries included images,
   stickers, app payloads, video, and system/status events. These counts do not
   imply that all 496 entries were ordinary chat messages. This was a fresh
   tablet cursor, separate from the earlier desktop cursor.
4. At the user's explicit request, a single test text was sent to a uniquely
   matched contact from the private synchronized contact records. The server
   returned a successful item receipt, matching recipient and client ID, plus
   a nonzero server message ID. No automatic send retry was performed.
   Server acceptance does not establish delivery or a read receipt.

No message contents, recipient identifiers, approval grants, or account keys
are included in this report or repository.

The implementation saves a protocol profile with each login attempt and
account. It uses profile-specific BaseRequest values, native header device
IDs, account request versions, sync device strings, and poll timestamps.
Existing state without a profile continues to use the original desktop
settings. Explicit resume/profile mismatches are rejected. Desktop and tablet
credentials/cursors remain isolated by `--config-dir`.

The experiment reused the existing signed ECDH business envelope and pinned
MMTLS transport; the server accepted them for this profile. The Java RSA
settings observed in the APK do not establish that this alternative verified
hybrid path is invalid. We did not forge Android risk/attestation data: the
opaque QR field 11 and fingerprint/device-token initialization were omitted.
They may still be required for other accounts or future server policies.

The account-free probe never saves account credentials or completes login.
The user-approved CLI login does. `read`, `history`, and `send` use the saved
profile automatically when pointed at the same config directory. The tablet
profile currently requires modern 24-byte account request/response keys; there
is no legacy CBC account-key fallback.

Remaining gaps include account-specific verification challenges, complete
Android risk metadata, automatic authorization renewal/reconnect, media
transfer, and complete history retrieval. Coexistence is an observation on one
account, rather than a universal server-policy guarantee. The working desktop
backend remains available and its default behavior has not changed.

## Reproduce the static inspection

Download the APK from the link above and verify its hash. Extract the specific
DEX before invoking JADX to avoid decompiling the entire app:

```sh
unzip -p weixin8079.apk classes5.dex > classes5.dex
jadx -r --single-class l41.j1 --single-class-output CgiCheckLoginAsPad.java classes5.dex
jadx -r --single-class nd5.a30 --single-class-output PadCheckRequest.java classes5.dex
jadx -r --single-class com.tencent.mm.plugin.login_exdevice.ui.LoginAsExDeviceUI \
  --single-class-output ExDeviceLogin.java classes5.dex
```

Other relevant class locations: `nd5.b30` in `classes2.dex`, `nd5.jp3` in
`classes4.dex`, and `com.tencent.mm.app.s7` / `kd5.ui` in `classes11.dex`.
Locate renamed equivalents through the route/log strings and the generated
protobuf serializers when inspecting another APK version.


## Additional authenticated APIs

The client now supports HTTP shortlink requests protected by the existing MMTLS session credentials, including status notifications (CGI 251), group-member details (551), quoted app messages (222), and translation (631).

The official Android 8.0.79 APK contains `/cgi-bin/micromsg-bin/gettranstext`. Its direct translation requester uses a base request in field 1, item count in field 2, repeated text items in field 3, and target language in field 4. Each item carries its ID in field 1, original text in field 2, and scene 17 in field 4. Responses contain matching item IDs, translated text, and per-item status. A live authenticated request translating `你好，世界` to `en` returned `Hello, world.`; this confirms server translation is usable without a separate translation provider. This test used an explicit phrase rather than a private chat message.

Native image download and read-state notification returned successful server responses. Other-device badge changes have not been independently checked. Quoted-reply encoding is covered by fixtures but has not been tested with a live send. Phone-profile QR request and poll work; full phone authorization remains untested.
