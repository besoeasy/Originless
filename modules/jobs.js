// Background job management
const { pinCid } = require("./ipfs");
const {
  markInProgress,
  clearInProgress,
  updatePinSize,
  setLastPinnerActivity,
  getRandomCid,
} = require("./database");

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
  pinnerJob,
};
