# Originless

## The Universal Backend for Building Apps

Collapse sprawling cloud microservices into two universal primitives — **Signed Events** and **IPFS-Native Content**. Build full-featured real-time apps in pure vanilla HTML and JavaScript with zero backend boilerplate.

```bash
docker run --rm --name originless \
  -p 3232:3232 \
  ghcr.io/besoeasy/originless:latest
```

Open [http://localhost:3232](http://localhost:3232) after the container starts.

For local Podman development:

```bash
./podman.sh
```

## The Traditional SaaS Stack

### Fragile & Expensive

- **5+ Fragmented Services:** Juggling Auth0, PostgreSQL, AWS S3, Redis, and Pusher just to build a simple collaborative tool.
- **Credential & Secret Leaks:** Rotating API keys, JWT expiration races, and central user database breach liabilities.
- **Database Migrations:** Schema alterations, ORM overhead, connection pool limits, and downtime risk.
- **Escalating Cloud Bills:** Per-MAU authentication fees, egress penalties, and monthly SaaS subscriptions that punish growth.

## The Originless Way

### The Best Option

- **One Single Binary:** A zero-dependency Go container running on a $4/mo VPS, Raspberry Pi, Umbrel, Docker, or laptop.
- **Client-Side Cryptography:** Ed25519 keypairs generated on-device in two lines of code. The key is the identity; zero server auth state.
- **Native Real-Time (SSE):** Instant Server-Sent Events (`/events/stream`) supported natively in every browser without socket libraries.
- **IPFS-Native Lifecycle & P2P:** Automatic TTL record pruning, local IPFS content retrieval, and O(1) Kademlia DHT swarm sync.

## Protocols & Cloud Services Replaced

Standard open protocols replace proprietary cloud APIs with zero vendor lock-in.

### 🔐 Zero Auth

**Ed25519 Cryptographic Signatures**

Identity lives strictly on client devices. Every event is mathematically signed. Zero passwords to store, zero user databases to breach, and zero token expiration bugs.

**Replaces:** Auth0, Clerk, Firebase Auth, AWS Cognito

### ⚡ Native HTTP

**Native Server-Sent Events (SSE)**

Stream live events to browsers with native `EventSource` and terminals with `curl -N`. Filterable by collection and label with automatic client reconnection.

**Replaces:** Redis Pub/Sub, Pusher, Ably, Socket.io

### 📦 IPFS-Native Storage

**Content-Addressed by CID**

Upload files through `/up` and `/upf`; Originless adds them to IPFS and returns the resulting CID and size. Content is addressed by its IPFS CID, served from the local node, and requires no separate object-storage service.

**Replaces:** AWS S3, Cloudflare R2, MinIO, 0x0.st

### 🗄️ ACID WAL

**Indexed SQLite Event Store**

Microsecond queries, full-text search, label indexing, keyset cursor pagination, and built-in TTL eviction. Zero database migrations or ORM schemas required.

**Replaces:** Firebase Firestore, Supabase, PocketBase

### 🌐 libp2p Mesh

**Autonomous P2P Swarm**

Single-port HTTP/WS multiplexing, AutoNAT hole punching, Kademlia DHT discovery, and O(1) commutative XOR state root fast-path sync across nodes worldwide.

**Replaces:** Nostr Relays, Matrix Homeservers, BitTorrent

## Possibility Is What You Can Imagine

What can you build with Originless? Everything you can imagine, in a fraction of the time.

### 🎨 Real-Time Collaborative Apps

Shared whiteboards, code pads, live voting, and infinite collaborative canvases. Every pixel or stroke is a signed event pushed instantly to all peers over SSE.

### 💬 Encrypted Chat & Social Feeds

Public room messaging, decentralized comments for static blogs, and end-to-end encrypted private chats without a central database operator.

### 🤖 Autonomous AI Agent Swarms

Multi-agent blackboard architectures where LLMs coordinate workflows, claim task events, tail live SSE streams, and exchange binary model weights or artifacts without API tokens.

### 📱 Local-First & Multi-Device Sync

Personal notes, todo managers, and password vaults that store data locally and sync silently across phones, laptops, and home servers when online.

### 🎮 Turn-Based Multiplayer Games

Chess, checkers, card games, and turn-based strategy where every move is a cryptographically signed event, guaranteeing cheat-proof move history and player provenance.

### 📡 IoT & Edge Sensor Fleets

Environmental monitors, home automation hubs, and edge cameras logging time-series events and IPFS snapshots with automatic lifecycle retention.

## API

See [`api.md`](api.md) for the current HTTP API:

- `POST /up` and `POST /upf` for IPFS uploads
- `GET /down/{cid}` for locally available content
- `POST /events` for signed event publishing
- `GET /events` for event queries
- `GET /events/{id}` for event retrieval
- `GET /events/stream` for real-time SSE updates
- `GET /stats` for IPFS and event metrics
