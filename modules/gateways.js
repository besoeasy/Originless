const fs = require("fs");
const path = require("path");

const GATEWAY_TEST_CID = "QmV2ZAJVPafPNhKjorD2v9ZnfENYDC5Be5gTKiymaCMmeN";
const GATEWAYS_PATH = path.join(__dirname, "../gateways.json");
const TEST_TIMEOUT_MS = 6000;
const REFRESH_TTL_MS = 10 * 60 * 1000; // 10 minutes

let cachedGateways = null;
let workingGateways = [];
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
    cachedGateways = ["https://dweb.link"]; // fallback
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

const refreshGateways = async () => {
  if (refreshPromise) return refreshPromise;

  refreshPromise = (async () => {
    const gateways = loadGateways();

    const results = await Promise.allSettled(
      gateways.map(async (gateway) => ({
        gateway,
        ok: await testGateway(gateway),
      }))
    );

    workingGateways = results
      .filter((result) => result.status === "fulfilled" && result.value.ok)
      .map((result) => result.value.gateway);

    lastTestAt = Date.now();

    if (workingGateways.length === 0) {
      console.warn("[GATEWAY] No working gateways detected, falling back to configured list");
      workingGateways = gateways.slice();
    }

    refreshPromise = null;
    return workingGateways;
  })();

  return refreshPromise;
};

const getRandomGateway = () => {
  const gateways = workingGateways.length ? workingGateways : loadGateways();
  if (!gateways.length) return "https://dweb.link";

  const idx = Math.floor(Math.random() * gateways.length);
  return gateways[idx];
};

const getGatewayUrl = async (cid, filename) => {
  if (Date.now() - lastTestAt > REFRESH_TTL_MS) {
    await refreshGateways();
  }

  const base = getRandomGateway().replace(/\/$/, "");
  let url = `${base}/ipfs/${cid}`;

  if (filename) {
    url += `?filename=${encodeURIComponent(filename)}`;
  }

  return url;
};

module.exports = {
  refreshGateways,
  getGatewayUrl,
};
