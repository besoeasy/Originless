// Nostr job management
const { syncNostrPins, syncFollowPins } = require("./nostr");

const {
  setLastPinnerActivity,
  setLastNostrRun,
} = require("./queue");

const { isPinned, pinCid, addCid, getCidSize } = require("./nostr");
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

// Pinner job
const pinnerJob = async () => {
  try {
    console.log(`\n════ Pinner Job Started ════`);

    // Get counts
    const selfPending = countByTypeAndStatus('self', 'pending');
    const friendsPending = countByTypeAndStatus('friend', 'pending');
    
    console.log(`Database Status: Self Pending=${selfPending}, Friends Pending=${friendsPending}`);

    let didWork = false;

    // Process self queue: pin CID (permanent)
    if (selfPending > 0) {
      const pendingCids = getPendingCidsByType('self', 1);
      
      if (pendingCids.length > 0) {
        const cidObj = pendingCids[0];
        const cid = cidObj.cid;

        const primalLink = `https://primal.net/e/${cidObj.event_id}`;
        console.log(`\n[Self] Processing CID: ${cid}`);
        console.log(`  Event: ${primalLink}`);
        console.log(`  Author: ${cidObj.author} | Time: ${new Date(cidObj.timestamp * 1000).toISOString()}`);

        // Check if already pinned in IPFS
        const alreadyPinned = await isPinned(cid);
        
        if (alreadyPinned) {
          console.log(`⏭️  Already pinned in IPFS, updating database`);
          try {
            const size = await getCidSize(cid);
            updatePinSize(cid, size, "pinned");
          } catch (err) {
            updatePinSize(cid, 0, "pinned");
          }
          didWork = true;
        } else {
          console.log(`\n[Self] Pinning CID: ${cid}`);
          
          // Fire-and-forget: start pinning without waiting
          pinCid(cid)
            .then(async () => {
              console.log(`✓ Successfully pinned: ${cid}`);
              try {
                const size = await getCidSize(cid);
                updatePinSize(cid, size, "pinned");
              } catch (err) {
                updatePinSize(cid, 0, "pinned");
              }
            })
            .catch((err) => {
              console.error(`❌ Failed to pin ${cid}:`, err.message);
              updatePinSize(cid, 0, "pending");
            });
          
          didWork = true;
        }
      }
    } else {
      console.log(`[Self] No pending CIDs in database`);
    }

    // Process friends queue: cache CID (ephemeral)
    if (friendsPending > 0) {
      const pendingCids = getPendingCidsByType('friend', 1);
      
      if (pendingCids.length > 0) {
        const cidObj = pendingCids[0];
        const cid = cidObj.cid;

        const primalLink = `https://primal.net/e/${cidObj.event_id}`;
        console.log(`\n[Friend] Processing CID: ${cid}`);
        console.log(`  Event: ${primalLink}`);
        console.log(`  Author: ${cidObj.author} | Time: ${new Date(cidObj.timestamp * 1000).toISOString()}`);

        // Check if already available locally in IPFS
        const alreadyAvailable = await isPinned(cid);
        
        if (alreadyAvailable) {
          console.log(`⏭️  Already available locally, updating database`);
          try {
            const size = await getCidSize(cid);
            updatePinSize(cid, size, "cached");
          } catch (err) {
            updatePinSize(cid, 0, "cached");
          }
          didWork = true;
        } else {
          console.log(`\n[Friend] Caching CID: ${cid}`);
          
          // Fire-and-forget: start caching without waiting
          addCid(cid)
            .then((result) => {
              console.log(`✓ Successfully cached: ${cid}`);
              const size = result?.size || 0;
              updatePinSize(cid, size, "cached");
            })
            .catch((err) => {
              console.error(`❌ Failed to cache ${cid}:`, err.message);
              updatePinSize(cid, 0, "pending");
            });
          
          didWork = true;
        }
      }
    } else {
      console.log(`[Friend] No pending CIDs in database`);
    }

    if (didWork) {
      setLastPinnerActivity(new Date().toISOString());
      console.log(`\n⏰ Activity timestamp updated`);
    } else {
      console.log(`\n⏸  No work performed - no pending CIDs`);
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
