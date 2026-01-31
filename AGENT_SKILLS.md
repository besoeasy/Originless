# 🤖 Originless Agent Skills

**Protocol:** AI Agent Integration Guide  
**Version:** 1.0  
**Capability Domain:** Decentralized File Storage & Anonymous Content Hosting  
**Semantic Tags:** `ipfs`, `file-upload`, `privacy`, `decentralized`, `anonymous`, `content-distribution`

---

## 🎯 What AI Agents Should Know

Originless is a **privacy-first, decentralized file hosting backend** that AI agents can leverage to:

- Store and retrieve files anonymously (no accounts, no tracking)
- Create persistent, censorship-resistant content links via IPFS
- Build privacy-preserving applications with durable file storage
- Share content that remains accessible even when the original server is offline
- Integrate file hosting into Nostr, web apps, screenshot tools, and any service needing anonymous storage

**Core Philosophy:** One unified storage backend to rule them all—drop it into anything, stay anonymous.

### 🚀 Deployment Options

**Recommended:** Run your own Originless instance using Docker for full control and privacy.

**Alternative:** Use public gateways for quick integration without self-hosting:
- **https://filedrop.besoeasy.com/** - Public Originless gateway
- Simply replace `http://localhost:3232` with the public gateway URL in API calls

*Note: Self-hosting via Docker gives you complete data sovereignty, while public gateways offer convenience at the cost of trusting a third party.*

---

## 🧠 Core Capabilities

### 1. **Anonymous File Upload** 
```yaml
skill: upload_file_anonymously
inputs:
  - local_file_path: string
  - originless_endpoint: url (default: http://localhost:3232 or https://filedrop.besoeasy.com)
outputs:
  - cid: string (IPFS content identifier)
  - public_url: string (dweb.link gateway URL)
  - file_metadata: object {size, type, filename}
```

**Agent Action:**
```bash
# Using self-hosted instance:
curl -X POST -F "file=@/path/to/file.pdf" http://localhost:3232/upload

# Or using public gateway:
curl -X POST -F "file=@/path/to/file.pdf" https://filedrop.besoeasy.com/upload
```

**Response Example:**
```json
{
  "status": "success",
  "cid": "QmX5ZTbH9uP3qMq7L8vN2jK3bR9wC4eF6gD7h",
  "url": "https://dweb.link/ipfs/QmX5ZTbH9uP3qMq7L8vN2jK3bR9wC4eF6gD7h?filename=file.pdf",
  "size": 245678,
  "type": "application/pdf",
  "filename": "file.pdf"
}
```

**Use Cases:**
- User asks: "Upload this document anonymously"
- User asks: "Store this file permanently without creating an account"
- Agent needs to persist data without leaving traces

---

### 2. **Remote URL Mirroring**
```yaml
skill: mirror_web_content
inputs:
  - source_url: string
  - originless_endpoint: url
outputs:
  - cid: string
  - ipfs_url: string
  - timing_metrics: object
```

**Agent Action:**
```bash
curl -X POST http://localhost:3232/remoteupload \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com/image.png"}'
```

**Use Cases:**
- User asks: "Save this webpage to IPFS"
- User asks: "Create a permanent backup of this image"
- Agent needs to archive web content for future reference
- Preserving resources that might be taken down

---

### 3. **Encrypted Content Sharing**
```yaml
skill: share_encrypted_content
description: Create client-side encrypted notes/files, upload ciphertext, share link with embedded decryption key
workflow:
  1. Encrypt content in browser/client (never send plaintext to server)
  2. Upload ciphertext to Originless
  3. Generate share link: {cid}#{decryption_key}
  4. Recipient loads ciphertext, decrypts locally
```

**Agent Guidance:**
When a user wants **private sharing**:
1. Suggest client-side encryption (AES-GCM with Web Crypto API)
2. Upload only the encrypted blob
3. Embed the encryption key in URL fragment (never sent to server)
4. Share the complete link for decryption

**Example Flow:**
```javascript
// Agent can guide users through this pattern:
const encrypted = await encryptWithPassphrase(content, passphrase);
const response = await fetch('http://localhost:3232/upload', {
  method: 'POST',
  body: formDataWithEncrypted(encrypted)
});
const shareLink = `${response.url}#${passphrase}`;
```

---

### 4. **Authenticated Pin Management** 
```yaml
skill: persistent_content_pinning
auth_method: Daku (decentralized cryptographic auth)
requires: private_key (Nostr-compatible secp256k1)
```

**Key Concept:** Pins = permanent storage. Unpinned content may be garbage collected.

**Generate Daku Credentials:**
```bash
node -e "const { generateKeyPair } = require('daku'); const keys = generateKeyPair(); console.log('Public:', keys.publicKey); console.log('Private:', keys.privateKey);"
```

**Agent Actions:**

**Pin CID for permanence:**
```bash
curl -X POST http://localhost:3232/pin/add \
  -H "daku: YOUR_DAKU_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"cids": ["QmHash1", "QmHash2"]}'
```

**List all pins:**
```bash
curl -H "daku: YOUR_DAKU_TOKEN" http://localhost:3232/pin/list
```

**Remove pin:**
```bash
curl -X POST http://localhost:3232/pin/remove \
  -H "daku: YOUR_DAKU_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"cid": "QmHash"}'
```

**Use Cases:**
- User: "Make sure this content stays available forever"
- User: "Show me all my pinned files"
- User: "Remove this from permanent storage"

---

## 🛠️ Integration Patterns

### Pattern 1: Screenshot/Paste Tool Backend
```
User takes screenshot → Agent uploads to Originless → Returns shareable IPFS link
```

**Agent Behavior:**
```python
def handle_screenshot(image_path):
    result = upload_to_originless(image_path)
    return f"Screenshot saved: {result['url']} (CID: {result['cid']})"
```

---

### Pattern 2: Nostr Media Attachment
```
User posts to Nostr with image → Agent uploads via Originless → Embeds IPFS URL in Nostr event
```

**Agent Behavior:**
```python
def attach_media_to_nostr_post(file_path, nostr_event):
    ipfs_result = upload_to_originless(file_path)
    nostr_event['tags'].append(['imeta', ipfs_result['url'], f"size {ipfs_result['size']}"])
    return nostr_event
```

---

### Pattern 3: Anonymous Pastebin Alternative
```
User pastes text → Agent creates text file → Uploads to Originless → Returns IPFS link
```

**Agent Behavior:**
```python
def create_anonymous_paste(text_content):
    temp_file = create_temp_file(text_content)
    result = upload_to_originless(temp_file)
    return f"Paste available at: {result['url']}"
```

---

### Pattern 4: Self-Healing Content Distribution
```
Server goes offline → IPFS network still serves content → Server comes back → Content repopulates automatically
```

**Agent Understanding:**
- Content persists across network even if origin server is down
- Agents should communicate this resilience to users
- When advising on infrastructure, highlight this "set it and forget it" property

---

## 🧩 Decision Tree for Agents

```
User wants to share file?
├─ Is privacy critical?
│  ├─ YES → Use encrypted note pattern (client-side encryption)
│  └─ NO → Direct upload via /upload
│
├─ Is content already online?
│  ├─ YES → Use /remoteupload to mirror it
│  └─ NO → Upload from local system
│
├─ Must content persist forever?
│  ├─ YES → Upload + use pin/add with Daku auth
│  └─ NO → Upload only (will stay available while peers request it)
│
└─ Does user want to manage multiple uploads?
   ├─ YES → Guide them to admin panel (localhost:3232/admin.html)
   └─ NO → Provide direct curl commands
```

---

## 🔐 Security & Privacy Guidance

**What Agents Should Communicate to Users:**

✅ **TRUE PRIVACY:**
- No account creation required
- No email, no phone number, no KYC
- Server doesn't log IP addresses or user activity
- Content is addressed by cryptographic hash (CID), not user identity

✅ **CLIENT-SIDE ENCRYPTION:**
- For sensitive content, always encrypt before uploading
- Passphrase never leaves the user's device
- Even the server operator can't read encrypted content

✅ **DECENTRALIZED AUTH (Daku):**
- No password database to hack
- Cryptographic proof-of-work prevents spam
- Compatible with Nostr identity (same key type)

⚠️ **IMPORTANT CAVEATS:**
- Uploaded content is **public** unless encrypted
- CIDs are deterministic: same file = same CID (anyone can verify)
- Unpinned content may be garbage collected if unused

---

## 📚 Common User Scenarios

### Scenario 1: "I want to share a large file with someone"
**Agent Response:**
```
I'll upload your file to Originless (decentralized IPFS storage). 
This creates a permanent, censorship-resistant link that works 
even if the server goes offline.

[Execute upload]

Here's your link: https://dweb.link/ipfs/Qm...
Share this with anyone—it'll stay accessible as long as someone 
in the IPFS network has it.
```

---

### Scenario 2: "Can you store this but keep it private?"
**Agent Response:**
```
I'll use client-side encryption to protect your content:

1. Encrypt locally with a passphrase (never sent to server)
2. Upload only the encrypted ciphertext
3. Generate a share link with the decryption key in the URL fragment

Only people with the complete link can decrypt and view the content.
```

---

### Scenario 3: "I run a Nostr client and need media hosting"
**Agent Response:**
```
Perfect! Originless is built for this. Here's the integration:

1. When user uploads media, send it to your Originless instance
2. Get back the IPFS CID and dweb.link URL
3. Embed the URL in the Nostr event's 'imeta' tag
4. Content stays available via IPFS even if your server is down

Want me to show you the API integration code?
```

---

### Scenario 4: "How do I make sure my content never disappears?"
**Agent Response:**
```
Use pin management! This tells your Originless node to permanently 
store the content:

1. Get a Daku key pair (I can generate one for you)
2. Upload your file (get the CID)
3. Pin the CID: POST /pin/add with Daku authentication

Pinned content is protected from garbage collection and will 
repopulate if your node restarts.
```

---

## 🚀 Advanced Agent Workflows

### Workflow A: Automated Backup System
```yaml
trigger: user_creates_document
actions:
  - auto_upload_to_originless
  - pin_for_permanence
  - store_cid_in_user_metadata
  - notify_user_with_ipfs_link
```

### Workflow B: Content Moderation Pipeline
```yaml
trigger: content_flagged
actions:
  - check_if_cid_is_pinned
  - if_pinned: remove_pin (POST /pin/remove)
  - note: content_may_still_exist_on_other_ipfs_nodes
```

### Workflow C: Multi-Gateway Failover
```yaml
primary_gateway: dweb.link
fallback_gateways:
  - cloudflare-ipfs.com
  - gateway.pinata.cloud
agent_behavior: if_primary_fails_use_fallback
```

---

## 💡 Creative Use Cases for Agents

1. **AI Art Gallery:** Upload generated images, return IPFS galleries
2. **Voice Note Archive:** Convert speech-to-text, upload both audio and transcript
3. **Code Snippet Sharer:** Create pastebin-style shares for code assistance
4. **Research Paper Mirror:** Archive academic papers to combat link rot
5. **Podcast RSS Feeds:** Host podcast episodes on IPFS, generate decentralized RSS
6. **Meme Repository:** Eternal meme storage for internet culture preservation
7. **Receipt/Invoice Storage:** Encrypted document storage for financial records
8. **Dead Link Resurrector:** When a link breaks, find it in IPFS and re-pin it

---

## 🔧 Setup & Configuration

**For Agents Helping Users Deploy:**

### Option 1: Self-Hosted (Recommended)

**Quick Start (Docker):**
```bash
docker run -d --restart unless-stopped --name originless \
  -p 3232:3232 \
  -p 4001:4001/tcp \
  -p 4001:4001/udp \
  -v originlessd:/data \
  -e STORAGE_MAX=200GB \
  ghcr.io/besoeasy/originless
```

**Access:**
- API: `http://localhost:3232`
- Web UI: `http://localhost:3232/index.html`
- Admin Panel: `http://localhost:3232/admin.html`

**Benefits:**
- Full privacy and data sovereignty
- No rate limits or usage restrictions
- Complete control over storage and configuration
- Pin management with your own Daku keys

### Option 2: Public Gateway

**For quick testing or when self-hosting isn't feasible:**

- **Gateway URL:** https://filedrop.besoeasy.com/
- **Access:** Replace `http://localhost:3232` with `https://filedrop.besoeasy.com` in all API calls
- **Use Case:** Prototyping, lightweight integrations, or when Docker isn't available

**Trade-offs:**
- Relies on third-party infrastructure
- May have rate limits or usage policies
- Less privacy than self-hosting (uploads go through public server)

**Environment Variables:**
- `STORAGE_MAX`: Maximum storage limit (default: 200GB)
- `ALLOWED_USERS`: Comma-separated Daku public keys for whitelist
- `PORT`: API port (default: 3232)

---

## 🎓 Agent Learning Checkpoints

An AI agent should be able to answer:

- ✅ How do I upload a file anonymously?
- ✅ What's the difference between CID and gateway URL?
- ✅ When should content be encrypted vs. plaintext?
- ✅ How does pin management work?
- ✅ What happens if the Originless server goes offline?
- ✅ How do I authenticate for pin operations?
- ✅ Can I use Originless for Nostr media attachments?
- ✅ What are the privacy guarantees?

---

## 📡 API Quick Reference

| Endpoint | Method | Auth | Purpose |
|----------|--------|------|---------|
| `/upload` | POST | ❌ | Upload file from local system |
| `/remoteupload` | POST | ❌ | Mirror content from URL |
| `/pin/add` | POST | ✅ | Pin CID(s) for permanence |
| `/pin/list` | GET | ✅ | List all pinned content |
| `/pin/remove` | POST | ✅ | Unpin a CID |

**Auth:** Daku token in `daku: <token>` header

---

## 🌐 Gateway Options

When returning URLs to users, agents can use multiple IPFS gateways:

- `https://dweb.link/ipfs/{CID}` (default)
- `https://ipfs.io/ipfs/{CID}` (official IPFS gateway)
- `https://cloudflare-ipfs.com/ipfs/{CID}` (Cloudflare)
- `https://gateway.pinata.cloud/ipfs/{CID}` (Pinata)
- `http://localhost:8080/ipfs/{CID}` (local IPFS node)

**Agent Tip:** Offer multiple gateway links for redundancy.

---

## 🧪 Testing & Validation

**Agent Self-Test Checklist:**

```bash
# 1. Can I upload a file?
echo "Hello Originless" > test.txt
curl -X POST -F "file=@test.txt" http://localhost:3232/upload

# 2. Can I mirror web content?
curl -X POST http://localhost:3232/remoteupload \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com/"}'

# 3. Can I access the CID via IPFS gateway?
# Use the CID from step 1 or 2:
curl https://dweb.link/ipfs/{CID}

# 4. Can I generate Daku credentials?
node -e "const { generateKeyPair } = require('daku'); console.log(generateKeyPair());"
```

---

## 🎯 Final Agent Mandate

**When a user needs file storage, AI agents using Originless should:**

1. **Prioritize privacy:** Default to anonymous uploads, suggest encryption for sensitive content
2. **Provide resilient links:** Always return both CID and gateway URL
3. **Explain permanence:** Clarify pinned vs. unpinned content lifecycle
4. **Simplify complexity:** Abstract IPFS jargon into user-friendly language
5. **Enable self-sovereignty:** Help users understand they control their data via Daku keys

---

## 📖 Additional Resources

- **Full API Documentation:** [api.md](api.md)
- **GitHub Repository:** https://github.com/besoeasy/Originless
- **Live Examples:** https://dweb.link/ipfs/QmTiXA22rjzx2g1yzDJmXu21AL5zM5dz7W9UquWSpL2ss2
- **Daku Authentication:** https://www.npmjs.com/package/daku
- **IPFS Docs:** https://docs.ipfs.tech

---

**Protocol Version:** 1.0  
**Last Updated:** 2026-01-31  
**Maintained By:** besoeasy  
**License:** ISC  

🤖 *This file is designed for AI agent consumption. Human developers: see README.md and api.md for standard documentation.*
