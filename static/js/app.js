(() => {
  // Built-in public IPFS gateways for content resolution (Originless decouples HTTP
  // fetching to public gateways or dedicated Rainbow instances).
  const PUBLIC_GATEWAY = "https://inbrowser.link/ipfs/";
  const GATEWAYS = [
    { label: "inbrowser.link", url: PUBLIC_GATEWAY },
    { label: "ipfs.io (Official)", url: "https://ipfs.io/ipfs/" },
  ];

  function migrateSavedGateway(url) {
    if (!url) return url;
    if (url === "https://dweb.link/ipfs/" || url === "https://dweb.link/ipfs" || url.includes("/ipfs/")) {
      // If previously pointed to a local /ipfs/ route, migrate to default public gateway
      if (url.includes("127.0.0.1") || url.includes("localhost")) {
        return PUBLIC_GATEWAY;
      }
    }
    if (url === "https://dweb.link/ipfs/" || url === "https://dweb.link/ipfs") {
      return PUBLIC_GATEWAY;
    }
    return url;
  }

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

  function getFileCategory(filename = "", mime = "") {
    const fn = (filename || "").toLowerCase();
    if (mime.startsWith("image/") || /\.(jpg|jpeg|png|gif|webp|svg|bmp|ico|avif)$/.test(fn)) {
      return "image";
    }
    if (mime.startsWith("video/") || /\.(mp4|webm|mkv|mov|avi|m4v)$/.test(fn)) {
      return "video";
    }
    if (mime.startsWith("audio/") || /\.(mp3|wav|ogg|flac|m4a|aac)$/.test(fn)) {
      return "audio";
    }
    if (/\.(zip|tar|gz|7z|rar|bz2)$/.test(fn) || fn === "folder") {
      return "archive";
    }
    if (/\.(html|htm|js|ts|jsx|tsx|css|json|go|py|rs|c|cpp|md|sh|yml|yaml|sql|wasm)$/.test(fn)) {
      return "code";
    }
    return "file";
  }

  function itemMime(item) {
    return (item && (item.type || item.mime)) || "";
  }

  function itemCategory(item) {
    if (!item) return "file";
    return getFileCategory(item.filename, itemMime(item));
  }

  function shortCid(cid) {
    if (!cid) return "";
    if (cid.length <= 18) return cid;
    return `${cid.slice(0, 8)}…${cid.slice(-8)}`;
  }

  function shortName(name, cid) {
    const value = name || cid || "Untitled";
    if (value.length <= 12) return value;
    return `${value.slice(0, 4)}....${value.slice(-4)}`;
  }

  function gatewayUrlFor(gateway, cid, filename) {
    if (!cid) return "";
    const base = `${gateway || ""}${cid}`;
    if (!filename) return base;
    return `${base}?filename=${encodeURIComponent(filename)}`;
  }

  function ipfsUrlFor(cid, filename) {
    if (!cid) return "";
    if (!filename) return `ipfs://${cid}`;
    return `ipfs://${cid}?filename=${encodeURIComponent(filename)}`;
  }

  // Precompute fields so in-DOM templates never call helpers inside v-for.
  // Vue's browser compiler + v-for can fail to resolve methods like shortCid.
  function presentItem(item, gateway, brokenThumbs) {
    if (!item) return item;
    const category = itemCategory(item);
    const cid = item.cid || "";
    const thumbs = brokenThumbs || {};
    return {
      ...item,
      category,
      cidShort: shortCid(cid),
      nameShort: shortName(item.filename, cid),
      sizeLabel: formatBytes(item.size),
      ageLabel: formatRelative(item.created_at),
      dateLabel: formatDate(item.created_at),
      gatewayUrl: gatewayUrlFor(gateway, cid, item.filename),
      ipfsUrl: ipfsUrlFor(cid, item.filename),
      thumbSrc: cid ? `${gateway || ""}${cid}` : "",
      showThumb: category === "image" && !!cid && !thumbs[cid],
      isVideo: category === "video",
      isAudio: category === "audio",
    };
  }

  function isFolderFileList(files) {
    if (!files || files.length === 0) return false;
    if (files.length > 1) return true;
    const f = files[0];
    const rel = f.relativePath || f.webkitRelativePath || "";
    return rel.includes("/");
  }

  function walkEntry(entry, prefix, out) {
    return new Promise((resolve, reject) => {
      if (!entry) {
        resolve();
        return;
      }
      if (entry.isFile) {
        entry.file((file) => {
          const rel = prefix ? `${prefix}${file.name}` : file.name;
          try {
            Object.defineProperty(file, "webkitRelativePath", { value: rel });
          } catch (_) {}
          file.relativePath = rel;
          out.push(file);
          resolve();
        }, reject);
        return;
      }
      if (entry.isDirectory) {
        const reader = entry.createReader();
        const dirPrefix = `${prefix}${entry.name}/`;
        const next = () => {
          reader.readEntries(async (ents) => {
            if (!ents.length) {
              resolve();
              return;
            }
            try {
              for (const child of ents) {
                await walkEntry(child, dirPrefix, out);
              }
              next();
            } catch (err) {
              reject(err);
            }
          }, reject);
        };
        next();
        return;
      }
      resolve();
    });
  }

  async function filesFromDataTransfer(dt) {
    const items = dt && dt.items;
    if (items && items.length && typeof items[0].webkitGetAsEntry === "function") {
      const entries = [];
      for (let i = 0; i < items.length; i++) {
        const entry = items[i].webkitGetAsEntry && items[i].webkitGetAsEntry();
        if (entry) entries.push(entry);
      }
      if (entries.length) {
        const files = [];
        for (const entry of entries) {
          await walkEntry(entry, "", files);
        }
        if (files.length) return files;
      }
    }
    return Array.from((dt && dt.files) || []);
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
      nodeId: "...",
      fullNodeId: "",
      bandwidthIn: "0 B",
      bandwidthOut: "0 B",
      bandwidthRate: "0 B / 0 B",
      bandwidthRateIn: "0 B",
      bandwidthRateOut: "0 B",
      repoSize: "...",
      repoObjects: "0",
      version: "...",
      timestamp: "...",
      peerscount: 0,
      storageLimit: "Unknown",
      fileLimit: "Unknown",
      repoSizeBytes: 0,
      storageMaxBytes: 0,
      isHealthy: true,
      blobs: { count: 0, size: 0, sizeStr: "0 B" },
      records: { count: 0 },
    };
  }

  function pinDefaults() {
    return { count: 0, size: 0, sizeStr: "0 B", threshold: 75 };
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
          shortCid,
          shortHash,
          shortOwner,
          formatExpires,
          isBlobProtected,
          getFileCategory,
          fileKind: itemCategory,
        };
      },
      data() {
        const savedGateway = migrateSavedGateway(safeGet("ol_gateway_url"));
        if (savedGateway && savedGateway !== safeGet("ol_gateway_url")) {
          safeSet("ol_gateway_url", savedGateway);
        }
        return {
          activePage: options.page || "overview",
          
          gateways: GATEWAYS.slice(),
          currentGateway: savedGateway || PUBLIC_GATEWAY,
          gatewayEnabled: false,

          status: statusDefaults(),
          pinStats: pinDefaults(),
          
          history: [],
          searchQuery: "",
          statusFilter: "all", 
          typeFilter: "all",
          sortBy: "date-desc",
          
          activeTab: "pin", // pin, prompt
          workspaceTab: safeGet("ol_workspace_tab") || "records", // records, blobs, ipfs
          brokenThumbs: {},
          anonymizeMedia: safeGet("ol_anonymize_media") !== "false",

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
          
          // Single File Upload
          dragOver: false,
          isUploading: false,
          uploadProgress: 0,
          uploadSpeedStr: "",
          currentUploadFile: null,
          lastUploadResult: null,

          // Folder Upload
          folderFilesCount: 0,
          folderTotalSize: 0,
          isUploadingFolder: false,
          lastFolderResult: null,

          // Agent Prompt Config
          promptFormat: "plain", // plain, markdown, curl, python
          
          // Content Inspection Modal
          inspectModalOpen: false,
          inspectItem: null,
          inspectQrUrl: "",

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

        gatewayHost() {
          try {
            return new URL(this.currentGateway).hostname;
          } catch (_) {
            return this.currentGateway;
          }
        },

        storagePercentage() {
          const st = (this && this.status) || {};
          if (!st.storageMaxBytes || st.storageMaxBytes === 0) return 0;
          const pct = (st.repoSizeBytes / st.storageMaxBytes) * 100;
          return Math.min(100, Math.max(0, Math.round(pct * 10) / 10));
        },

        storageGaugeClass() {
          if (this.storagePercentage >= 90) return "is-danger";
          if (this.storagePercentage >= 75) return "is-warn";
          return "";
        },

        nodeGraphics() {
          try {
            const st = (this && this.status) || {};
            const id = st.fullNodeId || st.nodeId || "12D3KooWOriginlessNode000000000000000000000000000";
          
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
          const coreSides = 3 + Math.floor(rand() * 5); // 3 (triangle), 4 (diamond), 5, 6, 7
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
          const ps = (this && this.pinStats) || {};
          const repoBytes = st.repoSizeBytes || 0;
          const maxBytes = st.storageMaxBytes || (100 * 1024 * 1024 * 1024);
          const pinnedBytes = ps.size || 0;
          const overheadBytes = Math.max(0, repoBytes - pinnedBytes);
          const thresholdPct = ps.threshold || 75;
          const thresholdBytes = maxBytes * (thresholdPct / 100);
          const headroomBytes = Math.max(0, thresholdBytes - repoBytes);

          // Scaled for visual representation on a 100% bar
          const pinnedPct = maxBytes > 0 ? (pinnedBytes / maxBytes) * 100 : 0;
          const overheadPct = maxBytes > 0 ? (overheadBytes / maxBytes) * 100 : 0;
          const visualPinnedPct = pinnedBytes > 0 ? Math.max(1.8, pinnedPct) : 0;
          const visualOverheadPct = overheadBytes > 0 ? Math.max(1.2, overheadPct) : 0;

          const isOverThreshold = repoBytes >= thresholdBytes;

          return {
            pinnedBytes,
            overheadBytes,
            headroomBytes,
            pinnedStr: formatBytes(pinnedBytes),
            overheadStr: formatBytes(overheadBytes),
            headroomStr: formatBytes(headroomBytes),
            pinnedPct: Math.round(visualPinnedPct * 10) / 10,
            overheadPct: Math.round(visualOverheadPct * 10) / 10,
            statusText: isOverThreshold ? "Evicting Over Quota" : "Operating in Safe Zone",
            isOverThreshold,
          };
        },

        categoryDistribution() {
          const colors = {
            image: "#3dd68c",
            video: "#38bdf8",
            audio: "#a78bfa",
            archive: "#f59e0b",
            code: "#ec4899",
            file: "#94a3b8",
          };
          const labels = {
            image: "Images",
            video: "Videos",
            audio: "Audio",
            archive: "Archives",
            code: "Code",
            file: "Files",
          };

          const catTotals = {};
          let totalPinned = 0;

          for (const item of (this.history || [])) {
            if (item.unpinned) continue;
            const cat = itemCategory(item);
            catTotals[cat] = (catTotals[cat] || 0) + (item.size || 0);
            totalPinned += (item.size || 0);
          }

          if (totalPinned === 0) return [];

          return Object.keys(catTotals).map((cat) => ({
            category: cat,
            label: labels[cat] || "Other",
            bytes: catTotals[cat],
            sizeStr: formatBytes(catTotals[cat]),
            pct: Math.round((catTotals[cat] / totalPinned) * 100),
            color: colors[cat] || colors.file,
          })).sort((a, b) => b.bytes - a.bytes);
        },

        filteredHistory() {
          let list = [...(this.history || [])];
          
          // Search query filter
          if (this.searchQuery.trim()) {
            const q = this.searchQuery.toLowerCase().trim();
            list = list.filter((item) => 
              (item.filename || "").toLowerCase().includes(q) ||
              (item.cid || "").toLowerCase().includes(q)
            );
          }

          // Status filter
          if (this.statusFilter === "pinned") {
            list = list.filter(item => !item.unpinned);
          } else if (this.statusFilter === "unpinned") {
            list = list.filter(item => item.unpinned);
          }

          // Type filter
          if (this.typeFilter !== "all") {
            list = list.filter(item => {
              const type = getFileCategory(item.filename, item.type);
              return type === this.typeFilter;
            });
          }

          // Sorting
          if (this.sortBy === "date-desc") {
            list.sort((a, b) => new Date(b.created_at) - new Date(a.created_at));
          } else if (this.sortBy === "date-asc") {
            list.sort((a, b) => new Date(a.created_at) - new Date(b.created_at));
          } else if (this.sortBy === "size-desc") {
            list.sort((a, b) => (b.size || 0) - (a.size || 0));
          } else if (this.sortBy === "size-asc") {
            list.sort((a, b) => (a.size || 0) - (b.size || 0));
          } else if (this.sortBy === "name-asc") {
            list.sort((a, b) => (a.filename || "").localeCompare(b.filename || ""));
          } else if (this.sortBy === "name-desc") {
            list.sort((a, b) => (b.filename || "").localeCompare(a.filename || ""));
          }

          return list.map((row) => presentItem(row, this.currentGateway, this.brokenThumbs));
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

        lastPinKind() {
          if (!this.lastUploadResult) return "";
          return itemCategory(this.lastUploadResult);
        },

        generatedAgentPrompt() {
          const origin = this.currentOrigin;
          const gw = PUBLIC_GATEWAY;

          if (this.promptFormat === "markdown") {
            return `### 🌐 Originless Decentralized Node Integration
- **Node Origin**: \`${origin}\`
- **Public IPFS Gateway**: \`${gw}\` (use this, not dweb.link)
- **Auth**: None (Public P2P multi-hash swarm)

#### API Endpoints
- **Upload Single File**: \`POST ${origin}/upload\` (Multipart field: \`file\`)
- **Upload Directory / DApp**: \`POST ${origin}/uploadfolder\` (Multipart field: \`file\`, relative paths)
- **Signed Records**: \`POST ${origin}/records\` (Ed25519 authenticated state)
- **Binary Blobs**: \`POST ${origin}/up\` (SHA-256 content-addressed)
- **Node Health Probe**: \`GET ${origin}/health\`
- **Node Telemetry**: \`GET ${origin}/status\`

#### Resolved Public URLs
- Standard File: \`${gw}{cid}\`
- Static Website Root: \`${gw}{cid}/\`
- Native Multi-Hash: \`ipfs://{cid}\`

#### Make This a Skill
Make this a skill.

Whenever you need files uploaded, use this Originless node.

#### Skill Verification
To confirm you have understood this skill:
1. Create a simple HTML page that says \`Hello from <your agent name>\`.
2. Upload it to this node.
3. Send the user the link to view it: \`${gw}{cid}\`.`;
          }

          if (this.promptFormat === "curl") {
            return `# 1. Upload a single binary or file
curl -X POST -F "file=@document.pdf" ${origin}/upload

# 2. Upload full Vite/React dist static build folder
curl -X POST \\
  -F "file=@dist/index.html;filename=index.html" \\
  -F "file=@dist/assets/app.js;filename=assets/app.js" \\
  ${origin}/uploadfolder

# 3. Store a signed record
curl -X POST ${origin}/records -H "Content-Type: application/json" -d '{"owner":"ed25519:...","collection":"notes",...}'

# 4. Store a .bin blob
curl -X POST -F "file=@data.bin" ${origin}/up

# 5. Check node telemetry & swarm status
curl ${origin}/status

# Fetch the CID via a gateway like Rainbow (${gw}{cid})`;
          }

          if (this.promptFormat === "python") {
            return `import requests

ORIGIN = "${origin}"
GATEWAY = "${gw}"

# 1. Upload a file
with open("sample.png", "rb") as f:
    r = requests.post(f"{ORIGIN}/upload", files={"file": f})
    data = r.json()
    cid = data["cid"]
    print(f"Pinned: {GATEWAY}{cid}")`;
          }

          return `Publish files with Originless. Pin them to IPFS on this node, then share the CID through a gateway.

This is not limited to websites. Use it for a music file, a generated index.html report, a photo, an image gallery, a video, a PDF, a folder of mixed assets, or a full dist/ site.

Repo: https://github.com/besoeasy/Originless
This Originless node: ${origin}
Public IPFS gateway: ${gw}

Use this node for all uploads. Do not invent another host or API key. There is no auth.

Endpoints:
- POST ${origin}/upload — one file, exact bytes. Multipart field "file".
- POST ${origin}/uploadfolder — a folder (gallery, album, report, site). Multipart field name is "file" for each file; set filename to the relative path so links stay intact.
- POST ${origin}/records — signed JSON record (Ed25519 auth).
- POST ${origin}/up — content-addressed .bin blob.
- GET ${origin}/health — liveness check.

The JSON response includes "cid". That CID is the content address.

After upload, fetch via a gateway (e.g. Rainbow or public gateway):
- ${gw}{cid}            (single file)
- ${gw}{cid}/           (folder; index.html at the root is served automatically)
- ipfs://{cid}

Keep relative paths inside folders. Never wrap the CID in ${origin}/upload or a local file path.

Make this a skill.

Whenever you need files uploaded, use this Originless node.

To confirm you have understood this skill, complete this check:
1. Create a simple HTML page that says "Hello from <your agent name>".
2. Upload it to this node.
3. Send the user the link to view it: ${gw}{cid}.`;
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

        onGatewayChange() {
          safeSet("ol_gateway_url", this.currentGateway);
          const gwName = new URL(this.currentGateway).hostname;
          this.showToast(`Active Gateway set to ${gwName}`, "success");
        },

        setSort(field) {
          const desc = field + "-desc";
          const asc = field + "-asc";
          this.sortBy = this.sortBy === desc ? asc : desc;
        },

        showThumb(item) {
          if (!item || !item.cid) return false;
          if (this.brokenThumbs[item.cid]) return false;
          return itemCategory(item) === "image";
        },

        isVideoItem(item) {
          return itemCategory(item) === "video";
        },

        isAudioItem(item) {
          return itemCategory(item) === "audio";
        },

        thumbUrl(item) {
          if (!item || !item.cid) return "";
          return `${this.currentGateway}${item.cid}`;
        },

        onThumbError(cid) {
          if (!cid || this.brokenThumbs[cid]) return;
          this.brokenThumbs = { ...this.brokenThumbs, [cid]: true };
        },

        persistAnonymize() {
          safeSet("ol_anonymize_media", this.anonymizeMedia ? "true" : "false");
        },

        isImageFile(file) {
          if (!file) return false;
          if (file.type && file.type.startsWith("image/")) return true;
          return /\.(jpe?g|png|gif|webp)$/i.test(file.name || "");
        },

        uploadEndpoint(file) {
          return "/upload";
        },

        getGatewayUrl(cid, filename) {
          return gatewayUrlFor(this.currentGateway, cid, filename);
        },

        getIpfsUrl(cid, filename) {
          return ipfsUrlFor(cid, filename);
        },

        async fetchStatus() {
          try {
            const res = await fetch("/status");
            const data = await res.json();
            if (data.status === "success") {
              const fullId = data.node?.id || "";
              const shortId = fullId ? `${fullId.slice(0, 8)}...${fullId.slice(-8)}` : "...";
              
              this.status = {
                nodeId: shortId,
                fullNodeId: fullId,
                bandwidthIn: formatBytes(data.bandwidth?.totalIn || 0),
                bandwidthOut: formatBytes(data.bandwidth?.totalOut || 0),
                bandwidthRateIn: formatBytes(data.bandwidth?.rateIn || 0) + "/s",
                bandwidthRateOut: formatBytes(data.bandwidth?.rateOut || 0) + "/s",
                bandwidthRate: `↓ ${formatBytes(data.bandwidth?.rateIn || 0)}/s  ↑ ${formatBytes(data.bandwidth?.rateOut || 0)}/s`,
                repoSize: `${formatBytes(data.repository?.size || 0)} / ${formatBytes(data.repository?.storageMax || 0)}`,
                repoObjects: `${data.repository?.numObjects || 0}`,
                version: data.node?.agentVersion || "IPFS Kubo",
                timestamp: new Date(data.timestamp).toLocaleTimeString("en-US", { hour12: false }),
                peerscount: data.peers?.count || 0,
                storageLimit: data.storageLimit?.configured || "Unknown",
                fileLimit: data.fileLimit?.bytes ? formatBytes(data.fileLimit.bytes) : "Unknown",
                repoSizeBytes: data.repository?.size || 0,
                storageMaxBytes: data.repository?.storageMax || 0,
                isHealthy: true,
                blobs: data.blobs || { count: 0, size: 0, sizeStr: "0 B" },
                records: data.records || { count: 0 },
              };

              if (data.blobs?.count !== undefined) {
                this.blobsCount = data.blobs.count;
                this.blobsTotalBytes = data.blobs.size || 0;
                this.blobsTotalBytesStr = data.blobs.sizeStr || formatBytes(this.blobsTotalBytes);
              }
              if (data.records?.count !== undefined) {
                this.recordsCount = data.records.count;
              }

              this.gatewayEnabled = false;
              this.gateways = GATEWAYS.slice();
              if (this.currentGateway.includes("127.0.0.1") || this.currentGateway.includes("localhost")) {
                this.currentGateway = PUBLIC_GATEWAY;
                safeSet("ol_gateway_url", this.currentGateway);
              }
            }
          } catch (err) {
            console.error("Fetch status error:", err);
            this.status.isHealthy = false;
          }
        },

        async fetchHistory() {
          try {
            const res = await fetch("/history?limit=100");
            const data = await res.json();
            if (data.status === "success" && data.uploads) {
              this.history = data.uploads;
            }
          } catch (err) {
            console.error("Fetch history error:", err);
          }
        },

        async fetchPinStats() {
          try {
            const res = await fetch("/pins");
            const data = await res.json();
            if (data.status === "success") {
              this.pinStats = {
                count: data.pinnedCount,
                size: data.pinnedSize,
                sizeStr: data.pinnedSizeStr,
                threshold: data.threshold,
              };
            }
          } catch (err) {
            console.error("Fetch pin stats error:", err);
          }
        },

        // Single File Upload
        triggerFileInput() {
          this.$refs.fileInput.click();
        },

        handleFileSelect(event) {
          const files = Array.from(event.target.files || []);
          if (!files.length) return;
          if (isFolderFileList(files)) {
            this.uploadFolder(files);
          } else {
            this.uploadSingleFile(files[0]);
          }
        },

        async handleDrop(event) {
          this.dragOver = false;
          try {
            const files = await filesFromDataTransfer(event.dataTransfer);
            if (!files.length) return;
            this.activeTab = "pin";
            if (isFolderFileList(files)) {
              await this.uploadFolder(files);
            } else {
              await this.uploadSingleFile(files[0]);
            }
          } catch (err) {
            console.error("Drop error:", err);
            this.showToast(err.message || "Drop failed", "error");
          }
        },

        async uploadSingleFile(file) {
          this.currentUploadFile = file;
          this.isUploading = true;
          this.uploadProgress = 15;
          this.lastUploadResult = null;
          this.lastFolderResult = null;

          const formData = new FormData();
          formData.append("file", file, file.name);
          const endpoint = this.uploadEndpoint(file);

          try {
            this.uploadProgress = 50;
            const res = await fetch(endpoint, {
              method: "POST",
              body: formData,
            });

            this.uploadProgress = 90;
            const data = await res.json();

            if (res.ok && data.status === "success") {
              this.uploadProgress = 100;
              this.lastUploadResult = data;
              const extra = data.anonymized ? " (EXIF stripped)" : "";
              this.showToast(`Pinned "${data.filename}" to Swarm!${extra}`, "success");
              this.fetchHistory();
              this.fetchPinStats();
              this.fetchStatus();
            } else {
              throw new Error(data.message || data.error || "Upload failed");
            }
          } catch (err) {
            console.error("Upload error:", err);
            this.showToast(err.message, "error");
          } finally {
            this.isUploading = false;
            if (this.$refs.fileInput) {
              this.$refs.fileInput.value = "";
            }
          }
        },

        // Folder Upload
        triggerFolderInput() {
          this.$refs.folderInput.click();
        },

        handleFolderSelect(event) {
          const files = event.target.files;
          if (files && files.length > 0) {
            this.uploadFolder(files);
          }
        },

        async uploadFolder(files) {
          this.isUploadingFolder = true;
          this.folderFilesCount = files.length;
          let totalBytes = 0;
          const formData = new FormData();

          for (let i = 0; i < files.length; i++) {
            const f = files[i];
            totalBytes += f.size;
            const relativePath = f.relativePath || f.webkitRelativePath || f.name;
            formData.append("file", f, relativePath);
          }
          this.folderTotalSize = totalBytes;
          this.lastFolderResult = null;
          this.lastUploadResult = null;

          try {
            const res = await fetch("/uploadfolder", {
              method: "POST",
              body: formData,
            });
            const data = await res.json();

            if (res.ok && data.status === "success") {
              this.lastFolderResult = data;
              this.showToast(`Folder pinned (${data.files} files)!`, "success");
              this.fetchHistory();
              this.fetchPinStats();
              this.fetchStatus();
            } else {
              throw new Error(data.message || data.error || "Folder upload failed");
            }
          } catch (err) {
            console.error("Folder upload error:", err);
            this.showToast(err.message, "error");
          } finally {
            this.isUploadingFolder = false;
            if (this.$refs.folderInput) {
              this.$refs.folderInput.value = "";
            }
          }
        },

        // Inspection & QR Modal
        openInspect(item) {
          this.inspectItem = presentItem(item, this.currentGateway, this.brokenThumbs);
          const url = this.inspectItem.gatewayUrl;
          // Standard high-res QR code link for instant mobile sharing
          this.inspectQrUrl = `https://api.qrserver.com/v1/create-qr-code/?size=180x180&data=${encodeURIComponent(url)}`;
          this.inspectModalOpen = true;
        },

        closeInspect() {
          this.inspectModalOpen = false;
          this.inspectItem = null;
        },

        focusSearch() {
          if (this.activePage !== "overview") return;
          const el = this.$refs.searchInput;
          if (el) el.focus({ preventScroll: true });
        },

        onGlobalKey(e) {
          const tag = e.target && e.target.tagName;
          const typing =
            tag === "INPUT" ||
            tag === "TEXTAREA" ||
            tag === "SELECT" ||
            (e.target && e.target.isContentEditable);
          if (e.metaKey || e.ctrlKey) {
            if (e.key === "k" || e.key === "K") {
              e.preventDefault();
              this.focusSearch();
            }
            return;
          }
          if (!typing && e.key === "/") {
            e.preventDefault();
            this.focusSearch();
          }
        },

        setWorkspaceTab(tab) {
          this.workspaceTab = tab;
          safeSet("ol_workspace_tab", tab);
          this.inspectItem = null;
          this.inspectModalOpen = false;
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
          } else if (tab === "ipfs") {
            this.fetchHistory();
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
        this.fetchPinStats();
        this.fetchRecords();
        this.connectRecordsSSE();
        this.fetchBlobs();
        if (this.activePage === "overview") {
          this.fetchHistory();
        }

        window.addEventListener("keydown", this.onGlobalKey);

        // Live polling
        this._statusTimer = setInterval(() => this.fetchStatus(), 8000);
        this._pinsTimer = setInterval(() => this.fetchPinStats(), 25000);
        this._blobsTimer = setInterval(() => this.fetchBlobs(), 15000);
        this._recordsTimer = setInterval(() => this.fetchRecords(), 15000);
      },

      beforeUnmount() {
        window.removeEventListener("keydown", this.onGlobalKey);
        clearInterval(this._statusTimer);
        clearInterval(this._pinsTimer);
        clearInterval(this._blobsTimer);
        clearInterval(this._recordsTimer);
        if (this.recordsEventSource) {
          try { this.recordsEventSource.close(); } catch (_) {}
        }
      },
    });
  }

  window.Originless = {
    GATEWAYS,
    PUBLIC_GATEWAY,
    formatBytes,
    formatDate,
    formatUnix,
    formatRelative,
    getFileCategory,
    itemCategory,
    shortCid,
    createOriginlessApp,
  };
})();
