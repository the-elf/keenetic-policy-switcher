"use strict";

const AUTO_REFRESH_MS = 15000;

const state = {
  policies: [], // [{id, name}]
  devices: [],  // [{mac, name, ip, online, policy_id}]
};

const elements = {
  list: document.getElementById("device-list"),
  loading: document.getElementById("loading"),
  offlineBanner: document.getElementById("offline-banner"),
  refreshBtn: document.getElementById("refresh-btn"),
  toastContainer: document.getElementById("toast-container"),
  cardTemplate: document.getElementById("device-card-template"),
};

async function fetchJSON(url, options) {
  const res = await fetch(url, options);
  let body = null;
  try {
    body = await res.json();
  } catch (_) {
    // body can be empty on a network error — that's an error too, handled below
  }
  if (!res.ok) {
    const message = (body && body.error) || `HTTP ${res.status}`;
    throw new Error(message);
  }
  return body;
}

function showToast(message, kind) {
  const toast = document.createElement("div");
  toast.className = "toast" + (kind ? ` toast--${kind}` : "");
  toast.textContent = message;
  elements.toastContainer.appendChild(toast);
  setTimeout(() => toast.remove(), 3500);
}

function policyName(policyId) {
  const p = state.policies.find((p) => p.id === policyId);
  return p ? p.name : policyId;
}

function renderDevices() {
  elements.list.innerHTML = "";

  if (state.devices.length === 0) {
    const hint = document.createElement("p");
    hint.className = "hint";
    hint.textContent = "No devices found.";
    elements.list.appendChild(hint);
    return;
  }

  for (const device of state.devices) {
    elements.list.appendChild(renderDeviceCard(device));
  }
}

// deviceType guesses an icon category from the host name: the router
// doesn't expose a device type (see docs/api-notes.md), so we work with
// what we have — the DHCP/admin-panel name. An unrecognized name falls
// back to a neutral icon.
const DEVICE_TYPE_PATTERNS = [
  ["phone", /iphone|android|phone|pixel|galaxy|redmi|xiaomi|honor/i],
  ["tv", /\btv\b|roku|chromecast|firestick|appletv|smart-?tv/i],
  ["laptop", /macbook|laptop|notebook|thinkpad|imac|desktop|\bpc\b/i],
];

function deviceType(device) {
  const haystack = `${device.name || ""} ${device.mac || ""}`;
  for (const [type, pattern] of DEVICE_TYPE_PATTERNS) {
    if (pattern.test(haystack)) {
      return type;
    }
  }
  return "device";
}

function renderDeviceCard(device) {
  const node = elements.cardTemplate.content.cloneNode(true);
  const card = node.querySelector(".device-card");
  const status = node.querySelector(".device-card__status");
  const name = node.querySelector(".device-card__name");
  const meta = node.querySelector(".device-card__meta");
  const select = node.querySelector(".device-card__policy");
  const icon = node.querySelector(".device-card__icon use");

  icon.setAttribute("href", `#icon-${deviceType(device)}`);
  card.dataset.mac = device.mac;
  status.classList.toggle("device-card__status--online", device.online);
  status.title = device.online ? "online" : "offline";
  name.textContent = device.name || device.mac;
  meta.textContent = `${device.ip} · ${device.mac}`;

  for (const policy of state.policies) {
    const option = document.createElement("option");
    option.value = policy.id;
    option.textContent = policy.name;
    select.appendChild(option);
  }
  select.value = device.policy_id;

  select.addEventListener("change", () => onPolicyChange(device, select));

  return node;
}

async function onPolicyChange(device, select) {
  const previousValue = device.policy_id;
  const newValue = select.value;

  select.disabled = true;
  try {
    const result = await fetchJSON(`/api/devices/${encodeURIComponent(device.mac)}/policy`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ policy_id: newValue }),
    });
    device.policy_id = result.policy_id;
    showToast(`Policy changed and saved: ${policyName(result.policy_id)}`, "success");
  } catch (err) {
    select.value = previousValue;
    showToast(`Failed to change policy: ${err.message}`, "error");
  } finally {
    select.disabled = false;
  }
}

async function loadAll({ silent } = {}) {
  if (!silent) {
    elements.loading.hidden = false;
  }

  // allSettled, not all: when the router is unreachable, /api/policies
  // answers 502 while /api/devices answers 200 with router_online:false.
  // With Promise.all, the first rejection would cancel the second, and the
  // "router offline" banner would never show — exactly the scenario it
  // exists for (spec §7.5).
  const [policiesRes, devicesRes] = await Promise.allSettled([
    fetchJSON("/api/policies"),
    fetchJSON("/api/devices"),
  ]);

  if (policiesRes.status === "fulfilled") {
    state.policies = policiesRes.value.policies || [];
  }

  let routerOnline = false;
  if (devicesRes.status === "fulfilled") {
    routerOnline = devicesRes.value.router_online !== false;
    // When the router is unreachable the list comes back empty — don't let
    // it wipe the last known data; the banner already warns about this.
    if (routerOnline) {
      state.devices = devicesRes.value.devices || [];
    }
  }

  elements.offlineBanner.hidden = routerOnline;
  elements.loading.hidden = true;
  renderDevices();

  const failure = [policiesRes, devicesRes].find((r) => r.status === "rejected");
  if (failure && !silent && routerOnline) {
    showToast(`Failed to load data: ${failure.reason.message}`, "error");
  }
}

async function refresh() {
  elements.refreshBtn.classList.add("spinning");
  try {
    await loadAll();
  } finally {
    elements.refreshBtn.classList.remove("spinning");
  }
}

elements.refreshBtn.addEventListener("click", refresh);

loadAll();
setInterval(() => loadAll({ silent: true }), AUTO_REFRESH_MS);
