(() => {
  function formatBytes(bytes) {
    if (!bytes || bytes === 0) return "0 B";
    const k = 1024;
    const sizes = ["B", "KB", "MB", "GB", "TB", "PB"];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    if (i <= 0) return bytes + " B";
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + " " + sizes[i];
  }

  function formatDate(dateStr) {
    if (!dateStr) return "";
    const d = new Date(dateStr);
    return d.toLocaleString("en-US", {
      month: "short",
      day: "numeric",
      year: "numeric",
      hour: "2-digit",
      minute: "2-digit",
      hour12: false,
    });
  }

  function formatUnix(value) {
    if (!value) return "Never";
    if (typeof value === "number") {
      return formatDate(new Date(value * 1000).toISOString());
    }
    return formatDate(value);
  }

  function formatRelative(dateStr) {
    if (!dateStr) return "";
    const now = new Date();
    const date = new Date(dateStr);
    const diffSec = Math.floor((now - date) / 1000);
    if (diffSec < 45) return "just now";
    const diffMin = Math.floor(diffSec / 60);
    if (diffMin < 60) return `${diffMin}m ago`;
    const diffHour = Math.floor(diffMin / 60);
    if (diffHour < 24) return `${diffHour}h ago`;
    const diffDay = Math.floor(diffHour / 24);
    if (diffDay < 30) return `${diffDay}d ago`;
    return formatDate(dateStr);
  }

  function shortHash(hash) {
    if (!hash) return "";
    if (hash.length <= 16) return hash;
    return `${hash.slice(0, 8)}…${hash.slice(-8)}`;
  }

  function shortOwner(owner) {
    if (!owner) return "";
    const clean = owner.replace(/^ed25519:/, "");
    if (clean.length <= 14) return clean;
    return `${clean.slice(0, 6)}…${clean.slice(-6)}`;
  }

  function formatExpires(unix) {
    if (!unix) return "Never";
    const now = Math.floor(Date.now() / 1000);
    const diff = unix - now;
    if (diff <= 0) return "Expired";
    const days = Math.floor(diff / 86400);
    if (days >= 1) return `in ${days}d`;
    const hours = Math.floor(diff / 3600);
    if (hours >= 1) return `in ${hours}h`;
    const mins = Math.floor(diff / 60);
    return `in ${mins}m`;
  }

  function isBlobProtected(val) {
    if (!val) return false;
    const src = (val && typeof val === "object")
      ? (val.created_at || val.createdAt || val.last_access || val.lastAccess)
      : val;
    if (!src) return false;
    const d = new Date(src).getTime();
    if (isNaN(d)) return false;
    const diffMs = Date.now() - d;
    return diffMs < 7 * 24 * 3600 * 1000;
  }

  function canonicalJSON(obj) {
    if (obj === null || typeof obj !== "object") {
      return JSON.stringify(obj);
    }
    if (Array.isArray(obj)) {
      return "[" + obj.map(canonicalJSON).join(",") + "]";
    }
    const keys = Object.keys(obj).sort();
    return "{" + keys.map(k => JSON.stringify(k) + ":" + canonicalJSON(obj[k])).join(",") + "}";
  }

  async function createSignedRecord(collection, data, labels, ttlSeconds = 86400 * 30) {
    const keyPair = await crypto.subtle.generateKey({ name: "Ed25519" }, true, ["sign", "verify"]);
    const rawPub = await crypto.subtle.exportKey("raw", keyPair.publicKey);
    const pubHex = Array.from(new Uint8Array(rawPub)).map(b => b.toString(16).padStart(2, "0")).join("");
    const owner = "ed25519:" + pubHex;
    const now = Math.floor(Date.now() / 1000);
    const expires = now + ttlSeconds;
    const canonical = canonicalJSON(data);
    const sortedLabels = (labels || []).slice().sort();
    const msg = `${owner}:${collection}:${now}:${expires}:${canonical}:${sortedLabels.join(",")}`;
    const enc = new TextEncoder().encode(msg);
    const hashBuf = await crypto.subtle.digest("SHA-256", enc);
    const sigBuf = await crypto.subtle.sign({ name: "Ed25519" }, keyPair.privateKey, hashBuf);
    const sigHex = Array.from(new Uint8Array(sigBuf)).map(b => b.toString(16).padStart(2, "0")).join("");
    return {
      owner,
      collection,
      created_at: now,
      expires_at: expires,
      data,
      labels: sortedLabels,
      sig: sigHex,
    };
  }

  function statusDefaults() {
    return {
      version: "...",
      timestamp: "...",
      storageLimit: { configured: "Unknown", bytes: 0 },
      fileLimit: { configured: "Unknown", bytes: 0 },
      blobs: { count: 0, size: 0, sizeStr: "0 B" },
      records: { count: 0 },
      isHealthy: true,
    };
  }

  function safeGet(key) {
    try {
      return localStorage.getItem(key);
    } catch (_) {
      return null;
    }
  }

  function safeSet(key, value) {
    try {
      localStorage.setItem(key, value);
    } catch (_) {}
  }

  // Fallback shown before /status loads, and if the computed ever fails.
  // The template also uses `nodeGraphics?.x` so a stale cached app.js
  // (without this computed) can't hard-crash the whole mount.
  function fallbackNodeGraphics() {
    const color1 = "hsl(160, 88%, 62%)";
    const color2 = "hsl(200, 82%, 58%)";
    const color3 = "hsl(180, 90%, 66%)";
    return {
      hashHex: "LOADING",
      color1,
      color2,
      color3,
      color4: color2,
      glowStyle: {},
      ticks: [],
      nodes: [],
      lines: [],
      corePolygon: "",
      dna: [],
      ringDash1: "8 6 2 6",
      ringDash2: "14 10",
    };
  }

  function createOriginlessApp(options = {}) {
    const { createApp } = Vue;

    return createApp({
      setup() {
        return {
          formatBytes,
          formatDate,
          formatUnix,
          formatRelative,
          shortHash,
          shortOwner,
          formatExpires,
          isBlobProtected,
        };
      },
      data() {
        const savedTab = safeGet("ol_workspace_tab");
        return {
          activePage: options.page || "overview",

          status: statusDefaults(),

          workspaceTab: (savedTab === "blobs") ? "blobs" : "records",

          // Quick Records Data
          records: [],
          recordsCount: 0,
          recordsLoading: false,
          recordsFilterCollection: "",
          recordsFilterLabel: "",
          recordsFilterSearch: "",
          recordsSseConnected: false,
          recordsEventSource: null,
          isPublishingRecord: false,
          inspectRecord: null,
          inspectRecordModalOpen: false,

          // Binary Blobs Data
          blobs: [],
          blobsCount: 0,
          blobsTotalBytes: 0,
          blobsTotalBytesStr: "0 B",
          blobsLoading: false,
          blobsSearchQuery: "",
          blobDragOver: false,
          isUploadingBlob: false,
          lastBlobResult: null,
          inspectBlob: null,
          inspectBlobModalOpen: false,

          // Toast Alerts
          toasts: [],
        };
      },

      computed: {
        currentOrigin() {
          return window.location.origin;
        },

        originUrl() {
          try {
            return (window.location && window.location.origin) || "";
          } catch (_) {
            return "";
          }
        },

        storagePercentage() {
          const st = (this && this.status) || {};
          const max = (st.storageLimit && st.storageLimit.bytes) || 0;
          if (!max || max === 0) return 0;
          const pct = ((this.blobsTotalBytes || 0) / max) * 100;
          return Math.min(100, Math.max(0, Math.round(pct * 10) / 10));
        },

        storageGaugeClass() {
          if (this.storagePercentage >= 90) return "is-critical";
          if (this.storagePercentage >= 75) return "is-warn";
          return "";
        },

        nodeGraphics() {
          try {
            const st = (this && this.status) || {};
            // Stable-per-node identity seed derived from the deployment origin.
            const id = (window.location && window.location.origin) || "originless.local";

            // FNV-1a 32-bit hash
            let seed = 2166136261 >>> 0;
            for (let i = 0; i < id.length; i++) {
              seed ^= id.charCodeAt(i);
              seed = Math.imul(seed, 16777619) >>> 0;
            }

            // Mulberry32 deterministic PRNG
            let prngState = seed;
            const rand = () => {
              let t = prngState += 0x6D2B79F5;
              t = Math.imul(t ^ (t >>> 15), t | 1);
              t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
              return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
            };

            const baseHue = Math.floor(rand() * 360);
            const scheme = Math.floor(rand() * 4);
            let hue2, hue3, hue4;
            if (scheme === 0) {
              hue2 = (baseHue + 40) % 360;
              hue3 = (baseHue + 80) % 360;
              hue4 = (baseHue + 320) % 360;
            } else if (scheme === 1) {
              hue2 = (baseHue + 150) % 360;
              hue3 = (baseHue + 210) % 360;
              hue4 = (baseHue + 180) % 360;
            } else if (scheme === 2) {
              hue2 = (baseHue + 120) % 360;
              hue3 = (baseHue + 240) % 360;
              hue4 = (baseHue + 60) % 360;
            } else {
              hue2 = (baseHue + 90) % 360;
              hue3 = (baseHue + 180) % 360;
              hue4 = (baseHue + 270) % 360;
            }

            const color1 = `hsl(${baseHue}, 88%, 62%)`;
            const color2 = `hsl(${hue2}, 82%, 58%)`;
            const color3 = `hsl(${hue3}, 90%, 66%)`;
            const color4 = `hsl(${hue4}, 75%, 52%)`;

            // 24 perimeter hash / cipher ticks
            const ticks = [];
            for (let i = 0; i < 24; i++) {
              const angle = (i / 24) * 2 * Math.PI;
              const tickLen = 3 + Math.floor(rand() * 5);
              const rOuter = 72;
              const rInner = rOuter - tickLen;
              const x1 = Math.round((80 + rInner * Math.cos(angle)) * 10) / 10;
              const y1 = Math.round((80 + rInner * Math.sin(angle)) * 10) / 10;
              const x2 = Math.round((80 + rOuter * Math.cos(angle)) * 10) / 10;
              const y2 = Math.round((80 + rOuter * Math.sin(angle)) * 10) / 10;
              const isMajor = i % 6 === 0;
              const isMinor = i % 2 === 0;
              const stroke = isMajor ? color1 : (isMinor ? color2 : "rgba(255,255,255,0.22)");
              ticks.push({
                x1, y1, x2, y2,
                stroke,
                width: isMajor ? 1.5 : 1,
                opacity: Math.round((0.35 + rand() * 0.5) * 100) / 100,
              });
            }

            // Constellation nodes (7 to 10 vertices)
            const numNodes = 7 + Math.floor(rand() * 4);
            const nodes = [];
            for (let i = 0; i < numNodes; i++) {
              const angle = (i / numNodes) * 2 * Math.PI + (rand() - 0.5) * 0.4;
              const r = 36 + Math.floor(rand() * 20);
              const x = Math.round(80 + r * Math.cos(angle));
              const y = Math.round(80 + r * Math.sin(angle));
              const dotR = Math.round((2.2 + rand() * 1.8) * 10) / 10;
              const fill = i % 2 === 0 ? color1 : (i % 3 === 0 ? color3 : color2);
              nodes.push({ x, y, r: dotR, fill });
            }

            // Interconnecting chords
            const lines = [];
            for (let i = 0; i < nodes.length; i++) {
              const next = nodes[(i + 1) % nodes.length];
              const cross = nodes[(i + 2) % nodes.length];
              lines.push({ x1: nodes[i].x, y1: nodes[i].y, x2: next.x, y2: next.y, cross: false });
              if (i % 2 === 0) {
                lines.push({ x1: nodes[i].x, y1: nodes[i].y, x2: cross.x, y2: cross.y, cross: true });
              }
            }

            // Central cryptographic core polygon
            const coreSides = 3 + Math.floor(rand() * 5);
            const coreRotation = rand() * Math.PI;
            const coreRadius = 13 + Math.floor(rand() * 4);
            const corePoints = [];
            for (let i = 0; i < coreSides; i++) {
              const ang = coreRotation + (i / coreSides) * 2 * Math.PI;
              const px = Math.round((80 + coreRadius * Math.cos(ang)) * 10) / 10;
              const py = Math.round((80 + coreRadius * Math.sin(ang)) * 10) / 10;
              corePoints.push(`${px},${py}`);
            }

            // 5-bar Visual DNA strip
            const dna = [];
            for (let i = 0; i < 5; i++) {
              const dHue = (baseHue + Math.floor(rand() * 180) - 90 + 360) % 360;
              const dH = 8 + Math.floor(rand() * 10);
              dna.push({ color: `hsl(${dHue}, 85%, 60%)`, height: dH });
            }

            // Deterministic dash arrays for rings
            const dash1Options = ["8 6 2 6", "10 5 3 5", "12 4 4 4", "6 8"];
            const dash2Options = ["14 10", "12 8", "16 6", "8 6 2 6"];
            const ringDash1 = dash1Options[Math.floor(rand() * dash1Options.length)];
            const ringDash2 = dash2Options[Math.floor(rand() * dash2Options.length)];

            const hashHex = seed.toString(16).padStart(8, "0").toUpperCase();

            return {
              hashHex,
              color1,
              color2,
              color3,
              color4,
              glowStyle: {
                background: `radial-gradient(circle, ${color1}2e 0%, ${color2}15 48%, transparent 70%)`
              },
              ticks,
              nodes,
              lines,
              corePolygon: corePoints.join(" "),
              dna,
              ringDash1,
              ringDash2,
            };
          } catch (_) {
            return fallbackNodeGraphics();
          }
        },

        storageBreakdown() {
          const st = (this && this.status) || {};
          const maxBytes = (st.storageLimit && st.storageLimit.bytes) || (100 * 1024 * 1024 * 1024);
          const usedBytes = this.blobsTotalBytes || 0;
          const thresholdPct = 75;
          const thresholdBytes = maxBytes * (thresholdPct / 100);
          const headroomBytes = Math.max(0, thresholdBytes - usedBytes);

          const usedPct = maxBytes > 0 ? (usedBytes / maxBytes) * 100 : 0;
          const visualUsedPct = usedBytes > 0 ? Math.max(1.8, usedPct) : 0;

          const isOverThreshold = usedBytes >= thresholdBytes;

          return {
            usedBytes,
            headroomBytes,
            usedStr: formatBytes(usedBytes),
            headroomStr: formatBytes(headroomBytes),
            usedPct: Math.round(visualUsedPct * 10) / 10,
            statusText: isOverThreshold ? "Evicting Over Quota" : "Operating in Safe Zone",
            isOverThreshold,
          };
        },

        filteredRecords() {
          let list = [...(this.records || [])];
          if (this.recordsFilterSearch && this.recordsFilterSearch.trim()) {
            const q = this.recordsFilterSearch.toLowerCase().trim();
            list = list.filter(r =>
              (r.id || "").toLowerCase().includes(q) ||
              (r.collection || "").toLowerCase().includes(q) ||
              (r.owner || "").toLowerCase().includes(q) ||
              (r.labels || []).some(l => l.toLowerCase().includes(q)) ||
              (r.dataPreview || "").toLowerCase().includes(q)
            );
          }
          return list;
        },

        filteredBlobs() {
          let list = [...(this.blobs || [])];
          if (this.blobsSearchQuery && this.blobsSearchQuery.trim()) {
            const q = this.blobsSearchQuery.toLowerCase().trim();
            list = list.filter(b => (b.hash || "").toLowerCase().includes(q));
          }
          return list;
        },

        generatedAgentPrompt() {
          const origin = this.currentOrigin;

          if (this.promptFormat === "markdown") {
            return `### 🌐 Originless Node Integration
- **Node Origin**: \`${origin}\`
- **Auth**: None (public primitives, signed records verified via Ed25519)

#### API Endpoints
- **Signed Records**: \`POST ${origin}/records\` (Ed25519 signed state, auto-expiring TTL)
- **Record Stream**: \`GET ${origin}/records/stream\` (Server-Sent Events)
- **Binary Blobs**: \`POST ${origin}/up\` (SHA-256 content-addressed .bin only)
- **Node Telemetry**: \`GET ${origin}/status\`

#### Skills
Make this a skill. Whenever you need to publish records or store binary blobs, use this Originless node.`;
          }

          if (this.promptFormat === "curl") {
            return `# 1. Publish a signed JSON record
curl -X POST ${origin}/records -H "Content-Type: application/json" -d '{"owner":"ed25519:...","collection":"notes","created_at":...,"expires_at":...,"data":{...},"sig":"..."}'

# 2. Store a .bin blob (content-addressed by SHA-256)
curl -X POST -F "file=@data.bin" ${origin}/up

# 3. Check node telemetry
curl ${origin}/status`;
          }

          if (this.promptFormat === "python") {
            return `import requests

ORIGIN = "${origin}"

# 1. Store a .bin blob
with open("data.bin", "rb") as f:
    r = requests.post(f"{ORIGIN}/up", files={"file": f})
    data = r.json()
    print(data["hash"])  # sha256 content address`;
          }

          return `Publish records and binary blobs with Originless.

Repo: https://github.com/besoeasy/Originless
This Originless node: ${origin}

Use this node for all record and blob operations. There is no auth.
Endpoints:
- POST ${origin}/records — a signed JSON record (Ed25519 signature verification).
- POST ${origin}/up — a content-addressed .bin blob (SHA-256), single file part "file".
- GET ${origin}/records/stream — live Server-Sent Events feed of new records.

Make this a skill.
Whenever you need to publish records or store blobs, use this Originless node.`;
        },
      },

      methods: {
        showToast(message, type = "success") {
          const id = Date.now() + Math.random();
          this.toasts.push({ id, message, type });
          setTimeout(() => {
            this.toasts = this.toasts.filter(t => t.id !== id);
          }, 3500);
        },

        async copyText(text, label = "Copied to clipboard") {
          if (!text) return;
          try {
            await navigator.clipboard.writeText(text);
            this.showToast(label, "success");
          } catch (e) {
            this.showToast("Failed to copy", "error");
          }
        },

        setWorkspaceTab(tab) {
          this.workspaceTab = tab;
          safeSet("ol_workspace_tab", tab);
          this.inspectRecord = null;
          this.inspectRecordModalOpen = false;
          this.inspectBlob = null;
          this.inspectBlobModalOpen = false;
          if (tab === "records") {
            this.fetchRecords();
            if (!this.recordsSseConnected) {
              this.connectRecordsSSE();
            }
          } else if (tab === "blobs") {
            this.fetchBlobs();
          }
        },

        async fetchStatus() {
          try {
            const res = await fetch("/status");
            const data = await res.json();
            if (data.status === "success") {
              this.status = {
                version: data.version || "0.1.0",
                timestamp: new Date(data.timestamp).toLocaleTimeString("en-US", { hour12: false }),
                storageLimit: data.storageLimit || { configured: "Unknown", bytes: 0 },
                fileLimit: data.fileLimit || { configured: "Unknown", bytes: 0 },
                blobs: data.blobs || { count: 0, size: 0, sizeStr: "0 B" },
                records: data.records || { count: 0 },
                isHealthy: true,
              };

              if (data.blobs?.count !== undefined) {
                this.blobsCount = data.blobs.count;
                this.blobsTotalBytes = data.blobs.size || 0;
                this.blobsTotalBytesStr = data.blobs.sizeStr || formatBytes(this.blobsTotalBytes);
              }
              if (data.records?.count !== undefined) {
                this.recordsCount = data.records.count;
              }
            }
          } catch (err) {
            console.error("Fetch status error:", err);
            this.status.isHealthy = false;
          }
        },

        async fetchRecords() {
          this.recordsLoading = true;
          try {
            const params = new URLSearchParams({ limit: "50" });
            if (this.recordsFilterCollection) params.set("collection", this.recordsFilterCollection);
            if (this.recordsFilterLabel) params.set("label", this.recordsFilterLabel);
            if (this.recordsFilterSearch) params.set("search", this.recordsFilterSearch);
            const res = await fetch(`/records?${params.toString()}`);
            const data = await res.json();
            if (res.ok && data.status === "success") {
              this.records = (data.records || []).map(r => ({
                ...r,
                dataFormatted: typeof r.data === "string" ? r.data : JSON.stringify(r.data, null, 2),
                dataPreview: typeof r.data === "string" ? r.data : JSON.stringify(r.data),
              }));
              this.recordsCount = data.count || this.records.length;
            }
          } catch (err) {
            console.error("Failed to fetch records:", err);
          } finally {
            this.recordsLoading = false;
          }
        },

        fetchRecordsDebounced() {
          clearTimeout(this._recSearchTimer);
          this._recSearchTimer = setTimeout(() => {
            this.fetchRecords();
          }, 300);
        },

        connectRecordsSSE() {
          if (this.recordsEventSource) {
            try { this.recordsEventSource.close(); } catch (_) {}
          }
          try {
            const sse = new EventSource("/records/stream");
            this.recordsEventSource = sse;
            sse.onopen = () => {
              this.recordsSseConnected = true;
            };
            sse.onmessage = (event) => {
              try {
                const rec = JSON.parse(event.data);
                const enriched = {
                  ...rec,
                  isNew: true,
                  dataFormatted: typeof rec.data === "string" ? rec.data : JSON.stringify(rec.data, null, 2),
                  dataPreview: typeof rec.data === "string" ? rec.data : JSON.stringify(rec.data),
                };
                if (!this.records.some(r => r.id === rec.id)) {
                  this.records.unshift(enriched);
                  this.recordsCount++;
                  this.showToast(`New live record in "${rec.collection}"`, "success");
                  setTimeout(() => { enriched.isNew = false; }, 3000);
                }
              } catch (e) {
                console.error("SSE parse error:", e);
              }
            };
            sse.onerror = () => {
              this.recordsSseConnected = false;
            };
          } catch (err) {
            console.error("SSE connection error:", err);
            this.recordsSseConnected = false;
          }
        },

        async publishDemoRecord() {
          this.isPublishingRecord = true;
          try {
            const collections = ["gamesaves", "alerts", "chat", "agents"];
            const chosenCol = this.recordsFilterCollection || collections[Math.floor(Math.random() * collections.length)];
            const sampleData = {
              slot: 1,
              level: Math.floor(Math.random() * 50) + 1,
              score: Math.floor(Math.random() * 10000),
              timestamp: Date.now(),
              message: "Demo state update from Originless Dashboard",
            };
            const sampleLabels = [`topic:${chosenCol}`, `client:web`, `ping:${Math.floor(Math.random() * 1000)}`];

            let recordPayload;
            if (window.crypto && window.crypto.subtle && window.crypto.subtle.generateKey) {
              try {
                recordPayload = await createSignedRecord(chosenCol, sampleData, sampleLabels);
              } catch (e) {
                console.warn("Native Web Crypto Ed25519 error:", e);
              }
            }

            if (!recordPayload) {
              throw new Error("Web Crypto Ed25519 signing is not supported in this browser. Please use curl / API to publish records.");
            }

            const res = await fetch("/records", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify(recordPayload),
            });
            const data = await res.json();
            if (res.ok && data.status === "success") {
              this.showToast(`Published demo record to "${chosenCol}"!`, "success");
              this.fetchRecords();
              this.fetchStatus();
            } else {
              throw new Error(data.error || "Failed to publish demo record");
            }
          } catch (err) {
            console.error("Demo record error:", err);
            this.showToast(err.message, "error");
          } finally {
            this.isPublishingRecord = false;
          }
        },

        openInspectRecord(rec) {
          this.inspectRecord = rec;
          this.inspectRecordModalOpen = true;
        },

        closeInspectRecord() {
          this.inspectRecord = null;
          this.inspectRecordModalOpen = false;
        },

        async fetchBlobs() {
          this.blobsLoading = true;
          try {
            const res = await fetch("/blobs?limit=50");
            const data = await res.json();
            if (res.ok && data.status === "success") {
              this.blobs = (data.blobs || []).map((b) => ({
                ...b,
                sizeStr: formatBytes(b.size),
                ageLabel: formatRelative(b.created_at),
                dateLabel: formatDate(b.created_at),
              }));
              this.blobsCount = data.count || this.blobs.length;
              this.blobsTotalBytes = data.total_bytes || 0;
              this.blobsTotalBytesStr = data.total_bytes_str || formatBytes(this.blobsTotalBytes);
            }
          } catch (err) {
            console.error("Fetch blobs error:", err);
          } finally {
            this.blobsLoading = false;
          }
        },

        triggerBlobFileInput() {
          if (this.$refs.blobFileInput) {
            this.$refs.blobFileInput.click();
          }
        },

        handleBlobFileSelect(event) {
          const file = event.target.files && event.target.files[0];
          if (file) {
            this.uploadBlobFile(file);
          }
        },

        handleBlobDrop(event) {
          this.blobDragOver = false;
          const dt = event.dataTransfer;
          const file = dt && dt.files && dt.files[0];
          if (file) {
            this.uploadBlobFile(file);
          }
        },

        async uploadBlobFile(file) {
          if (!file) return;
          if (!file.name.toLowerCase().endsWith(".bin")) {
            this.showToast("Only .bin files are accepted by /up", "error");
            return;
          }

          this.isUploadingBlob = true;
          this.lastBlobResult = null;
          const formData = new FormData();
          formData.append("file", file);

          try {
            const res = await fetch("/up", {
              method: "POST",
              body: formData,
            });
            const data = await res.json();
            if (res.ok && data.status === "success") {
              this.lastBlobResult = data;
              this.showToast(`Saved blob "${file.name}"!`, "success");
              this.fetchBlobs();
              this.fetchStatus();
            } else {
              throw new Error(data.message || data.error || "Upload failed");
            }
          } catch (err) {
            console.error("Blob upload error:", err);
            this.showToast(err.message, "error");
          } finally {
            this.isUploadingBlob = false;
            if (this.$refs.blobFileInput) {
              this.$refs.blobFileInput.value = "";
            }
          }
        },

        openInspectBlob(blob) {
          this.inspectBlob = blob;
          this.inspectBlobModalOpen = true;
        },

        closeInspectBlob() {
          this.inspectBlob = null;
          this.inspectBlobModalOpen = false;
        },
      },

      mounted() {
        this.fetchStatus();
        this.fetchRecords();
        this.connectRecordsSSE();
        this.fetchBlobs();

        // Live polling
        this._statusTimer = setInterval(() => this.fetchStatus(), 8000);
        this._blobsTimer = setInterval(() => this.fetchBlobs(), 15000);
        this._recordsTimer = setInterval(() => this.fetchRecords(), 30000);
      },

      beforeUnmount() {
        clearInterval(this._statusTimer);
        clearInterval(this._blobsTimer);
        clearInterval(this._recordsTimer);
        if (this.recordsEventSource) {
          try { this.recordsEventSource.close(); } catch (_) {}
        }
      },
    });
  }

  window.Originless = {
    formatBytes,
    formatDate,
    formatUnix,
    formatRelative,
    createOriginlessApp,
  };
})();