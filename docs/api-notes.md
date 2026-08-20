# Keenetic RCI notes

The RCI API is not officially documented, and response shapes vary between
KeeneticOS versions. Everything below was captured from a live router during
initial API exploration, using the same login flow the app itself implements.
Sanitized copies of the responses, used by the tests, live in
`internal/keenetic/testdata/`.

## Router

- Model: **Keenetic Giga (KN-1010)**, `hw_id=KN-1010`, arch `mips`.
- Firmware: **release `4.03.C.6.3-9`**, `title 4.3.6.3`.
- Source: `GET /rci/show/version`.

## 1. Authentication

Two-step challenge-response:

1. `GET /auth` on an unauthenticated session → `401` with the headers
   `X-NDM-Realm` (`"Keenetic Giga"`) and `X-NDM-Challenge` (a random string).
2. `md5 = MD5("<login>:<realm>:<password>")` (hex).
3. `sha = SHA256("<challenge>" + md5)` (hex).
4. `POST /auth` with `{"login": "<login>", "password": "<sha>"}` → `200`, and the
   session is established via `Set-Cookie`. A standard `cookiejar` handles the
   rest.

`GET /auth` on an already-authenticated session answers `200` — that is a normal
case, not an error.

## 2. Device list — `GET /rci/show/ip/hotspot/host`

Returns **all** registered hosts (45 in the captured sample), not just the online
ones. Fields actually present:

- `mac` — the MAC address; the key used for matching.
- `name` — human-readable name. **For offline hosts it equals the MAC address**
  (there is no separate "unknown" marker — worth keeping in mind when rendering).
- `hostname` — the raw DHCP/system name; may differ from `name` and may be an
  empty string for offline hosts.
- `ip` — IP address; `"0.0.0.0"` for offline hosts.
- `active` — **this is the online/offline flag** (bool). Offline hosts have
  `active: false` and `link: "down"`.
- `via` — duplicates the MAC.
- A pile of Wi-Fi-specific fields (`rssi`, `mode`, `ssid`, `txrate`, `ap`, …),
  present only for active wireless clients and not needed here.

**There is no `policy` field in this response at all** — the current policy has
to come from somewhere else.

## 3. Current host policy — `GET /rci/show/rc/ip/hotspot/host`

A separate, shorter running-config branch (44 entries for 45 hosts in the sample;
one host has no entry here at all because it never received an explicit setting).
Fields:

- `mac`
- `permit` (bool) — whether internet access is allowed.
- `access` — the string `"permit"`, a textual duplicate of `permit`; unused.
- `policy` — **present only when a policy is explicitly assigned to the host**
  (31 of 44 entries in the sample; the other 13 have no such field, meaning the
  host runs on the default policy).

So `ListDevices` takes the list, name, IP and online flag from
`/rci/show/ip/hotspot/host`, then looks each MAC up in a map built from
`/rci/show/rc/ip/hotspot/host`. No entry, or an entry without a `policy` field,
means `policy_id = "default"`.

## 4. Policy list — `GET /rci/show/rc/ip/policy`

Returns an object (not an array) keyed by the router's internal `PolicyN` names:

```json
{
  "Policy0": { "description": "With VPN", "permit": [...] },
  "Policy1": { "description": "ru_vpn", "permit": [...] },
  "Policy2": { "description": "No VPN", "permit": [...] }
}
```

(`description` values translated from the router owner's original Russian-language
names for readability — the field itself just holds whatever free-text name was
typed into the admin panel.)

`description` is the human-readable name shown in the admin panel. `permit` is
the policy's interface list, not needed here.

`GET /rci/show/ip/policy` returns the same data plus live routing tables
(`route4`, `table4`, `mark`) — more than required, so `/rci/show/rc/ip/policy` is
used as the lighter and sufficient source.

This router has three policies, and none of them is named `Policy0`/`Policy1` in
its description — so **never treat the numeric suffix as carrying meaning**; the
display name comes strictly from `description`.

`GET /rci/show/rc` (the whole running-config in one blob) returned an empty `{}`,
so that path is unusable here.

## 5. Writing a policy and saving

Confirmed on the live device. A batch to `POST /rci/`:

```json
[
  { "ip": { "hotspot": { "host": { "mac": "<MAC>", "permit": true, "policy": "Policy2" } } } },
  { "system": { "configuration": { "save": {} } } }
]
```

The save command is mandatory — without it the change is lost on reboot. Sending
both commands in one batch also avoids losing the write if power is cut between
two separate requests.

Live check: `fa:7e:db:a1:87:db` (elf_iphone) was switched `Policy0` → `Policy2`,
confirmed visually on the device (VPN dropped), re-read from
`/rci/show/rc/ip/hotspot/host` where `policy` had indeed become `"Policy2"`, then
switched back to `Policy0` and confirmed by re-reading again.

### Successful write response

```json
[
  {
    "ip": { "hotspot": { "host": {
      "permit": { "status": [{ "status": "message", "code": "19007440",
        "ident": "Hotspot::Manager",
        "message": "rule \"permit\" applied to host \"fa:7e:db:a1:87:db\"." }] },
      "policy": { "status": [{ "status": "message", "code": "19007840",
        "ident": "Hotspot::Manager",
        "message": "policy \"Policy2\" applied to host \"fa:7e:db:a1:87:db\"." }] }
    } } }
  },
  {
    "system": { "configuration": { "save": {
      "status": [{ "status": "message", "code": "8912996",
        "ident": "Core::System::StartupConfig",
        "message": "saving (http/rci)." }]
    } } }
  }
]
```

The field that distinguishes success from failure is `status.status`. In the
confirmed successful case it is `"message"`. The failure shape (`"error"`) was
not reproduced live — breaking a real configuration just to observe it was not
worth it — but the
`{ "status": [{ "status": "...", "code": "...", "message": "..." }] }` structure
is common to RCI write responses. The client must inspect the `status` of every
entry rather than trusting the HTTP code: the router can answer `200` while the
body carries `status: "error"`.

## Implementation summary

- `ListDevices` merges `/rci/show/ip/hotspot/host` (name, IP, online) with
  `/rci/show/rc/ip/hotspot/host` (policy and permit, keyed by MAC); a missing
  entry or a missing `policy` field means the default policy.
- `ListPolicies` reads `/rci/show/rc/ip/policy`, uses `description` as the
  display name, and prepends a synthetic `{"id": "default", "name": "Default"}`.
- `SetPolicy` issues a single `POST /rci/` batch (policy write + save). Resetting
  to the default is `"policy": {"no": true}` — this follows the RCI convention of
  `{"no": true}` for clearing a configuration node, and was not verified live on
  its own.
- Response checking walks the whole body; any nested `status[].status` other than
  `"message"` is treated as a failure.
