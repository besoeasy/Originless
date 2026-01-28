const fs = require("fs");
const path = require("path");

const GATEWAY_TEST_CID = "QmV2ZAJVPafPNhKjorD2v9ZnfENYDC5Be5gTKiymaCMmeN";
const GATEWAYS_PATH = path.join(__dirname, "../gateways.json");
const TEST_TIMEOUT_MS = 6000;
const REFRESH_TTL_MS = 60 * 1000; // 1 minute
const FALLBACK_GATEWAY = "https://dweb.link";

let cachedGateways = null;
let selectedGateway = "";
let lastTestAt = 0;
let refreshPromise = null;

const fetchWithTimeout = async (url, options = {}, timeoutMs = TEST_TIMEOUT_MS) => {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), timeoutMs);

  try {
    return await fetch(url, { ...options, signal: controller.signal });
  } finally {
    clearTimeout(timeout);
  }
};

const loadGateways = () => {
  if (cachedGateways) return cachedGateways;

  try {
    const raw = fs.readFileSync(GATEWAYS_PATH, "utf8");
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) {
      throw new Error("gateways.json must be an array");
    }

    cachedGateways = parsed
      .map((entry) => String(entry).trim())
      .filter((entry) => entry && entry.startsWith("http"));
  } catch (err) {
    console.warn(`[GATEWAY] Failed to read gateways.json: ${err.message}`);
    cachedGateways = [FALLBACK_GATEWAY]; // fallback
  }

  return cachedGateways;
};

const testGateway = async (baseUrl) => {
  const base = baseUrl.replace(/\/$/, "");
  const url = `${base}/ipfs/${GATEWAY_TEST_CID}`;

  try {
    const res = await fetchWithTimeout(url, { method: "GET", headers: { "accept": "application/json" } });
    if (!res.ok) return false;

    const json = await res.json().catch(() => null);
    return Boolean(json && json.check === true);
  } catch (err) {
    return false;
  }
};

const shuffle = (list) => {
  const copy = list.slice();
  for (let i = copy.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [copy[i], copy[j]] = [copy[j], copy[i]];
  }
  return copy;
};

const refreshGateways = async () => {
  if (refreshPromise) return refreshPromise;

  refreshPromise = (async () => {
    const gateways = shuffle(loadGateways());
    let selected = "";

    for (const gateway of gateways) {
      const ok = await testGateway(gateway);
      if (ok) {
        selected = gateway;
        break;
      }
    }

    if (!selected) {
      console.warn("[GATEWAY] All gateways failed. Falling back to dweb.link");
      selected = FALLBACK_GATEWAY;
    }

    selectedGateway = selected;
    lastTestAt = Date.now();
    refreshPromise = null;

    return selectedGateway;
  })();

  return refreshPromise;
};

const getSelectedGateway = () => {
  if (selectedGateway) return selectedGateway;
  const gateways = loadGateways();
  return gateways[0] || FALLBACK_GATEWAY;
};

const getGatewayUrl = async (cid, filename) => {
  if (Date.now() - lastTestAt > REFRESH_TTL_MS) {
    await refreshGateways();
  }

  const base = getSelectedGateway().replace(/\/$/, "");
  let url = `${base}/ipfs/${cid}`;

  if (filename) {
    url += `?filename=${encodeURIComponent(filename)}`;
  }

  return url;
};

module.exports = {
  refreshGateways,
  getGatewayUrl,
  getSelectedGateway,
};
