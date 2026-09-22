# Originless Client Examples

Single-file HTML web applications demonstrating zero-auth decentralized apps using [Originless](https://originless.space).

Every example:
- Runs directly in the browser with **no build steps or bundlers**.
- Uses [esm.sh](https://esm.sh/) to import `@noble/curves` for client-side **Ed25519 signing**.
- Generates and stores identity keypairs in `localStorage`.
- Connects to `https://originless.space` (or a local `http://localhost:3232` node).
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

---

## How to Run

Simply open any of the HTML files directly in your web browser, or serve them locally:

```bash
# Using Python
python3 -m http.server 8080

# Then open in browser:
# http://localhost:8080/examples/
```

Or open the launcher hub directly: [examples/index.html](./index.html).
