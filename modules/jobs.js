// Nostr job management
const { syncNostrPins, syncFollowPins } = require("./nostr");

const { pinCid, cacheCid } = require("./ipfs");
const { 
  batchInsertCids,
  getPendingCidsByType,
  updatePinSize,
  countByTypeAndStatus,
  setLastPinnerActivity,
  setLastNostrRun,
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

    console.log(`[JOB] NOSTR_DISCOVERY_COMPLETE total_discovered=${allCids.length} inserted=${insertedCount} duplicates=${allCids.length - insertedCount} self_pending=${selfPending} friends_pending=${friendsPending}`);
    console.log(`[JOB] NOSTR_DISCOVERY_DETAILS self_events=${selfResult.eventsScanned} self_cids=${selfResult.cidsFound} friends_events=${friendsResult.eventsScanned} friends_cids=${friendsResult.cidsFound}`);
  } catch (err) {
    setLastNostrRun({
      at: new Date().toISOString(),
      self: null,
      friends: null,
      error: err.message,
    });
    console.error(`[JOB] NOSTR_DISCOVERY_ERROR error="${err.message}"`);
  }
};

// Pinner job - processes CIDs one at a time, sequentially
const pinnerJob = async () => {
  try {
    console.log(`[JOB] PINNER_START`);

    // Get pending counts
    const selfPending = countByTypeAndStatus('self', 'pending');
    const friendsPending = countByTypeAndStatus('friend', 'pending');
    
    console.log(`[JOB] PINNER_QUEUE_STATUS self_pending=${selfPending} friends_pending=${friendsPending}`);

    let didWork = false;

    // Process ONE self CID (pin it permanently)
    if (selfPending > 0) {
      const [cidObj] = getPendingCidsByType('self', 1);
      
      if (cidObj) {
        const cid = cidObj.cid;
        const primalLink = `https://primal.net/e/${cidObj.event_id}`;
        
        console.log(`[JOB] PINNER_PROCESSING_SELF cid=${cid} event_id=${cidObj.event_id} author=${cidObj.author} timestamp=${new Date(cidObj.timestamp * 1000).toISOString()} event_url=${primalLink}`);

        // Pin it (function handles "already pinned" check internally)
        const result = await pinCid(cid);
        
        if (result.success && !result.caching) {
          // Only mark as pinned if it's actually pinned (not just started caching)
          const sizeMB = (result.size / 1024 / 1024).toFixed(2);
          updatePinSize(cid, result.size, "pinned");
          console.log(`[JOB] PINNER_SELF_COMPLETE cid=${cid} status=pinned size_mb=${sizeMB} message="${result.message}"`);
          didWork = true;
        } else if (result.caching) {
          // Started caching in background, leave as pending to check again later
          console.log(`[JOB] PINNER_SELF_CACHING cid=${cid} status=pending action=background_download message="${result.message}"`);
        } else {
          updatePinSize(cid, 0, "failed");
          console.error(`[JOB] PINNER_SELF_FAILED cid=${cid} status=failed error="${result.message}"`);
          didWork = true;
        }
      }
    }

    // Process ONE friend CID (cache it, not pinned)
    if (friendsPending > 0) {
      const [cidObj] = getPendingCidsByType('friend', 1);
      
      if (cidObj) {
        const cid = cidObj.cid;
        const primalLink = `https://primal.net/e/${cidObj.event_id}`;
        
        console.log(`[JOB] PINNER_PROCESSING_FRIEND cid=${cid} event_id=${cidObj.event_id} author=${cidObj.author} timestamp=${new Date(cidObj.timestamp * 1000).toISOString()} event_url=${primalLink}`);

        // Cache it (function handles "already cached" check internally)
        const result = await cacheCid(cid);
        
        if (result.success && !result.caching) {
          // Only mark as cached if it's actually available locally
          const sizeMB = (result.size / 1024 / 1024).toFixed(2);
          updatePinSize(cid, result.size, "cached");
          console.log(`[JOB] PINNER_FRIEND_COMPLETE cid=${cid} status=cached size_mb=${sizeMB} message="${result.message}"`);
          didWork = true;
        } else if (result.caching) {
          // Started caching in background, leave as pending to check again later
          console.log(`[JOB] PINNER_FRIEND_CACHING cid=${cid} status=pending action=background_download message="${result.message}"`);
        } else {
          updatePinSize(cid, 0, "failed");
          console.error(`[JOB] PINNER_FRIEND_FAILED cid=${cid} status=failed error="${result.message}"`);
          didWork = true;
        }
      }
    }

    if (didWork) {
      setLastPinnerActivity(new Date().toISOString());
    }

    console.log(`[JOB] PINNER_COMPLETE work_done=${didWork}\n`);
  } catch (err) {
    console.error(`[JOB] PINNER_ERROR error="${err.message}"`);
    console.error(`[JOB] PINNER_ERROR_STACK stack="${err.stack}"`);
  }
};

module.exports = {
  runNostrJob,
  pinnerJob,
};
