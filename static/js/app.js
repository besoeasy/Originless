(() => {
  // Built-in public IPFS gateways. The local Originless gateway is prepended
  // at runtime when ENABLE_GATEWAY is on (the default).
  const PUBLIC_GATEWAY = "https://inbrowser.link/ipfs/";
  const GATEWAYS = [
    { label: "inbrowser.link", url: PUBLIC_GATEWAY },
    { label: "ipfs.io (Official)", url: "https://ipfs.io/ipfs/" },
  ];

  function migrateSavedGateway(url) {
    if (!url) return url;
    if (url === "https://dweb.link/ipfs/" || url === "https://dweb.link/ipfs") {
      return PUBLIC_GATEWAY;
    }
    return url;
  }

  function localGateway() {
    const origin = typeof window !== "undefined" && window.location ? window.location.origin : "http://localhost:3232";
    return { label: "This node (Originless)", url: `${origin}/ipfs/`, local: true };
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
      appver: "...",
      timestamp: "...",
      peerscount: 0,
      storageLimit: "Unknown",
      fileLimit: "Unknown",
      repoSizeBytes: 0,
      storageMaxBytes: 0,
      isHealthy: true,
    };
  }

  function pinDefaults() {
    return { count: 0, size: 0, sizeStr: "0 B", threshold: 75 };
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
          getFileCategory,
          fileKind: itemCategory,
        };
      },
      data() {
        const local = localGateway();
        const savedGateway = migrateSavedGateway(localStorage.getItem("ol_gateway_url"));
        if (savedGateway && savedGateway !== localStorage.getItem("ol_gateway_url")) {
          localStorage.setItem("ol_gateway_url", savedGateway);
        }
        return {
          activePage: options.page || "overview",
          
          gateways: [local, ...GATEWAYS],
          currentGateway: savedGateway || local.url,
          gatewayEnabled: true,

          status: statusDefaults(),
          pinStats: pinDefaults(),
          
          history: [],
          searchQuery: "",
          statusFilter: "all", 
          typeFilter: "all",
          sortBy: "date-desc",
          
          activeTab: "pin", // pin, prompt
          brokenThumbs: {},
          anonymizeMedia: localStorage.getItem("ol_anonymize_media") !== "false",
          
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

        gatewayHost() {
          try {
            return new URL(this.currentGateway).hostname;
          } catch (_) {
            return this.currentGateway;
          }
        },

        storagePercentage() {
          if (!this.status.storageMaxBytes || this.status.storageMaxBytes === 0) return 0;
          const pct = (this.status.repoSizeBytes / this.status.storageMaxBytes) * 100;
          return Math.min(100, Math.max(0, Math.round(pct * 10) / 10));
        },

        storageGaugeClass() {
          if (this.storagePercentage >= 90) return "is-danger";
          if (this.storagePercentage >= 75) return "is-warn";
          return "";
        },

        nodeGraphics() {
          const id = this.status.fullNodeId || this.status.nodeId || "12D3KooWOriginlessNode000000000000000000000000000";
          let hash = 0;
          for (let i = 0; i < id.length; i++) {
            hash = Math.imul(31, hash) + id.charCodeAt(i) | 0;
          }
          hash = Math.abs(hash);

          const hue1 = hash % 360;
          const hue2 = (hue1 + 50 + ((hash >> 4) % 60)) % 360;
          const hue3 = (hue2 + 120) % 360;

          const color1 = `hsl(${hue1}, 85%, 62%)`;
          const color2 = `hsl(${hue2}, 80%, 55%)`;
          const color3 = `hsl(${hue3}, 90%, 65%)`;

          const cx = 80, cy = 80;
          const numNodes = 7;
          const nodes = [];
          for (let i = 0; i < numNodes; i++) {
            const charCode = id.charCodeAt(i % id.length) || 65;
            const angle = (i / numNodes) * 2 * Math.PI + ((charCode % 30) * Math.PI / 180);
            const r = 32 + (charCode % 22);
            const x = Math.round(cx + r * Math.cos(angle));
            const y = Math.round(cy + r * Math.sin(angle));
            nodes.push({ x, y, r: 2.5 + (i % 3) });
          }

          const lines = [];
          for (let i = 0; i < nodes.length; i++) {
            const next = nodes[(i + 1) % nodes.length];
            const cross = nodes[(i + 2) % nodes.length];
            lines.push({ x1: nodes[i].x, y1: nodes[i].y, x2: next.x, y2: next.y, cross: false });
            if (i % 2 === 0) {
              lines.push({ x1: nodes[i].x, y1: nodes[i].y, x2: cross.x, y2: cross.y, cross: true });
            }
          }

          return {
            hashHex: hash.toString(16).padStart(8, "0"),
            color1,
            color2,
            color3,
            glowStyle: {
              background: `radial-gradient(circle, ${color1}28 0%, ${color2}15 45%, transparent 70%)`
            },
            nodes,
            lines,
          };
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
- **Anonymized Image**: \`POST ${origin}/media\` (strips EXIF/GPS/XMP, then pins)
- **Upload Directory / DApp**: \`POST ${origin}/uploadfolder\` (Multipart field: \`file\`, relative paths)
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

# 1b. Pin a photo with EXIF/GPS stripped
curl -X POST -F "file=@photo.jpg" ${origin}/media

# 2. Upload full Vite/React dist static build folder
curl -X POST \\
  -F "file=@dist/index.html;filename=index.html" \\
  -F "file=@dist/assets/app.js;filename=assets/app.js" \\
  ${origin}/uploadfolder

# 3. Check node telemetry & swarm status
curl ${origin}/status

# Share the CID at ${gw}{cid} — not https://dweb.link/ipfs/`;
          }

          if (this.promptFormat === "python") {
            return `import requests

ORIGIN = "${origin}"
GATEWAY = "${gw}"

# 1. Upload a file
with open("sample.png", "rb") as f:
    r = requests.post(f"{ORIGIN}/media", files={"file": f})
    data = r.json()
    cid = data["cid"]
    print(f"Pinned: {GATEWAY}{cid}")`;
          }

          return `Publish files with Originless. Pin them to IPFS on this node, then share the CID through a gateway.

This is not limited to websites. Use it for a music file, a generated index.html report, a photo, an image gallery, a video, a PDF, a folder of mixed assets, or a full dist/ site.

Repo: https://github.com/besoeasy/Originless
This Originless node: ${origin}
Public IPFS gateway: ${gw}
Do not use https://dweb.link/ipfs — share https://inbrowser.link/ipfs/{cid} instead.

Use this node for all uploads. Do not invent another host or API key. There is no auth.

Endpoints:
- POST ${origin}/upload — one file, exact bytes. Multipart field "file".
- POST ${origin}/media — one image (JPEG/PNG/GIF/WebP). Strips EXIF, GPS, XMP, IPTC, and comments, applies orientation, then pins. Use this for photos.
- POST ${origin}/uploadfolder — a folder (gallery, album, report, site). Multipart field name is "file" for each file; set filename to the relative path so links stay intact.
- GET ${origin}/health — liveness check.

The JSON response includes "cid". That CID is the content address.

After upload, return gateway URLs to the user:
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
          localStorage.setItem("ol_gateway_url", this.currentGateway);
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
          localStorage.setItem("ol_anonymize_media", this.anonymizeMedia ? "true" : "false");
        },

        isImageFile(file) {
          if (!file) return false;
          if (file.type && file.type.startsWith("image/")) return true;
          return /\.(jpe?g|png|gif|webp)$/i.test(file.name || "");
        },

        uploadEndpoint(file) {
          if (this.anonymizeMedia && this.isImageFile(file)) return "/media";
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
                appver: data.appVersion || "1.0",
                timestamp: new Date(data.timestamp).toLocaleTimeString("en-US", { hour12: false }),
                peerscount: data.peers?.count || 0,
                storageLimit: data.storageLimit?.configured || "Unknown",
                fileLimit: data.fileLimit?.bytes ? formatBytes(data.fileLimit.bytes) : "Unknown",
                repoSizeBytes: data.repository?.size || 0,
                storageMaxBytes: data.repository?.storageMax || 0,
                isHealthy: true,
              };

              const local = localGateway();
              this.gatewayEnabled = !data.gateway || data.gateway.enabled !== false;
              if (this.gatewayEnabled) {
                if (!this.gateways.some((g) => g.url === local.url)) {
                  this.gateways = [local, ...GATEWAYS];
                }
              } else {
                this.gateways = GATEWAYS.slice();
                if (this.currentGateway === local.url) {
                  this.currentGateway = GATEWAYS[0].url;
                  localStorage.setItem("ol_gateway_url", this.currentGateway);
                }
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
      },

      mounted() {
        this.fetchStatus();
        this.fetchPinStats();
        if (this.activePage === "overview") {
          this.fetchHistory();
        }

        window.addEventListener("keydown", this.onGlobalKey);

        // Live polling
        this._statusTimer = setInterval(() => this.fetchStatus(), 8000);
        this._pinsTimer = setInterval(() => this.fetchPinStats(), 25000);
      },

      beforeUnmount() {
        window.removeEventListener("keydown", this.onGlobalKey);
        clearInterval(this._statusTimer);
        clearInterval(this._pinsTimer);
      },
    });
  }

  window.Originless = {
    GATEWAYS,
    PUBLIC_GATEWAY,
    localGateway,
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
