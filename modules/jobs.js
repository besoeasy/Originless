// Nostr job management
const { syncNostrPins, syncFollowPins } = require("./nostr");

const {
  setLastPinnerActivity,
  setLastNostrRun,
} = require("./queue");

const { isPinned, pinCid, cacheCid } = require("./ipfs");
const { 
  batchInsertCids,
  getPendingCidsByType,
  updatePinSize,
  countByTypeAndStatus,
} = require("./database");

// Nostr discovery job
const runNostrJob = async (NPUB) => {
  if (!NPUB) {
    return;
  }

  if (Math.random() < 0.5) {
    console.log("Nostr discovery job: Executing (random trigger)");
  } else {
    console.log("Nostr discovery job: Skipping (random delay)");
    return;
  }

  try {
    // Fetch CIDs without pinning (dryRun = true)
    const selfResult = await syncNostrPins({ npubOrPubkey: NPUB, dryRun: true });
    const friendsResult = await syncFollowPins({ npubOrPubkey: NPUB, dryRun: true });

    // Prepare CIDs for database insertion
    const selfCids = (selfResult.plannedPins || []).map(cidObj => ({
      ...cidObj,
      type: 'self'
    }));
    
    const friendCids = (friendsResult.plannedAdds || []).map(cidObj => ({
      ...cidObj,
      type: 'friend'
    }));

    // Batch insert to database (duplicates automatically ignored)
    const allCids = [...selfCids, ...friendCids];
    const insertedCount = batchInsertCids(allCids);

    // Get current pending counts
    const selfPending = countByTypeAndStatus('self', 'pending');
    const friendsPending = countByTypeAndStatus('friend', 'pending');

    setLastNostrRun({
      at: new Date().toISOString(),
      self: {
        eventsScanned: selfResult.eventsScanned,
        cidsFound: selfResult.cidsFound,
        newCids: selfCids.length,
        pendingInDb: selfPending,
      },
      friends: {
        eventsScanned: friendsResult.eventsScanned,
        cidsFound: friendsResult.cidsFound,
        newCids: friendCids.length,
        pendingInDb: friendsPending,
      },
      error: null,
    });

    console.log("\n=== Discovery Summary ===");
    console.log({
      discovered: allCids.length,
      inserted: insertedCount,
      duplicates: allCids.length - insertedCount,
      database: {
        selfPending: selfPending,
        friendsPending: friendsPending,
      }
    });
  } catch (err) {
    setLastNostrRun({
      at: new Date().toISOString(),
      self: null,
      friends: null,
      error: err.message,
    });
    console.error("Nostr discovery job failed", err.message);
  }
};

// Pinner job - processes CIDs one at a time, sequentially
const pinnerJob = async () => {
  try {
    console.log(`\n════ Pinner Job Started ════`);

    // Get pending counts
    const selfPending = countByTypeAndStatus('self', 'pending');
    const friendsPending = countByTypeAndStatus('friend', 'pending');
    
    console.log(`Database Status: Self Pending=${selfPending}, Friends Pending=${friendsPending}`);

    let didWork = false;

    // Process ONE self CID (pin it permanently)
    if (selfPending > 0) {
      const [cidObj] = getPendingCidsByType('self', 1);
      
      if (cidObj) {
        const cid = cidObj.cid;
        const primalLink = `https://primal.net/e/${cidObj.event_id}`;
        
        console.log(`\n[Self] Processing CID: ${cid}`);
        console.log(`  Event: ${primalLink}`);
        console.log(`  Author: ${cidObj.author} | Time: ${new Date(cidObj.timestamp * 1000).toISOString()}`);

        // Pin it (function handles "already pinned" check internally)
        const result = await pinCid(cid);
        
        if (result.success) {
          updatePinSize(cid, result.size, "pinned");
          console.log(`✓ ${result.message}`);
        } else {
          updatePinSize(cid, 0, "failed");
          console.error(`✗ ${result.message}`);
        }
        
        didWork = true;
      }
    }

    // Process ONE friend CID (cache it, not pinned)
    if (friendsPending > 0) {
      const [cidObj] = getPendingCidsByType('friend', 1);
      
      if (cidObj) {
        const cid = cidObj.cid;
        const primalLink = `https://primal.net/e/${cidObj.event_id}`;
        
        console.log(`\n[Friend] Processing CID: ${cid}`);
        console.log(`  Event: ${primalLink}`);
        console.log(`  Author: ${cidObj.author} | Time: ${new Date(cidObj.timestamp * 1000).toISOString()}`);

        // Cache it (function handles "already cached" check internally)
        const result = await cacheCid(cid);
        
        if (result.success) {
          updatePinSize(cid, result.size, "cached");
          console.log(`✓ ${result.message}`);
        } else {
          updatePinSize(cid, 0, "failed");
          console.error(`✗ ${result.message}`);
        }
        
        didWork = true;
      }
    }

    if (didWork) {
      setLastPinnerActivity(new Date().toISOString());
    }

    console.log(`════ Pinner Job Complete ════\n`);
  } catch (err) {
    console.error(`\n❌ Pinner job error:`, err.message);
    console.error(`Stack trace:`, err.stack);
  }
};

module.exports = {
  runNostrJob,
  pinnerJob,
};
