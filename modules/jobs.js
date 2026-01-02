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
const runNostrJob = async (NPUB) => {
  if (!NPUB) {
    return;
  }

  if (Math.random() < 0.5) {
    console.log(`[JOB] NOSTR_DISCOVERY_EXECUTE random_trigger=true`);
  } else {
    console.log(`[JOB] NOSTR_DISCOVERY_SKIP random_delay=true`);
    return;
  }

  try {
    // Fetch CIDs without pinning (dryRun = true)
    const selfResult = await syncNostrPins({ npubOrPubkey: NPUB, dryRun: true });

    // Prepare CIDs for database insertion
    const selfCids = (selfResult.plannedPins || []).map(cidObj => ({
      ...cidObj,
      type: 'self'
    }));

    // Batch insert to database (duplicates automatically ignored)
    const insertedCount = batchInsertCids(selfCids);

    // Get current pending count
    const selfPending = countByTypeAndStatus('self', 'pending');

    setLastNostrRun({
      at: new Date().toISOString(),
      self: {
        eventsScanned: selfResult.eventsScanned,
        cidsFound: selfResult.cidsFound,
        newCids: selfCids.length,
        pendingInDb: selfPending,
      },
      error: null,
    });

    console.log(`[JOB] NOSTR_DISCOVERY_COMPLETE total_discovered=${selfCids.length} inserted=${insertedCount} duplicates=${selfCids.length - insertedCount} self_pending=${selfPending}`);
    console.log(`[JOB] NOSTR_DISCOVERY_DETAILS self_events=${selfResult.eventsScanned} self_cids=${selfResult.cidsFound}`);
  } catch (err) {
    setLastNostrRun({
      at: new Date().toISOString(),
      self: null,
      error: err.message,
    });
    console.error(`[JOB] NOSTR_DISCOVERY_ERROR error="${err.message}"`);
  }
};

// Pinner job - continuously picks random CIDs and ensures they're pinned
const pinnerJob = async () => {
  console.log(`[JOB] PINNER_LOOP_START continuous_mode=true`);
  
  while (true) {
    try {
      // Get a random CID from database
      const cidObj = getRandomCid();
      
      if (!cidObj) {
        console.log(`[JOB] PINNER_NO_CIDS waiting=44s`);
        await new Promise(resolve => setTimeout(resolve, 44 * 1000));
        continue;
      }
      
      const cid = cidObj.cid;
      
      markInProgress(cid, cidObj.type);
      setLastPinnerActivity(new Date().toISOString());
      
      try {
        const result = await pinCid(cid);
        
        if (result.success) {
          const sizeMB = (result.size / 1024 / 1024).toFixed(2);
          updatePinSize(cid, result.size, "pinned");
          if (!result.alreadyPinned) {
            console.log(`[PIN] ✓ ${cid.slice(0, 12)}... ${sizeMB} MB`);
          }
        } else {
          updatePinSize(cid, 0, "failed");
          console.log(`[PIN] ✗ ${cid.slice(0, 12)}... ${result.message}`);
        }
      } catch (err) {
        updatePinSize(cid, 0, "failed");
        console.error(`[PIN] ✗ ${cid.slice(0, 12)}... ${err.message}`);
      } finally {
        clearInProgress(cid);
      }
      
      // Random delay between 30-100 seconds before next iteration
      const delaySeconds = 30 + Math.floor(Math.random() * 71); // 30-100 seconds
      await new Promise(resolve => setTimeout(resolve, delaySeconds * 1000));
      
    } catch (err) {
      console.error(`[JOB] PINNER_LOOP_ERROR error="${err.message}"`);
      await new Promise(resolve => setTimeout(resolve, 5000));
    }
  }
};

module.exports = {
  runNostrJob,
  pinnerJob,
};
