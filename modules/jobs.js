// Nostr job management
const { syncNostrPins } = require("./nostr");

const { pinCid } = require("./ipfs");
const {
  batchInsertCids,
  markInProgress,
  clearInProgress,
  updatePinSize,
  countByTypeAndStatus,
  setLastPinnerActivity,
  setLastNostrRun,
  getRandomCid,
} = require("./database");

// Nostr discovery job
const runNostrJob = async (NPUBS) => {
  if (!NPUBS || NPUBS.length === 0) {
    return;
  }

  if (Math.random() < 0.5) {
    console.log(`[JOB] NOSTR_DISCOVERY_EXECUTE random_trigger=true npub_count=${NPUBS.length}`);
  } else {
    return;
  }

  try {
    const allResults = [];
    const allCids = [];
    let totalEventsScanned = 0;
    let totalCidsFound = 0;

    // Process each NPUB
    for (const npub of NPUBS) {
      try {
        // Fetch CIDs without pinning (dryRun = true)
        const result = await syncNostrPins({ npubOrPubkey: npub, dryRun: true });

        totalEventsScanned += result.eventsScanned;
        totalCidsFound += result.cidsFound;

        // Tag CIDs with their source NPUB
        const cidsWithNpub = (result.plannedPins || []).map((cidObj) => ({
          ...cidObj,
          type: "self",
          npub: npub, // Add NPUB identifier
        }));

        allCids.push(...cidsWithNpub);
        allResults.push({
          npub: npub,
          eventsScanned: result.eventsScanned,
          cidsFound: result.cidsFound,
          newCids: cidsWithNpub.length,
        });

        console.log(`[JOB] NOSTR_DISCOVERY_NPUB npub=${npub.slice(0, 12)}... events=${result.eventsScanned} cids=${result.cidsFound}`);
      } catch (npubErr) {
        console.error(`[JOB] NOSTR_DISCOVERY_NPUB_ERROR npub=${npub.slice(0, 12)}... error="${npubErr.message}"`);
        allResults.push({
          npub: npub,
          error: npubErr.message,
        });
      }
    }

    // Batch insert to database (duplicates automatically ignored)
    const insertedCount = batchInsertCids(allCids);

    // Get current pending count
    const selfPending = countByTypeAndStatus("self", "pending");

    setLastNostrRun({
      at: new Date().toISOString(),
      npubs: allResults,
      aggregate: {
        eventsScanned: totalEventsScanned,
        cidsFound: totalCidsFound,
        newCids: allCids.length,
        inserted: insertedCount,
        duplicates: allCids.length - insertedCount,
        pendingInDb: selfPending,
      },
      error: null,
    });

    console.log(
      `[JOB] NOSTR_DISCOVERY_COMPLETE npubs=${NPUBS.length} total_discovered=${allCids.length} inserted=${insertedCount} duplicates=${allCids.length - insertedCount
      } pending=${selfPending}`
    );
    console.log(`[JOB] NOSTR_DISCOVERY_AGGREGATE events=${totalEventsScanned} cids=${totalCidsFound}`);
  } catch (err) {
    setLastNostrRun({
      at: new Date().toISOString(),
      npubs: null,
      aggregate: null,
      error: err.message,
    });
    console.error(`[JOB] NOSTR_DISCOVERY_ERROR error="${err.message}"`);
  }
};

// Pinner job - continuously picks random CIDs and ensures they're pinned
const pinnerJob = async () => {
  console.log(`[JOB] PINNER_LOOP_START mode=fire_and_forget`);

  while (true) {
    try {
      // Get a random CID from database
      const cidObj = getRandomCid();

      if (!cidObj) {
        const pinnocid = Math.floor(Math.random() * 200);

        console.log(`[JOB] PINNER_NO_CIDS waiting=${pinnocid} seconds`);

        await new Promise((resolve) => setTimeout(resolve, pinnocid * 1000));
        continue;
      }

      const cid = cidObj.cid;

      markInProgress(cid, cidObj.type);
      setLastPinnerActivity(new Date().toISOString());

      try {
        const result = await pinCid(cid);

        if (result.success) {
          // Already pinned - mark as complete and clear in-progress
          const sizeMB = (result.size / 1024 / 1024).toFixed(2);
          updatePinSize(cid, result.size, "pinned");
          clearInProgress(cid);
          if (!result.alreadyPinned) {
            console.log(`[PIN] ✓ ${cid.slice(0, 12)}... ${sizeMB} MB`);
          }
        } else if (result.pending) {
          // Pin started in background - keep in-progress, will check later
          updatePinSize(cid, 0, "pending");
          console.log(`[PIN] → ${cid.slice(0, 12)}... ${result.message}`);
          // Don't clear in-progress - let it expire after 3 hours
        } else {
          // Failed - mark and clear in-progress
          updatePinSize(cid, 0, "failed");
          clearInProgress(cid);
          console.log(`[PIN] ✗ ${cid.slice(0, 12)}... ${result.message}`);
        }
      } catch (err) {
        updatePinSize(cid, 0, "failed");
        clearInProgress(cid);
        console.error(`[PIN] ✗ ${cid.slice(0, 12)}... ${err.message}`);
      }

      // Short delay - can move fast since we're not blocking on pins
      const delaySeconds = 5 + Math.floor(Math.random() * 11); // 5-15 seconds
      await new Promise((resolve) => setTimeout(resolve, delaySeconds * 1000));
    } catch (err) {
      console.error(`[JOB] PINNER_LOOP_ERROR error="${err.message}"`);
      await new Promise((resolve) => setTimeout(resolve, 5000));
    }
  }
};

module.exports = {
  runNostrJob,
  pinnerJob,
};
