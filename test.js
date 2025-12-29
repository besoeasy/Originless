// Simple test harness for nostr pin sync; requires env vars for safety.
const { syncNostrPins } = require("./nostr");

const npubOrPubkey = process.env.NOSTR_NPUB || "npub1x6au4qgw9t403yushl34tgngmgcaqv9yna7ywf8e6x4xf686ln7qc7y6wq";

if (!npubOrPubkey) {
  console.error("Set NOSTR_NPUB (or NOSTR_PUBKEY) before running.\nExample: NOSTR_NPUB=npub1... node test.js");
  process.exit(1);
}

(async () => {
  try {
    const result = await syncNostrPins({
      npubOrPubkey,
      dryRun: true, // keep dry-run to avoid pinning during tests
    });
    console.log(JSON.stringify(result, null, 2));
  } catch (err) {
    console.error("Failed to sync/pin from Nostr:", err.message);
    process.exit(1);
  }
})();
