// Nostr job management
const { syncNostrPins } = require("./nostr");

const { pinCid } = require("./ipfs");
const {
  batchInsertCids,
  getPendingCidsByType,
  markInProgress,
  clearInProgress,
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

// Pinner job - processes CIDs one at a time, sequentially
const pinnerJob = async () => {
  try {
    console.log(`[JOB] PINNER_START`);

    // Get pending count
    const selfPending = countByTypeAndStatus('self', 'pending');

    console.log(`[JOB] PINNER_QUEUE_STATUS self_pending=${selfPending}`);

    let didWork = false;

    // Process ONE self CID (pin it permanently)
    if (selfPending > 0) {
      const [cidObj] = getPendingCidsByType('self', 1);

      if (cidObj) {
        const cid = cidObj.cid;

        console.log(`[JOB] PINNER_PROCESSING_SELF cid=${cid} event_id=${cidObj.event_id} author=${cidObj.author} timestamp=${new Date(cidObj.timestamp * 1000).toISOString()} `);

        // Start pinning asynchronously so one bad CID can't stall the queue
        markInProgress(cid, 'self');
        didWork = true;
        setLastPinnerActivity(new Date().toISOString());

        pinCid(cid)
          .then((result) => {
            if (result.success && !result.fetching) {
              const sizeMB = (result.size / 1024 / 1024).toFixed(2);
              updatePinSize(cid, result.size, "pinned");
              console.log(`[JOB] PINNER_SELF_COMPLETE cid=${cid} status=pinned size_mb=${sizeMB} message="${result.message}"`);
            } else if (result.fetching) {
              // Started fetching in background, leave as pending to check again later
              console.log(`[JOB] PINNER_SELF_FETCHING cid=${cid} status=pending action=background_download message="${result.message}"`);
            } else {
              updatePinSize(cid, 0, "failed");
              console.error(`[JOB] PINNER_SELF_FAILED cid=${cid} status=failed error="${result.message}"`);
            }
          })
          .catch((err) => {
            updatePinSize(cid, 0, "failed");
            console.error(`[JOB] PINNER_SELF_FAILED cid=${cid} status=failed error="${err.message}"`);
          })
          .finally(() => {
            clearInProgress(cid);
          });
      }
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
