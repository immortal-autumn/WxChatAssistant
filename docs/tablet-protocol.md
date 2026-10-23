# Android tablet / iPad protocol investigation

Investigated 2026-10-23 to find a standalone login mode that can potentially
coexist with an official desktop session. **A real Android tablet login mode
exists in the inspected official APK. Standalone tablet authentication and
three-device coexistence have not been live verified here.** The CLI still uses
the Linux desktop profile; there is no working `--tablet` switch yet.

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
This confirms a separate native setting in addition to the protobuf device
string. Its complete use in packet packing has not yet been traced; these
addresses are not an implementation of the tablet wire format.

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
The exact native authentication/risk-data construction still needs tracing.

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

## Consequences for this Go CLI

Android-tablet mode is a credible route to investigate for desktop coexistence.
The presence of a separate device class and secondary-device UI is strong
implementation evidence; **the session-slot policy for this account remains a
live-test question**. Phone-plus-tablet operation alone does not prove that the
same account can simultaneously retain its desktop session.

The next implementation needs to:

1. Model protocol profiles per attempt and saved account, including version,
   device type, packet policy, keys, and timestamps. Keep the working desktop
   account and cursor separate from a tablet attempt.
2. Trace native packing for device ID 28, Android pre-login key handling, and the
   opaque QR/auth metadata. Add verified request/response fixtures before
   advertising a tablet login command.
3. Exercise account-free eligibility/QR calls with correct envelopes and
   authenticated server verification. A QR response alone would not establish
   successful tablet authorization.
4. Perform one phone-approved tablet login while an official desktop session is
   active, then verify incoming sync on both and restart behavior. Observe
   actual session displacement rather than inferring coexistence from labels.

No tablet QR was approved, no current credentials were modified, and no message
was sent during this investigation. The existing `wechat login` behavior is unchanged.

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
