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
  markInProgress,
  updateProgress,
  clearInProgress,
  isInProgress,
  getInProgressCids,
  cleanupStaleInProgress,
} = require("./database");

// Configuration for concurrent processing
const MAX_CONCURRENT_SELF = 2;  // Max concurrent pins for self CIDs
const MAX_CONCURRENT_FRIENDS = 3; // Max concurrent caches for friend CIDs

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

// Pinner job - processes multiple CIDs concurrently
const pinnerJob = async () => {
  try {
    console.log(`\n════ Pinner Job Started ════`);

    // Cleanup stale in-progress entries first
    cleanupStaleInProgress();

    // Get current in-progress operations
    const inProgressCids = getInProgressCids();
    const inProgressSelf = inProgressCids.filter(p => p.type === 'self').length;
    const inProgressFriends = inProgressCids.filter(p => p.type === 'friend').length;

    // Get counts
    const selfPending = countByTypeAndStatus('self', 'pending');
    const friendsPending = countByTypeAndStatus('friend', 'pending');
    
    console.log(`Database Status: Self Pending=${selfPending} (${inProgressSelf} in-progress), Friends Pending=${friendsPending} (${inProgressFriends} in-progress)`);

    let didWork = false;

    // Process self queue: pin CIDs (permanent) - up to MAX_CONCURRENT_SELF at a time
    const selfSlotsAvailable = MAX_CONCURRENT_SELF - inProgressSelf;
    if (selfSlotsAvailable > 0 && selfPending > 0) {
      const pendingCids = getPendingCidsByType('self', selfSlotsAvailable);
      
      for (const cidObj of pendingCids) {
        const cid = cidObj.cid;
        
        // Skip if already in progress
        if (isInProgress(cid)) {
          console.log(`[Self] CID ${cid} already in progress, skipping`);
          continue;
        }

        const primalLink = `https://primal.net/e/${cidObj.event_id}`;
        console.log(`\n[Self] Starting pin for CID: ${cid}`);
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
          // Mark as in-progress
          markInProgress(cid, 'self');
          
          // Fire-and-forget: start pinning without blocking
          pinCid(cid, (progress) => {
            // Update progress in database
            updateProgress(progress.cid, progress.bytes);
            const sizeMB = (progress.bytes / 1024 / 1024).toFixed(2);
            console.log(`[Self] ${progress.cid}: ${sizeMB} MB pinned`);
          })
            .then(async () => {
              console.log(`✓ Successfully pinned: ${cid}`);
              try {
                const size = await getCidSize(cid);
                updatePinSize(cid, size, "pinned");
              } catch (err) {
                updatePinSize(cid, 0, "pinned");
              }
              clearInProgress(cid);
            })
            .catch((err) => {
              console.error(`❌ Failed to pin ${cid}:`, err.message);
              updatePinSize(cid, 0, "failed");
              clearInProgress(cid);
            });
          
          didWork = true;
          console.log(`🚀 Pin started for ${cid} (non-blocking)`);
        }
      }
    }

    // Process friends queue: cache CIDs (ephemeral) - up to MAX_CONCURRENT_FRIENDS at a time
    const friendsSlotsAvailable = MAX_CONCURRENT_FRIENDS - inProgressFriends;
    if (friendsSlotsAvailable > 0 && friendsPending > 0) {
      const pendingCids = getPendingCidsByType('friend', friendsSlotsAvailable);
      
      for (const cidObj of pendingCids) {
        const cid = cidObj.cid;
        
        // Skip if already in progress
        if (isInProgress(cid)) {
          console.log(`[Friend] CID ${cid} already in progress, skipping`);
          continue;
        }

        const primalLink = `https://primal.net/e/${cidObj.event_id}`;
        console.log(`\n[Friend] Starting cache for CID: ${cid}`);
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
          // Mark as in-progress
          markInProgress(cid, 'friend');
          
          // Fire-and-forget: start caching without blocking
          addCid(cid, undefined, (progress) => {
            // Update progress in database
            updateProgress(progress.cid, progress.bytes);
            const sizeMB = (progress.bytes / 1024 / 1024).toFixed(2);
            console.log(`[Friend] ${progress.cid}: ${sizeMB} MB downloaded`);
          })
            .then((result) => {
              console.log(`✓ Successfully cached: ${cid}`);
              const size = result?.size || 0;
              updatePinSize(cid, size, "cached");
              clearInProgress(cid);
            })
            .catch((err) => {
              console.error(`❌ Failed to cache ${cid}:`, err.message);
              updatePinSize(cid, 0, "failed");
              clearInProgress(cid);
            });
          
          didWork = true;
          console.log(`🚀 Cache started for ${cid} (non-blocking)`);
        }
      }
    }

    if (didWork) {
      setLastPinnerActivity(new Date().toISOString());
      console.log(`\n⏰ Activity timestamp updated`);
    }

    // Show summary of in-progress operations
    const finalInProgress = getInProgressCids();
    if (finalInProgress.length > 0) {
      console.log(`\n📊 Currently processing ${finalInProgress.length} CID(s):`);
      finalInProgress.forEach(p => {
        const elapsedMin = (p.elapsed / 1000 / 60).toFixed(1);
        const sizeMB = p.bytes ? (p.bytes / 1024 / 1024).toFixed(2) + ' MB' : 'starting...';
        console.log(`  ${p.type}: ${p.cid} - ${elapsedMin} min - ${sizeMB}`);
      });
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
