# Originless Client Examples

Single-file HTML web applications demonstrating zero-auth decentralized apps using [Originless](https://originless.space).

Live deployment: **[https://originless.besoeasy.com/](https://originless.besoeasy.com/)**

Every example:
- Runs directly in the browser with **no build steps, node modules, or bundlers**.
- Uses [esm.sh](https://esm.sh/) to import `@noble/curves` for client-side **Ed25519 signing**.
- Generates and stores identity keypairs in `localStorage`.
- Connects to `https://originless.space` (or any local `http://localhost:3232` node).
- Syncs state in real time using Server-Sent Events (`/events/stream`).

---

## Included Examples

### 1. [Real-Time Room Comments (`comment-box.html`)](./comment-box.html)
A decentralized room-based comment and chat box.
- Join any room via URL parameter (e.g., `?room=general` or `?room=dev`).
- Sign every message with client-side Ed25519 keys.
- Receive live incoming comments via Server-Sent Events (`EventSource`).
- Displays sender Ed25519 public key fingerprints and timestamps.

### 2. [2-Player Board Game (`board-game.html`)](./board-game.html)
A real-time multiplayer Tic-Tac-Toe game.
- Share a game room URL with an opponent (e.g., `?game=alpha-42`).
- Both players take turns signing move events.
- Replays full move history on load; updates turns live over SSE.
- Fully decentralized with zero game server backend logic.

### 3. [Collaborative Pixel Canvas (`pixel-canvas.html`)](./pixel-canvas.html)
A shared r/place style collaborative pixel art grid.
- Pick from a palette of vibrant colors or use custom hex codes.
- Every pixel placement publishes a signed Ed25519 event.
- Live canvas updates stream simultaneously to all viewers over SSE.
- Export your completed canvas directly to PNG.

### 4. [Zero-Knowledge Pastebin (`encrypted-pastebin.html`)](./encrypted-pastebin.html)
Client-side end-to-end encrypted secret note sharing.
- Encrypts text locally using WebCrypto **AES-256-GCM**.
- Decryption key resides exclusively in the URL fragment (`#key`) and is never sent to the server.
- Configurable auto-destruct TTL (1 hour to 30 days) purged automatically by Originless.

---

## Live Demo & Local Testing

- **Live URL**: [https://originless.besoeasy.com/](https://originless.besoeasy.com/)
- **Launcher Hub**: [examples/index.html](./index.html)

To run locally:
```bash
python3 -m http.server 8080
# Open http://localhost:8080/examples/
```
